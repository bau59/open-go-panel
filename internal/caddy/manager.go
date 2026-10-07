package caddy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/bau59/open-go-panel/internal/state"
)

const recommendedSiteTemplate = `{domain} {
	encode zstd gzip
	reverse_proxy 127.0.0.1:{port}
	log
}`

var domainRE = regexp.MustCompile(`^(?i:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+)$`)

type Site struct {
	AppID    int64  `json:"app_id"`
	Domain   string `json:"domain"`
	Port     int    `json:"port"`
	Template string `json:"template,omitempty"`
}

type Manager struct {
	mu                 sync.Mutex
	store              *state.Store
	legacyStateFile    string
	configFile         string
	managedDir         string
	managedFile        string
	legacyTemplateFile string
}

func New(store *state.Store, legacyStateFile string) *Manager {
	return &Manager{
		store:              store,
		legacyStateFile:    legacyStateFile,
		configFile:         "/etc/caddy/Caddyfile",
		managedDir:         "/etc/caddy/open-go-panel",
		managedFile:        "/etc/caddy/open-go-panel/sites.caddy",
		legacyTemplateFile: filepath.Join(filepath.Dir(legacyStateFile), "caddy-site-template.txt"),
	}
}

func (m *Manager) Status(ctx context.Context) string {
	if _, err := exec.LookPath("caddy"); err != nil {
		return "not installed"
	}
	cmd := exec.CommandContext(ctx, "systemctl", "is-active", "caddy.service")
	out, err := cmd.Output()
	if err != nil {
		if s := strings.TrimSpace(string(out)); s != "" { return s }
		return "inactive"
	}
	return strings.TrimSpace(string(out))
}

func (m *Manager) Sites() ([]Site, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.load()
}

func (m *Manager) SiteForApp(appID int64) (Site, bool, error) {
	sites, err := m.Sites()
	if err != nil { return Site{}, false, err }
	for _, site := range sites {
		if site.AppID == appID { return site, true, nil }
	}
	return Site{}, false, nil
}

func (m *Manager) SetSite(ctx context.Context, appID int64, domain string, port int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	domain = strings.ToLower(strings.TrimSpace(domain))
	if !domainRE.MatchString(domain) {
		return errors.New("invalid domain")
	}
	if port < 1 || port > 65535 {
		return errors.New("invalid app port")
	}

	sites, err := m.load()
	if err != nil { return err }

	for _, site := range sites {
		if site.Domain == domain && site.AppID != appID {
			return fmt.Errorf("domain %q is already assigned", domain)
		}
	}

	found := false
	for i := range sites {
		if sites[i].AppID == appID {
			sites[i].Domain = domain
			sites[i].Port = port
			found = true
			break
		}
	}
	if !found {
		sites = append(sites, Site{AppID: appID, Domain: domain, Port: port})
	}

	return m.apply(ctx, sites)
}

func (m *Manager) SetSiteTemplate(ctx context.Context, appID int64, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	sites, err := m.load()
	if err != nil {
		return err
	}
	found := false
	for i := range sites {
		if sites[i].AppID != appID {
			continue
		}
		found = true
		value = strings.TrimSpace(value)
		if value == "" {
			sites[i].Template = ""
			break
		}
		if !strings.Contains(value, "{domain}") || !strings.Contains(value, "{port}") {
			return errors.New("Caddy template must contain {domain} and {port}")
		}
		if err := m.validateRendered(ctx, renderSite(value, sites[i])); err != nil {
			return err
		}
		sites[i].Template = value
		break
	}
	if !found {
		return fmt.Errorf("site for app %d not found", appID)
	}

	globalTemplate, err := m.Template()
	if err != nil {
		return err
	}
	return m.applyLocked(ctx, sites, globalTemplate)
}

func (m *Manager) RemoveSite(ctx context.Context, appID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	sites, err := m.load()
	if err != nil { return err }

	filtered := sites[:0]
	for _, site := range sites {
		if site.AppID != appID { filtered = append(filtered, site) }
	}
	return m.apply(ctx, filtered)
}


func (m *Manager) Template() (string, error) {
	var value string
	err := m.store.DB().QueryRow(`SELECT value FROM settings WHERE key = 'caddy.site_template'`).Scan(&value)
	if err == nil {
		value = strings.TrimSpace(value)
		if value != "" {
			return value, nil
		}
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("read Caddy template setting: %w", err)
	}

	data, legacyErr := os.ReadFile(m.legacyTemplateFile)
	if legacyErr == nil {
		value = strings.TrimSpace(string(data))
		if value != "" {
			_, _ = m.store.DB().Exec(`
				INSERT INTO settings(key, value) VALUES('caddy.site_template', ?)
				ON CONFLICT(key) DO UPDATE SET value=excluded.value
			`, value)
			return value, nil
		}
	} else if !errors.Is(legacyErr, os.ErrNotExist) {
		return "", legacyErr
	}
	return recommendedSiteTemplate, nil
}

