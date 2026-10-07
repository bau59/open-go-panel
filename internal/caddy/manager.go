package caddy

import (
	"context"
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
	mu           sync.Mutex
	stateFile    string
	configFile   string
	templateFile string
}

func New(stateFile string) *Manager {
	return &Manager{
		stateFile:    stateFile,
		configFile:   "/etc/caddy/Caddyfile",
		templateFile: filepath.Join(filepath.Dir(stateFile), "caddy-site-template.txt"),
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
	data, err := os.ReadFile(m.templateFile)
	if errors.Is(err, os.ErrNotExist) {
		return recommendedSiteTemplate, nil
	}
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return recommendedSiteTemplate, nil
	}
	return value, nil
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
	if err := os.MkdirAll(filepath.Dir(m.templateFile), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(m.templateFile, []byte(value+"\n"), 0600); err != nil {
		return err
	}
	sites, err := m.load()
	if err != nil {
		return err
	}
	return m.applyLocked(ctx, sites, value)
}

func (m *Manager) ResetTemplate(ctx context.Context) error {
	return m.SetTemplate(ctx, recommendedSiteTemplate)
}

func (m *Manager) Config() (string, error) {
	data, err := os.ReadFile(m.configFile)
	if errors.Is(err, os.ErrNotExist) { return "", nil }
	if err != nil { return "", err }
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
	if err := os.MkdirAll(filepath.Dir(m.configFile), 0755); err != nil { return err }

	sort.Slice(sites, func(i, j int) bool { return sites[i].Domain < sites[j].Domain })

	var b strings.Builder
	b.WriteString("# Managed by Open Go Panel\n\n")
	for _, site := range sites {
		b.WriteString(renderSite(template, site))
		b.WriteString("\n\n")
	}

	if err := m.validateRendered(ctx, b.String()); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(m.configFile), ".Caddyfile-*")
	if err != nil { return err }
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.WriteString(b.String()); err != nil { _ = tmp.Close(); return err }
	if err := tmp.Close(); err != nil { return err }

	if err := os.Rename(tmpName, m.configFile); err != nil { return err }
	if err := os.Chmod(m.configFile, 0644); err != nil { return err }

	if out, err := exec.CommandContext(ctx, "systemctl", "reload", "caddy.service").CombinedOutput(); err != nil {
		return fmt.Errorf("reload caddy: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return m.save(sites)
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
	data, err := os.ReadFile(m.stateFile)
	if errors.Is(err, os.ErrNotExist) { return []Site{}, nil }
	if err != nil { return nil, err }
	if len(strings.TrimSpace(string(data))) == 0 { return []Site{}, nil }

	var sites []Site
	if err := json.Unmarshal(data, &sites); err != nil { return nil, err }
	return sites, nil
}

func (m *Manager) save(sites []Site) error {
	if err := os.MkdirAll(filepath.Dir(m.stateFile), 0755); err != nil { return err }
	data, err := json.MarshalIndent(sites, "", "  ")
	if err != nil { return err }
	data = append(data, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(m.stateFile), ".caddy-sites-*")
	if err != nil { return err }
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil { _ = tmp.Close(); return err }
	if err := tmp.Chmod(0600); err != nil { _ = tmp.Close(); return err }
	if err := tmp.Close(); err != nil { return err }
	return os.Rename(tmpName, m.stateFile)
}

func (s Site) Target() string {
	return "127.0.0.1:" + strconv.Itoa(s.Port)
}
