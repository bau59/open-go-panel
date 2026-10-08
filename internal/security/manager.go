package security

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const allowlistName = "open-go-panel"

type Status struct {
	Installed       bool
	EngineActive    bool
	BouncerActive   bool
	Version         string
	Installing      bool
	InstallError    string
	FirewallActive  bool
	FirewallStatus  string
	WebProtection   bool
}

type Decision struct {
	ID         string
	Source     string
	Scope      string
	Value      string
	Reason     string
	Action     string
	Country    string
	AS         string
	Expiration string
	AlertID    string
}

type Alert struct {
	ID        string
	Value     string
	Scope     string
	Reason    string
	Country   string
	AS        string
	Decisions string
	CreatedAt string
	Kind      string
}

type AllowEntry struct {
	Value      string
	Comment    string
	Expiration string
	CreatedAt  string
}

type Allowlist struct {
	Name        string
	Description string
	Entries     []AllowEntry
}


type Manager struct {
	mu           sync.Mutex
	installing   bool
	installError string
}

func New() *Manager { return &Manager{} }

func (m *Manager) Status(ctx context.Context) Status {
	s := Status{}
	if _, err := exec.LookPath("cscli"); err == nil {
		s.Installed = true
		if out, err := exec.CommandContext(ctx, "cscli", "version").CombinedOutput(); err == nil {
			s.Version = strings.TrimSpace(string(out))
		}
	}
	s.EngineActive = serviceActive(ctx, "crowdsec.service")
	s.BouncerActive = serviceActive(ctx, "crowdsec-firewall-bouncer.service")
	s.FirewallStatus = firewallStatus(ctx)
	s.FirewallActive = strings.Contains(strings.ToLower(s.FirewallStatus), "status: active")
	_, acquisErr := os.Stat("/etc/crowdsec/acquis.d/open-go-panel-caddy.yaml")
	s.WebProtection = s.EngineActive && acquisErr == nil

	m.mu.Lock()
	s.Installing = m.installing
	s.InstallError = m.installError
	m.mu.Unlock()

	return s
}

func (m *Manager) StartInstall(trustedIP string) error {
	trustedIP = strings.TrimSpace(trustedIP)
	if trustedIP != "" && net.ParseIP(trustedIP) == nil {
		return errors.New("invalid trusted IP")
	}

	m.mu.Lock()
	if m.installing {
		m.mu.Unlock()
		return errors.New("installation already running")
	}
	m.installing = true
	m.installError = ""
	m.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		err := install(ctx, trustedIP)

		m.mu.Lock()
		m.installing = false
		if err != nil {
			m.installError = err.Error()
		}
		m.mu.Unlock()
	}()

	return nil
}

func install(ctx context.Context, trustedIP string) error {
	tmp, err := os.CreateTemp("", "crowdsec-install-*.sh")
	if err != nil {
		return fmt.Errorf("create installer temp file: %w", err)
	}
	path := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(path)

	if err := run(ctx, "curl", "-fsSL", "https://install.crowdsec.net", "-o", path); err != nil {
		return err
	}
	if err := run(ctx, "sh", path); err != nil {
		return err
	}
	if err := run(ctx, "apt-get", "update"); err != nil {
		return err
	}
	if err := run(ctx, "apt-get", "install", "-y", "crowdsec"); err != nil {
		return err
	}
	if err := run(ctx, "systemctl", "enable", "--now", "crowdsec.service"); err != nil {
		return err
	}

	if err := enableWebProtection(ctx); err != nil {
		return err
	}

	if err := ensureAllowlist(ctx); err != nil {
		return err
	}
	for _, value := range []string{"127.0.0.1", "::1", trustedIP} {
		if value == "" {
			continue
		}
		if err := addAllowlist(ctx, value, "Open Go Panel trusted address"); err != nil {
			if !strings.Contains(strings.ToLower(err.Error()), "already") {
				return err
			}
		}
	}

	if err := run(ctx, "apt-get", "install", "-y", "crowdsec-firewall-bouncer-nftables"); err != nil {
		return err
	}
	return run(ctx, "systemctl", "enable", "--now", "crowdsec-firewall-bouncer.service")
}


func (m *Manager) EnableWebProtection(ctx context.Context) error {
	return enableWebProtection(ctx)
}