func (m *Manager) RecommendedTemplate() string {
	return recommendedSiteTemplate
}

func (m *Manager) SetTemplate(ctx context.Context, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("Caddy site template cannot be empty")
	}
	if !strings.Contains(value, "{domain}") || !strings.Contains(value, "{port}") {
		return errors.New("Caddy template must contain {domain} and {port}")
	}
	if err := m.validateRendered(ctx, renderSite(value, Site{Domain: "example.com", Port: 8100})); err != nil {
		return err
	}
	previous, err := m.Template()
	if err != nil {
		return err
	}
	if _, err := m.store.DB().Exec(`
		INSERT INTO settings(key, value) VALUES('caddy.site_template', ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value
	`, value); err != nil {
		return fmt.Errorf("save Caddy template setting: %w", err)
	}
	sites, err := m.load()
	if err != nil {
		_, _ = m.store.DB().Exec(`
			INSERT INTO settings(key, value) VALUES('caddy.site_template', ?)
			ON CONFLICT(key) DO UPDATE SET value=excluded.value
		`, previous)
		return err
	}
	if err := m.applyLocked(ctx, sites, value); err != nil {
		_, _ = m.store.DB().Exec(`
			INSERT INTO settings(key, value) VALUES('caddy.site_template', ?)
			ON CONFLICT(key) DO UPDATE SET value=excluded.value
		`, previous)
		return err
	}
	return nil
}

func (m *Manager) ResetTemplate(ctx context.Context) error {
	return m.SetTemplate(ctx, recommendedSiteTemplate)
}

