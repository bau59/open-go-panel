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
	"os/user"
	"net/url"
	"strings"
	"sync"

	"github.com/bau59/open-go-panel/internal/journal"
	"github.com/bau59/open-go-panel/internal/state"
)

const recommendedSiteTemplate = `{domain} {
	encode zstd gzip
	reverse_proxy 127.0.0.1:{port}
	log
}`

const recommendedStaticSiteTemplate = `{domain} {
	encode zstd gzip
	root * {root}
	file_server
	log
}`

var domainRE = regexp.MustCompile(`^(?i:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+)$`)

type Site struct {
	AppID    int64  `json:"app_id"`
	Domain   string `json:"domain"`
	Port     int    `json:"port"`
	Root     string `json:"root,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Template string `json:"template,omitempty"`
}

type GlobalSettings struct {
	HTTPS                       bool
	Compression                 bool
	AccessLog                   bool
	SecurityHeaders             bool
	FrameProtection             bool
	HSTS                        bool
	ProxyDialTimeoutSeconds     int
	ProxyHeaderTimeoutSeconds   int
}

func defaultGlobalSettings() GlobalSettings {
	return GlobalSettings{HTTPS: true, Compression: true, AccessLog: true}
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

func (m *Manager) QueryLogs(ctx context.Context, query journal.Query) (journal.Result, error) {
	query.Service = "caddy.service"
	return journal.Read(ctx, query)
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

func (m *Manager) SetSite(ctx context.Context, appID int64, domain string, port int, root, kind string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	domain = strings.ToLower(strings.TrimSpace(domain))
	if !domainRE.MatchString(domain) {
		return errors.New("invalid domain")
	}
	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = "proxy"
	}
	if kind == "static" {
		if port != 0 {
			return errors.New("static sites must not use an app port")
		}
		root = filepath.Clean(strings.TrimSpace(root))
		if !filepath.IsAbs(root) || strings.ContainsAny(root, "\r\n") {
			return errors.New("static site root must be an absolute path")
		}
		if err := prepareStaticRoot(ctx, root); err != nil {
			return err
		}
	} else {
		kind = "proxy"
		if port < 1 || port > 65535 {
			return errors.New("invalid app port")
		}
		root = ""
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
			sites[i].Root = root
			sites[i].Kind = kind
			found = true
			break
		}
	}
	if !found {
		sites = append(sites, Site{AppID: appID, Domain: domain, Port: port, Root: root, Kind: kind})
	}

	return m.apply(ctx, sites)
}

// AddStandaloneDomain creates an unbound Caddy site. Negative IDs are reserved
// for standalone domains so existing app/domain associations remain unchanged.
func (m *Manager) AddStandaloneDomain(ctx context.Context, domain string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	domain = strings.ToLower(strings.TrimSpace(domain))
	if !domainRE.MatchString(domain) {
		return errors.New("invalid domain")
	}
	sites, err := m.load()
	if err != nil {
		return err
	}
	id := int64(-1)
	for _, site := range sites {
		if site.Domain == domain {
			return fmt.Errorf("domain %q is already configured", domain)
		}
		if site.AppID <= id {
			id = site.AppID - 1
		}
	}
	sites = append(sites, Site{AppID: id, Domain: domain, Kind: "parked"})
	return m.apply(ctx, sites)
}

// SetStandaloneRedirect switches a standalone domain between redirect and parked modes.
func (m *Manager) SetStandaloneRedirect(ctx context.Context, id int64, destination string) error {
	if id >= 0 { return errors.New("redirect requires an independent domain") }
	destination = strings.TrimSpace(destination)
	if destination != "" {
		u, err := url.Parse(destination)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") ||
			u.Hostname() == "" || u.User != nil || strings.ContainsAny(destination, "\r\n\t {}\"'\\") {
			return errors.New("invalid redirect destination")
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	sites, err := m.load()
	if err != nil { return err }
	for i := range sites {
		if sites[i].AppID != id { continue }
		if strings.TrimSpace(sites[i].Template) != "" {
			return errors.New("remove custom Caddy override before setting a redirect")
		}
		sites[i].Kind, sites[i].Root = "parked", ""
		if destination != "" { sites[i].Kind, sites[i].Root = "redirect", destination }
		return m.apply(ctx, sites)
	}
	return errors.New("domain not found")
}

// RenameSite changes the hostname while preserving the existing target and template.
func (m *Manager) RenameSite(ctx context.Context, id int64, domain string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	domain = strings.ToLower(strings.TrimSpace(domain))
	if !domainRE.MatchString(domain) {
		return errors.New("invalid domain")
	}
	sites, err := m.load()
	if err != nil {
		return err
	}
	found := false
	for _, site := range sites {
		if site.Domain == domain && site.AppID != id {
			return fmt.Errorf("domain %q is already configured", domain)
		}
	}
	for i := range sites {
		if sites[i].AppID == id {
			sites[i].Domain = domain
			found = true
			break
		}
	}
	if !found {
		return errors.New("domain not found")
	}
	return m.apply(ctx, sites)
}

// AttachStandaloneDomain converts a parked domain into the app's managed site.
// An app may still have only one domain, matching the existing UI and schema.
func (m *Manager) AttachStandaloneDomain(ctx context.Context, standaloneID, appID int64, port int, root, kind string) error {
	if standaloneID >= 0 || appID <= 0 {
		return errors.New("invalid domain or application")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	sites, err := m.load()
	if err != nil {
		return err
	}
	if kind == "static" {
		if port != 0 || !filepath.IsAbs(root) {
			return errors.New("invalid static application target")
		}
		if err := prepareStaticRoot(ctx, root); err != nil {
			return err
		}
	} else if port < 1 || port > 65535 {
		return errors.New("invalid application port")
	}
	for _, site := range sites {
		if site.AppID == appID {
			return errors.New("application already has a domain; disconnect it first")
		}
	}
	for i := range sites {
		if sites[i].AppID != standaloneID {
			continue
		}
		if strings.TrimSpace(sites[i].Template) != "" {
			return errors.New("remove the custom Caddy override before attaching this domain to an app")
		}
		sites[i].AppID = appID
		sites[i].Port = port
		sites[i].Root = root
		sites[i].Kind = kind
		sites[i].Template = ""
		return m.apply(ctx, sites)
	}
	return errors.New("standalone domain not found")
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
		if err := validateSiteTemplate(value, sites[i]); err != nil {
			return err
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


func (m *Manager) GlobalSettings() (GlobalSettings, error) {
	settings := defaultGlobalSettings()
	rows, err := m.store.DB().Query(`
		SELECT key, value FROM settings
		WHERE key IN ('caddy.https','caddy.compression','caddy.access_log',
			'caddy.security_headers','caddy.frame_protection','caddy.hsts',
			'caddy.proxy_dial_timeout','caddy.proxy_header_timeout')
	`)
	if err != nil {
		return settings, err
	}
	defer rows.Close()

	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return settings, err
		}
		enabled := value == "1"
		switch key {
		case "caddy.https":
			settings.HTTPS = enabled
		case "caddy.compression":
			settings.Compression = enabled
		case "caddy.access_log":
			settings.AccessLog = enabled
		case "caddy.security_headers":
			settings.SecurityHeaders = enabled
		case "caddy.frame_protection":
			settings.FrameProtection = enabled
		case "caddy.hsts":
			settings.HSTS = enabled
		case "caddy.proxy_dial_timeout":
			settings.ProxyDialTimeoutSeconds, _ = strconv.Atoi(value)
		case "caddy.proxy_header_timeout":
			settings.ProxyHeaderTimeoutSeconds, _ = strconv.Atoi(value)
		}
	}
	return settings, rows.Err()
}

func (m *Manager) SetGlobalSettings(ctx context.Context, settings GlobalSettings) error {
	if err := validateGlobalSettings(settings); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	previousSettings, err := m.GlobalSettings()
	if err != nil {
		return err
	}
	previousTemplate, err := m.Template()
	if err != nil {
		return err
	}

	writeSettings := func(s GlobalSettings, template string) error {
		tx, err := m.store.DB().Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		values := map[string]string{
			"caddy.https":        boolSetting(s.HTTPS),
			"caddy.compression":  boolSetting(s.Compression),
			"caddy.access_log":          boolSetting(s.AccessLog),
			"caddy.security_headers":   boolSetting(s.SecurityHeaders),
			"caddy.frame_protection":   boolSetting(s.FrameProtection),
			"caddy.hsts":               boolSetting(s.HSTS),
			"caddy.proxy_dial_timeout": strconv.Itoa(s.ProxyDialTimeoutSeconds),
			"caddy.proxy_header_timeout": strconv.Itoa(s.ProxyHeaderTimeoutSeconds),
			"caddy.site_template":      template,
		}
		for key, value := range values {
			if _, err := tx.Exec(`
				INSERT INTO settings(key, value) VALUES(?, ?)
				ON CONFLICT(key) DO UPDATE SET value=excluded.value
			`, key, value); err != nil {
				return err
			}
		}
		return tx.Commit()
	}

	newTemplate := managedProxyTemplate(settings)
	if err := writeSettings(settings, newTemplate); err != nil {
		return err
	}
	sites, err := m.load()
	if err != nil {
		_ = writeSettings(previousSettings, previousTemplate)
		return err
	}
	if err := m.applyLocked(ctx, sites, newTemplate); err != nil {
		_ = writeSettings(previousSettings, previousTemplate)
		return err
	}
	return nil
}

func boolSetting(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

// Caddy's built-in transport and header directives are used; no plugins required.
// Zero timeout means Caddy's native default (3s for dial, unlimited for response headers).
func validateGlobalSettings(settings GlobalSettings) error {
	if settings.HSTS && !settings.HTTPS {
		return errors.New("HSTS requires Automatic HTTPS")
	}
	allowedDial := map[int]bool{0: true, 3: true, 5: true, 10: true, 30: true}
	if !allowedDial[settings.ProxyDialTimeoutSeconds] {
		return errors.New("invalid reverse proxy connection timeout")
	}
	allowedHeader := map[int]bool{0: true, 10: true, 30: true, 60: true, 120: true}
	if !allowedHeader[settings.ProxyHeaderTimeoutSeconds] {
		return errors.New("invalid reverse proxy response-header timeout")
	}
	return nil
}

func securityHeaderLines(settings GlobalSettings) []string {
	var headers []string
	if settings.SecurityHeaders {
		headers = append(headers, "\t\tX-Content-Type-Options nosniff",
			"\t\tReferrer-Policy strict-origin-when-cross-origin")
	}
	if settings.FrameProtection {
		headers = append(headers, "\t\tX-Frame-Options SAMEORIGIN")
	}
	if settings.HSTS && settings.HTTPS {
		// A short, opt-in max-age; do not force includeSubDomains or preload.
		headers = append(headers, "\t\tStrict-Transport-Security \"max-age=604800\"")
	}
	if len(headers) == 0 {
		return nil
	}
	return append(append([]string{"\theader {"}, headers...), "\t}")
}

func managedProxyTemplate(settings GlobalSettings) string {
	address := "{domain}"
	if !settings.HTTPS {
		address = "http://{domain}"
	}
	var lines []string
	lines = append(lines, address+" {")
	if settings.Compression {
		lines = append(lines, "\tencode zstd gzip")
	}
	lines = append(lines, securityHeaderLines(settings)...)
	if settings.ProxyDialTimeoutSeconds == 0 && settings.ProxyHeaderTimeoutSeconds == 0 {
		lines = append(lines, "\treverse_proxy 127.0.0.1:{port}")
	} else {
		lines = append(lines, "\treverse_proxy 127.0.0.1:{port} {",
			"\t\ttransport http {")
		if settings.ProxyDialTimeoutSeconds > 0 {
			lines = append(lines, fmt.Sprintf("\t\t\tdial_timeout %ds", settings.ProxyDialTimeoutSeconds))
		}
		if settings.ProxyHeaderTimeoutSeconds > 0 {
			lines = append(lines, fmt.Sprintf("\t\t\tresponse_header_timeout %ds", settings.ProxyHeaderTimeoutSeconds))
		}
		lines = append(lines, "\t\t}", "\t}")
	}
	if settings.AccessLog {
		lines = append(lines, "\tlog")
	}
	lines = append(lines, "}")
	return strings.Join(lines, "\n")
}

func managedStaticTemplate(settings GlobalSettings) string {
	address := "{domain}"
	if !settings.HTTPS {
		address = "http://{domain}"
	}
	var lines []string
	lines = append(lines, address+" {")
	if settings.Compression {
		lines = append(lines, "\tencode zstd gzip")
	}
	lines = append(lines, securityHeaderLines(settings)...)
	lines = append(lines, "\troot * {root}", "\tfile_server")
	if settings.AccessLog {
		lines = append(lines, "\tlog")
	}
	lines = append(lines, "}")
	return strings.Join(lines, "\n")
}

// ManagedSiteTemplate returns the generated defaults for an individual site.
// Custom site overrides are applied only by the caller during configuration reload.
func ManagedSiteTemplate(site Site, settings GlobalSettings) string {
	switch site.Kind {
	case "parked", "redirect":
		address := "{domain}"
		if !settings.HTTPS {
			address = "http://{domain}"
		}
		lines := []string{address + " {"}
		lines = append(lines, securityHeaderLines(settings)...)
		if site.Kind == "redirect" {
			lines = append(lines, "\tredir {root} 301")
		} else {
			lines = append(lines, "\trespond \"Domain not configured\" 404")
		}
		return strings.Join(append(lines, "}"), "\n")
	case "static":
		return managedStaticTemplate(settings)
	default:
		if site.Port == 0 {
			return managedStaticTemplate(settings)
		}
		return managedProxyTemplate(settings)
	}
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

func (m *Manager) DefaultTemplateForSite(site Site) string {
	settings, err := m.GlobalSettings()
	if err != nil {
		settings = defaultGlobalSettings()
	}
	if site.Kind == "static" || site.Kind == "parked" || site.Kind == "redirect" || site.Port == 0 {
		return ManagedSiteTemplate(site, settings)
	}
	template, err := m.Template()
	if err != nil || strings.TrimSpace(template) == "" {
		return managedProxyTemplate(settings)
	}
	return template
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
		if site.Kind == "static" || site.Kind == "parked" || site.Kind == "redirect" || site.Port == 0 {
			settings, err := m.GlobalSettings()
			if err != nil {
				settings = defaultGlobalSettings()
			}
			siteTemplate = ManagedSiteTemplate(site, settings)
		}
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

func prepareStaticRoot(ctx context.Context, root string) error {
	if _, err := exec.LookPath("setfacl"); err != nil {
		return errors.New("static sites require the acl package (setfacl)")
	}
	if _, err := user.Lookup("caddy"); err != nil {
		return errors.New("Caddy system user is not available")
	}

	parents := []string{filepath.Dir(root), filepath.Dir(filepath.Dir(root))}
	for _, path := range parents {
		if path == "." || path == "/" {
			continue
		}
		if out, err := exec.CommandContext(ctx, "setfacl", "-m", "u:caddy:--x", path).CombinedOutput(); err != nil {
			return fmt.Errorf("grant Caddy directory traversal on %s: %w: %s", path, err, strings.TrimSpace(string(out)))
		}
	}
	if out, err := exec.CommandContext(ctx, "setfacl", "-R", "-m", "u:caddy:rX", root).CombinedOutput(); err != nil {
		return fmt.Errorf("grant Caddy read access on %s: %w: %s", root, err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.CommandContext(ctx, "setfacl", "-m", "d:u:caddy:rX", root).CombinedOutput(); err != nil {
		return fmt.Errorf("set default Caddy ACL on %s: %w: %s", root, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func renderSite(template string, site Site) string {
	value := strings.ReplaceAll(template, "{domain}", site.Domain)
	value = strings.ReplaceAll(value, "{port}", strconv.Itoa(site.Port))
	value = strings.ReplaceAll(value, "{root}", site.Root)
	return strings.TrimSpace(value)
}

func validateSiteTemplate(value string, site Site) error {
	if !strings.Contains(value, "{domain}") {
		return errors.New("Caddy template must contain {domain}")
	}
	if site.Kind == "parked" {
		return nil
	}
	if site.Kind == "redirect" {
		if !strings.Contains(value, "{root}") { return errors.New("redirect template must contain {root}") }
		return nil
	}
	if site.Kind == "static" || site.Port == 0 {
		if !strings.Contains(value, "{root}") {
			return errors.New("static Caddy template must contain {root}")
		}
		return nil
	}
	if !strings.Contains(value, "{port}") {
		return errors.New("proxy Caddy template must contain {port}")
	}
	return nil
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
		SELECT app_id, domain, port, root, kind, template
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
		if err := rows.Scan(&site.AppID, &site.Domain, &site.Port, &site.Root, &site.Kind, &site.Template); err != nil {
			return nil, fmt.Errorf("scan domain state: %w", err)
		}
		sites = append(sites, site)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate domains state: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close domains rows: %w", err)
	}

	standaloneRows, err := m.store.DB().Query("SELECT id, domain, port, root, kind, template FROM standalone_domains ORDER BY domain")
	if err != nil {
		return nil, fmt.Errorf("query standalone domains: %w", err)
	}
	defer standaloneRows.Close()
	for standaloneRows.Next() {
		var site Site
		if err := standaloneRows.Scan(&site.AppID, &site.Domain, &site.Port, &site.Root, &site.Kind, &site.Template); err != nil {
			return nil, fmt.Errorf("scan standalone domain: %w", err)
		}
		sites = append(sites, site)
	}
	if err := standaloneRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate standalone domains: %w", err)
	}
	if err := standaloneRows.Close(); err != nil {
		return nil, fmt.Errorf("close standalone domains rows: %w", err)
	}

	if len(sites) > 0 {
		if _, done, err := m.store.Setting("migration.caddy_sites_json_done"); err != nil {
			return nil, err
		} else if !done {
			if err := m.store.SetSetting("migration.caddy_sites_json_done", "1"); err != nil {
				return nil, fmt.Errorf("mark Caddy migration complete: %w", err)
			}
		}
	}
	if len(sites) == 0 {
		if _, done, err := m.store.Setting("migration.caddy_sites_json_done"); err != nil {
			return nil, err
		} else if !done {
			legacy, err := m.loadLegacy()
			if err != nil {
				return nil, err
			}
			if len(legacy) > 0 {
				if err := m.save(legacy); err != nil {
					return nil, fmt.Errorf("migrate legacy domains state: %w", err)
				}
				sites = legacy
			}
			if err := m.store.SetSetting("migration.caddy_sites_json_done", "1"); err != nil {
				return nil, fmt.Errorf("mark Caddy migration complete: %w", err)
			}
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
	standaloneKeep := make(map[int64]struct{})
	for _, site := range sites {
		if site.AppID < 0 {
			if _, err := tx.Exec(`INSERT INTO standalone_domains(id, domain, port, root, kind, template)
				VALUES(?, ?, ?, ?, ?, ?)
				ON CONFLICT(id) DO UPDATE SET domain=excluded.domain, port=excluded.port,
					root=excluded.root, kind=excluded.kind, template=excluded.template`,
				site.AppID, site.Domain, site.Port, site.Root, site.Kind, site.Template); err != nil {
				return fmt.Errorf("save standalone domain %s: %w", site.Domain, err)
			}
			standaloneKeep[site.AppID] = struct{}{}
			continue
		}
		if _, err := tx.Exec(`
			INSERT INTO domains(app_id, domain, port, root, kind, template)
			VALUES(?, ?, ?, ?, ?, ?)
			ON CONFLICT(app_id) DO UPDATE SET
				domain=excluded.domain,
				port=excluded.port,
				root=excluded.root,
				kind=excluded.kind,
				template=excluded.template
		`, site.AppID, site.Domain, site.Port, site.Root, site.Kind, site.Template); err != nil {
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

	standaloneRows, err := tx.Query("SELECT id FROM standalone_domains")
	if err != nil {
		return err
	}
	var staleStandalone []int64
	for standaloneRows.Next() {
		var id int64
		if err := standaloneRows.Scan(&id); err != nil {
			standaloneRows.Close()
			return err
		}
		if _, ok := standaloneKeep[id]; !ok {
			staleStandalone = append(staleStandalone, id)
		}
	}
	if err := standaloneRows.Err(); err != nil {
		standaloneRows.Close()
		return err
	}
	standaloneRows.Close()
	for _, id := range staleStandalone {
		if _, err := tx.Exec("DELETE FROM standalone_domains WHERE id = ?", id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s Site) Target() string {
	if s.Kind == "parked" {
		return "Unassigned"
	}
	if s.Kind == "redirect" { return s.Root }
	if s.Kind == "static" || s.Port == 0 {
		return s.Root
	}
	return "127.0.0.1:" + strconv.Itoa(s.Port)
}