func enableWebProtection(ctx context.Context) error {
	if _, err := exec.LookPath("cscli"); err != nil {
		return errors.New("CrowdSec is not installed")
	}
	if err := run(ctx, "cscli", "collections", "install", "crowdsecurity/caddy"); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "already") {
			return err
		}
	}
	if err := os.MkdirAll("/etc/crowdsec/acquis.d", 0755); err != nil {
		return fmt.Errorf("create CrowdSec acquisition directory: %w", err)
	}
	caddyAcquisition := `source: journalctl
journalctl_filter:
  - "_SYSTEMD_UNIT=caddy.service"
labels:
  type: caddy
`
	if err := os.WriteFile("/etc/crowdsec/acquis.d/open-go-panel-caddy.yaml", []byte(caddyAcquisition), 0644); err != nil {
		return fmt.Errorf("write Caddy CrowdSec acquisition: %w", err)
	}
	if err := run(ctx, "crowdsec", "-t"); err != nil {
		return err
	}
	return run(ctx, "systemctl", "restart", "crowdsec.service")
}

func (m *Manager) StartProtection(ctx context.Context) error {
	if _, err := exec.LookPath("cscli"); err != nil {
		return errors.New("CrowdSec is not installed")
	}
	if err := run(ctx, "systemctl", "enable", "--now", "crowdsec.service"); err != nil {
		return err
	}
	if _, err := exec.LookPath("crowdsec-firewall-bouncer"); err == nil {
		return run(ctx, "systemctl", "enable", "--now", "crowdsec-firewall-bouncer.service")
	}
	if _, err := os.Stat("/usr/bin/crowdsec-firewall-bouncer"); err == nil {
		return run(ctx, "systemctl", "enable", "--now", "crowdsec-firewall-bouncer.service")
	}
	return errors.New("CrowdSec firewall bouncer is not installed")
}

func (m *Manager) ControlService(ctx context.Context, component, action string) error {
	var service string
	switch strings.TrimSpace(component) {
	case "engine":
		service = "crowdsec.service"
	case "bouncer":
		service = "crowdsec-firewall-bouncer.service"
	default:
		return errors.New("unsupported security component")
	}
	switch strings.TrimSpace(action) {
	case "start":
		return run(ctx, "systemctl", "enable", "--now", service)
	case "stop":
		return run(ctx, "systemctl", "disable", "--now", service)
	case "restart":
		return run(ctx, "systemctl", "restart", service)
	default:
		return errors.New("unsupported service action")
	}
}

func (m *Manager) DisableWebProtection(ctx context.Context) error {
	path := "/etc/crowdsec/acquis.d/open-go-panel-caddy.yaml"
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("disable Caddy acquisition: %w", err)
	}
	if serviceActive(ctx, "crowdsec.service") {
		if err := run(ctx, "crowdsec", "-t"); err != nil {
			return err
		}
		return run(ctx, "systemctl", "restart", "crowdsec.service")
	}
	return nil
}

func (m *Manager) DisableFirewall(ctx context.Context) error {
	if _, err := exec.LookPath("ufw"); err != nil {
		return errors.New("UFW is not installed")
	}
	return run(ctx, "ufw", "--force", "disable")
}

func (m *Manager) EnableFirewall(ctx context.Context) error {
	if err := run(ctx, "apt-get", "install", "-y", "ufw"); err != nil {
		return err
	}

	ports := map[int]struct{}{80: {}, 443: {}}
	if p := panelPublicPort(); p > 0 {
		ports[p] = struct{}{}
	}
	for _, p := range sshPorts(ctx) {
		ports[p] = struct{}{}
	}

	if err := run(ctx, "ufw", "default", "deny", "incoming"); err != nil {
		return err
	}
	if err := run(ctx, "ufw", "default", "allow", "outgoing"); err != nil {
		return err
	}

	for port := range ports {
		if err := run(ctx, "ufw", "allow", strconv.Itoa(port)+"/tcp"); err != nil {
			return err
		}
	}

	return run(ctx, "ufw", "--force", "enable")
}

func (m *Manager) FirewallRules(ctx context.Context) string {
	return firewallStatus(ctx)
}

func firewallStatus(ctx context.Context) string {
	if _, err := exec.LookPath("ufw"); err != nil {
		return "UFW is not installed."
	}
	out, err := exec.CommandContext(ctx, "ufw", "status", "numbered").CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil && text == "" {
		return err.Error()
	}
	return text
}

func panelPublicPort() int {
	addr := strings.TrimSpace(os.Getenv("OGP_LISTEN_ADDR"))
	if addr == "" {
		return 0
	}
	if strings.HasPrefix(addr, ":") {
		p, _ := strconv.Atoi(strings.TrimPrefix(addr, ":"))
		return p
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return 0
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return 0
	}
	p, _ := strconv.Atoi(port)
	return p
}

func sshPorts(ctx context.Context) []int {
	out, err := exec.CommandContext(ctx, "sshd", "-T").CombinedOutput()
	if err != nil {
		return []int{22}
	}
	var ports []int
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "port" {
			if p, err := strconv.Atoi(fields[1]); err == nil && p > 0 && p <= 65535 {
				ports = append(ports, p)
			}
		}
	}
	if len(ports) == 0 {
		return []int{22}
	}
	return ports
}

