package security

import (
	"context"
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


func (m *Manager) EnableFirewall(ctx context.Context) error {
	if err := run(ctx, "apt-get", "install", "-y", "ufw"); err != nil {
		return err
	}

	ports := map[int]struct{}{80: {}, 443: {}}
	if p := panelPort(); p > 0 {
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

func panelPort() int {
	addr := strings.TrimSpace(os.Getenv("OGP_LISTEN_ADDR"))
	if addr == "" {
		return 8443
	}
	if strings.HasPrefix(addr, ":") {
		p, _ := strconv.Atoi(strings.TrimPrefix(addr, ":"))
		return p
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 8443
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
