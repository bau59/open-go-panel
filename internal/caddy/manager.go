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

var domainRE = regexp.MustCompile(`^(?i:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+)$`)

type Site struct {
	AppID  int64  `json:"app_id"`
	Domain string `json:"domain"`
	Port   int    `json:"port"`
}

type Manager struct {
	mu        sync.Mutex
	stateFile string
	configFile string
}

func New(stateFile string) *Manager {
	return &Manager{
		stateFile: stateFile,
		configFile: "/etc/caddy/Caddyfile",
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
			sites[i] = Site{AppID: appID, Domain: domain, Port: port}
			found = true
			break
		}
	}
	if !found {
		sites = append(sites, Site{AppID: appID, Domain: domain, Port: port})
	}

	return m.apply(ctx, sites)
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

func (m *Manager) Config() (string, error) {
	data, err := os.ReadFile(m.configFile)
	if errors.Is(err, os.ErrNotExist) { return "", nil }
	if err != nil { return "", err }
	return string(data), nil
}

func (m *Manager) apply(ctx context.Context, sites []Site) error {
	if _, err := exec.LookPath("caddy"); err != nil {
		return errors.New("Caddy is not installed")
	}
	if err := os.MkdirAll(filepath.Dir(m.configFile), 0755); err != nil { return err }

	sort.Slice(sites, func(i, j int) bool { return sites[i].Domain < sites[j].Domain })

	var b strings.Builder
	b.WriteString("# Managed by Open Go Panel\n\n")
	for _, site := range sites {
		fmt.Fprintf(&b, "%s {\n\treverse_proxy 127.0.0.1:%d\n}\n\n", site.Domain, site.Port)
	}

	tmp, err := os.CreateTemp(filepath.Dir(m.configFile), ".Caddyfile-*")
	if err != nil { return err }
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.WriteString(b.String()); err != nil { _ = tmp.Close(); return err }
	if err := tmp.Close(); err != nil { return err }

	if out, err := exec.CommandContext(ctx, "caddy", "validate", "--config", tmpName, "--adapter", "caddyfile").CombinedOutput(); err != nil {
		return fmt.Errorf("caddy validate: %w: %s", err, strings.TrimSpace(string(out)))
	}

	if err := os.Rename(tmpName, m.configFile); err != nil { return err }
	if err := os.Chmod(m.configFile, 0644); err != nil { return err }

	if out, err := exec.CommandContext(ctx, "systemctl", "reload", "caddy.service").CombinedOutput(); err != nil {
		return fmt.Errorf("reload caddy: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return m.save(sites)
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