func (m *Manager) DecisionsData(ctx context.Context) ([]Decision, error) {
	raw, err := cliJSON(ctx, "decisions", "list")
	if err != nil {
		return nil, err
	}
	var list []any
	switch v := raw.(type) {
	case []any:
		list = v
	case map[string]any:
		if items, ok := firstArray(v, "decisions", "items"); ok {
			list = items
		}
	}
	var out []Decision
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, Decision{
			ID:         valueString(m, "id"),
			Source:     valueString(m, "origin", "source"),
			Scope:      valueString(m, "scope"),
			Value:      valueString(m, "value"),
			Reason:     valueString(m, "scenario", "reason"),
			Action:     valueString(m, "type", "action"),
			Country:    valueString(m, "country"),
			AS:         valueString(m, "as", "as_name", "asname"),
			Expiration: firstNonEmpty(valueString(m, "duration"), valueString(m, "expiration"), valueString(m, "until")),
			AlertID:    valueString(m, "alert_id", "alertid"),
		})
	}
	return out, nil
}

func (m *Manager) AlertsData(ctx context.Context) ([]Alert, error) {
	return m.AlertsDataSince(ctx, "24h")
}

func (m *Manager) AlertsDataSince(ctx context.Context, since string) ([]Alert, error) {
	switch since {
	case "24h", "7d", "30d":
	default:
		since = "24h"
	}
	raw, err := cliJSON(ctx, "alerts", "list", "--since", since)
	if err != nil {
		return nil, err
	}
	var list []any
	switch v := raw.(type) {
	case []any:
		list = v
	case map[string]any:
		if items, ok := firstArray(v, "alerts", "items"); ok {
			list = items
		}
	}
	var out []Alert
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		source, _ := mapValue(m, "source")
		value := valueString(m, "value")
		scope := valueString(m, "scope")
		country := valueString(m, "country")
		asName := valueString(m, "as", "as_name", "asname")
		if source != nil {
			if value == "" { value = valueString(source, "value") }
			if scope == "" { scope = valueString(source, "scope") }
			if country == "" { country = valueString(source, "country") }
			if asName == "" { asName = firstNonEmpty(valueString(source, "as_name", "asname"), valueString(source, "as_number")) }
		}
		decisionText := ""
		if decisions, ok := firstArray(m, "decisions"); ok {
			var values []string
			for _, rawDecision := range decisions {
				if dm, ok := rawDecision.(map[string]any); ok {
					t := valueString(dm, "type")
					if t == "" { t = valueString(dm, "action") }
					if t != "" { values = append(values, t) }
				}
			}
			if len(values) > 0 {
				decisionText = strings.Join(values, ", ")
			} else {
				decisionText = strconv.Itoa(len(decisions))
			}
		}
		out = append(out, Alert{
			ID:        valueString(m, "id"),
			Value:     value,
			Scope:     scope,
			Reason:    valueString(m, "scenario", "reason"),
			Country:   country,
			AS:        asName,
			Decisions: decisionText,
			CreatedAt: valueString(m, "created_at", "createdat"),
			Kind:      valueString(m, "kind"),
		})
	}
	return out, nil
}

func isMissingPanelAllowlist(err error) bool {
	if err == nil { return false }
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "allowlist 'open-go-panel' not found") ||
		strings.Contains(text, "allowlist \"open-go-panel\" not found")
}

func (m *Manager) AllowlistData(ctx context.Context) (Allowlist, error) {
	raw, err := cliJSON(ctx, "allowlists", "inspect", allowlistName)
	if err != nil {
		// A fresh CrowdSec installation has no panel-owned allowlist yet.
		// Only the explicit not-found response is an empty state; preserve
		// errors caused by permissions, connectivity, or broken cscli.
		if isMissingPanelAllowlist(err) {
			return Allowlist{Name: allowlistName}, nil
		}
		return Allowlist{}, err
	}
	root, ok := raw.(map[string]any)
	if !ok {
		return Allowlist{Name: allowlistName}, nil
	}
	result := Allowlist{
		Name:        firstNonEmpty(valueString(root, "name"), allowlistName),
		Description: valueString(root, "description"),
	}
	items, _ := firstArray(root, "items", "values", "entries")
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		result.Entries = append(result.Entries, AllowEntry{
			Value:      valueString(m, "value"),
			Comment:    valueString(m, "comment"),
			Expiration: valueString(m, "expiration", "expires_at"),
			CreatedAt:  valueString(m, "created_at", "createdat"),
		})
	}
	return result, nil
}