func (m *Manager) Config() (string, error) {
	data, err := os.ReadFile(m.managedFile)
	if errors.Is(err, os.ErrNotExist) {
		data, err = os.ReadFile(m.configFile)
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (m *Manager) apply(ctx context.Context, sites []Site) error {
	template, err := m.Template()
	if err != nil {
		return err
	}
	return m.applyLocked(ctx, sites, template)
}

func (m *Manager) applyLocked(ctx context.Context, sites []Site, template string) error {
	if _, err := exec.LookPath("caddy"); err != nil {
		return errors.New("Caddy is not installed")
	}
	if err := os.MkdirAll(m.managedDir, 0755); err != nil {
		return err
	}

	sort.Slice(sites, func(i, j int) bool { return sites[i].Domain < sites[j].Domain })

	var b strings.Builder
	b.WriteString("# Managed by Open Go Panel\n\n")
	for _, site := range sites {
		siteTemplate := template
		if strings.TrimSpace(site.Template) != "" {
			siteTemplate = site.Template
		}
		b.WriteString(renderSite(siteTemplate, site))
		b.WriteString("\n\n")
	}
	managedContent := b.String()
	if err := m.validateRendered(ctx, managedContent); err != nil {
		return err
	}

	oldManaged, managedErr := os.ReadFile(m.managedFile)
	hadManaged := managedErr == nil
	if managedErr != nil && !errors.Is(managedErr, os.ErrNotExist) {
		return managedErr
	}

	oldRoot, rootErr := os.ReadFile(m.configFile)
	hadRoot := rootErr == nil
	if rootErr != nil && !errors.Is(rootErr, os.ErrNotExist) {
		return rootErr
	}

	if err := os.WriteFile(m.managedFile, []byte(managedContent), 0644); err != nil {
		return fmt.Errorf("write managed Caddy config: %w", err)
	}

	rootContent := string(oldRoot)
	importLine := "import /etc/caddy/open-go-panel/*.caddy"
	if !hadRoot || strings.TrimSpace(rootContent) == "" || strings.HasPrefix(strings.TrimSpace(rootContent), "# Managed by Open Go Panel") {
		rootContent = "# Open Go Panel keeps its sites in /etc/caddy/open-go-panel/*.caddy\n" + importLine + "\n"
	} else if !strings.Contains(rootContent, importLine) {
		rootContent = strings.TrimRight(rootContent, "\n") + "\n\n# Open Go Panel managed sites\n" + importLine + "\n"
	}
	if err := os.WriteFile(m.configFile, []byte(rootContent), 0644); err != nil {
		m.restoreConfig(oldManaged, hadManaged, oldRoot, hadRoot)
		return fmt.Errorf("write Caddy root config: %w", err)
	}

	if out, err := exec.CommandContext(ctx, "caddy", "validate", "--config", m.configFile, "--adapter", "caddyfile").CombinedOutput(); err != nil {
		m.restoreConfig(oldManaged, hadManaged, oldRoot, hadRoot)
		return fmt.Errorf("caddy validate: %w: %s", err, strings.TrimSpace(string(out)))
	}

	if out, err := exec.CommandContext(ctx, "systemctl", "reload", "caddy.service").CombinedOutput(); err != nil {
		m.restoreConfig(oldManaged, hadManaged, oldRoot, hadRoot)
		_ = exec.CommandContext(ctx, "systemctl", "reload", "caddy.service").Run()
		return fmt.Errorf("reload caddy: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return m.save(sites)
}

func (m *Manager) restoreConfig(oldManaged []byte, hadManaged bool, oldRoot []byte, hadRoot bool) {
	if hadManaged {
		_ = os.WriteFile(m.managedFile, oldManaged, 0644)
	} else {
		_ = os.Remove(m.managedFile)
	}
	if hadRoot {
		_ = os.WriteFile(m.configFile, oldRoot, 0644)
	} else {
		_ = os.Remove(m.configFile)
	}
}

func renderSite(template string, site Site) string {
	value := strings.ReplaceAll(template, "{domain}", site.Domain)
	value = strings.ReplaceAll(value, "{port}", strconv.Itoa(site.Port))
	return strings.TrimSpace(value)
}

func (m *Manager) validateRendered(ctx context.Context, config string) error {
	tmp, err := os.CreateTemp("", "open-go-panel-caddy-*.Caddyfile")
	if err != nil {
		return err
	}
	path := tmp.Name()
	defer os.Remove(path)
	if _, err := tmp.WriteString(config + "\n"); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if out, err := exec.CommandContext(ctx, "caddy", "validate", "--config", path, "--adapter", "caddyfile").CombinedOutput(); err != nil {
		return fmt.Errorf("caddy validate: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (m *Manager) load() ([]Site, error) {
	rows, err := m.store.DB().Query(`
		SELECT app_id, domain, port, template
		FROM domains
		ORDER BY domain
	`)
	if err != nil {
		return nil, fmt.Errorf("query domains state: %w", err)
	}
	defer rows.Close()

	var sites []Site
	for rows.Next() {
		var site Site
		if err := rows.Scan(&site.AppID, &site.Domain, &site.Port, &site.Template); err != nil {
			return nil, fmt.Errorf("scan domain state: %w", err)
		}
		sites = append(sites, site)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate domains state: %w", err)
	}

	if len(sites) == 0 {
		legacy, err := m.loadLegacy()
		if err != nil {
			return nil, err
		}
		if len(legacy) > 0 {
			if err := m.save(legacy); err != nil {
				return nil, fmt.Errorf("migrate legacy domains state: %w", err)
			}
			return legacy, nil
		}
	}
	return sites, nil
}

func (m *Manager) loadLegacy() ([]Site, error) {
	data, err := os.ReadFile(m.legacyStateFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil
	}
	var sites []Site
	if err := json.Unmarshal(data, &sites); err != nil {
		return nil, err
	}
	return sites, nil
}

func (m *Manager) save(sites []Site) error {
	tx, err := m.store.DB().Begin()
	if err != nil {
		return fmt.Errorf("begin domains state transaction: %w", err)
	}
	defer tx.Rollback()

	keep := make(map[int64]struct{}, len(sites))
	for _, site := range sites {
		if _, err := tx.Exec(`
			INSERT INTO domains(app_id, domain, port, template)
			VALUES(?, ?, ?, ?)
			ON CONFLICT(app_id) DO UPDATE SET
				domain=excluded.domain,
				port=excluded.port,
				template=excluded.template
		`, site.AppID, site.Domain, site.Port, site.Template); err != nil {
			return fmt.Errorf("save domain %s state: %w", site.Domain, err)
		}
		keep[site.AppID] = struct{}{}
	}

	rows, err := tx.Query(`SELECT app_id FROM domains`)
	if err != nil {
		return fmt.Errorf("query existing domain ids: %w", err)
	}
	var stale []int64
	for rows.Next() {
		var appID int64
		if err := rows.Scan(&appID); err != nil {
			rows.Close()
			return err
		}
		if _, ok := keep[appID]; !ok {
			stale = append(stale, appID)
		}
	}
	rows.Close()
	for _, appID := range stale {
		if _, err := tx.Exec(`DELETE FROM domains WHERE app_id = ?`, appID); err != nil {
			return fmt.Errorf("delete stale domain state for app %d: %w", appID, err)
		}
	}

	return tx.Commit()
}

func (s Site) Target() string {
	return "127.0.0.1:" + strconv.Itoa(s.Port)
}