func cliJSON(ctx context.Context, args ...string) (any, error) {
	if _, err := exec.LookPath("cscli"); err != nil {
		return nil, errors.New("CrowdSec is not installed")
	}
	args = append(args, "--output", "json")
	out, err := exec.CommandContext(ctx, "cscli", args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("cscli %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	var value any
	if err := json.Unmarshal(out, &value); err != nil {
		return nil, fmt.Errorf("decode cscli JSON: %w", err)
	}
	return value, nil
}

func normalizedKey(v string) string {
	v = strings.ToLower(v)
	v = strings.ReplaceAll(v, "_", "")
	v = strings.ReplaceAll(v, "-", "")
	return v
}

func valueString(m map[string]any, names ...string) string {
	for key, value := range m {
		nk := normalizedKey(key)
		for _, name := range names {
			if nk != normalizedKey(name) {
				continue
			}
			switch v := value.(type) {
			case string:
				return v
			case float64:
				if v == float64(int64(v)) {
					return strconv.FormatInt(int64(v), 10)
				}
				return strconv.FormatFloat(v, 'f', -1, 64)
			case bool:
				return strconv.FormatBool(v)
			case nil:
				return ""
			default:
				data, _ := json.Marshal(v)
				return string(data)
			}
		}
	}
	return ""
}

func mapValue(m map[string]any, name string) (map[string]any, bool) {
	for key, value := range m {
		if normalizedKey(key) == normalizedKey(name) {
			v, ok := value.(map[string]any)
			return v, ok
		}
	}
	return nil, false
}

func firstArray(m map[string]any, names ...string) ([]any, bool) {
	for key, value := range m {
		nk := normalizedKey(key)
		for _, name := range names {
			if nk == normalizedKey(name) {
				v, ok := value.([]any)
				return v, ok
			}
		}
	}
	return nil, false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (m *Manager) Decisions(ctx context.Context) string {
	return cliText(ctx, "decisions", "list", "--color", "no")
}

func (m *Manager) Alerts(ctx context.Context) string {
	return cliText(ctx, "alerts", "list", "--since", "24h", "--color", "no")
}

func (m *Manager) Allowlist(ctx context.Context) string {
	return cliText(ctx, "allowlists", "inspect", allowlistName, "--color", "no")
}

func (m *Manager) Unban(ctx context.Context, ip string) error {
	ip = strings.TrimSpace(ip)
	if net.ParseIP(ip) == nil {
		return errors.New("invalid IP address")
	}
	return run(ctx, "cscli", "decisions", "delete", "--ip", ip)
}

func (m *Manager) Trust(ctx context.Context, value, comment string) error {
	value = strings.TrimSpace(value)
	if !validIPOrCIDR(value) {
		return errors.New("trusted value must be an IP address or CIDR")
	}
	if err := ensureAllowlist(ctx); err != nil {
		return err
	}
	return addAllowlist(ctx, value, strings.TrimSpace(comment))
}

func (m *Manager) Untrust(ctx context.Context, value string) error {
	value = strings.TrimSpace(value)
	if !validIPOrCIDR(value) {
		return errors.New("trusted value must be an IP address or CIDR")
	}
	return run(ctx, "cscli", "allowlists", "remove", allowlistName, value)
}

func ensureAllowlist(ctx context.Context) error {
	if exec.CommandContext(ctx, "cscli", "allowlists", "inspect", allowlistName).Run() == nil {
		return nil
	}
	out, err := exec.CommandContext(
		ctx,
		"cscli", "allowlists", "create", allowlistName,
		"--description", "Trusted IPs managed by Open Go Panel",
	).CombinedOutput()
	if err != nil && !strings.Contains(strings.ToLower(string(out)), "already") {
		return fmt.Errorf("create CrowdSec allowlist: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func addAllowlist(ctx context.Context, value, comment string) error {
	args := []string{"allowlists", "add", allowlistName, value}
	if comment != "" {
		args = append(args, "--comment", comment)
	}
	return run(ctx, "cscli", args...)
}

func validIPOrCIDR(value string) bool {
	if net.ParseIP(value) != nil {
		return true
	}
	_, _, err := net.ParseCIDR(value)
	return err == nil
}

func cliText(ctx context.Context, args ...string) string {
	if _, err := exec.LookPath("cscli"); err != nil {
		return "CrowdSec is not installed."
	}
	out, err := exec.CommandContext(ctx, "cscli", args...).CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil && text == "" {
		return err.Error()
	}
	if text == "" {
		return "No entries."
	}
	return text
}

func serviceActive(ctx context.Context, service string) bool {
	return exec.CommandContext(ctx, "systemctl", "is-active", "--quiet", service).Run() == nil
}

func run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
