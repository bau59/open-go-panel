package adminer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	listenAddr = "127.0.0.1:8787"
	rootDir    = "/var/lib/open-go-panel/adminer"
	indexFile  = "/var/lib/open-go-panel/adminer/index.php"
	serviceFile = "/etc/systemd/system/open-go-panel-adminer.service"
)

type Status struct {
	Installed bool
	Active    bool
	Listen    string
}

type Manager struct{}

func New() *Manager { return &Manager{} }

func (m *Manager) Status(ctx context.Context) Status {
	_, phpErr := exec.LookPath("php")
	_, fileErr := os.Stat(indexFile)
	return Status{
		Installed: phpErr == nil && fileErr == nil,
		Active: exec.CommandContext(ctx, "systemctl", "is-active", "--quiet", "open-go-panel-adminer.service").Run() == nil,
		Listen: listenAddr,
	}
}

func (m *Manager) Install(ctx context.Context) error {
	if err := run(ctx, "apt-get", "update"); err != nil {
		return err
	}
	if err := run(ctx, "apt-get", "install", "-y", "php-cli", "php-mysql", "php-pgsql", "curl"); err != nil {
		return err
	}
	if err := os.MkdirAll(rootDir, 0755); err != nil {
		return fmt.Errorf("create Adminer directory: %w", err)
	}

	tmp, err := os.CreateTemp(rootDir, ".adminer-*.php")
	if err != nil {
		return fmt.Errorf("create Adminer temp file: %w", err)
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(tmpPath)

	if err := run(ctx, "curl", "-fsSL", "https://www.adminer.org/latest.php", "-o", tmpPath); err != nil {
		return err
	}
	if info, err := os.Stat(tmpPath); err != nil || info.Size() < 100000 {
		if err != nil {
			return fmt.Errorf("verify Adminer download: %w", err)
		}
		return errors.New("downloaded Adminer file is unexpectedly small")
	}
	if err := os.Chmod(tmpPath, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, indexFile); err != nil {
		return fmt.Errorf("install Adminer file: %w", err)
	}

	unit := `[Unit]
Description=Open Go Panel Adminer
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=www-data
Group=www-data
WorkingDirectory=/var/lib/open-go-panel/adminer
ExecStart=/usr/bin/php -S 127.0.0.1:8787 -t /var/lib/open-go-panel/adminer
Restart=on-failure
RestartSec=3
PrivateTmp=true
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/php/sessions

[Install]
WantedBy=multi-user.target
`
	if err := os.WriteFile(serviceFile, []byte(unit), 0644); err != nil {
		return fmt.Errorf("write Adminer service: %w", err)
	}

	if err := run(ctx, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	if err := run(ctx, "systemctl", "enable", "--now", "open-go-panel-adminer.service"); err != nil {
		return err
	}
	return nil
}

func (m *Manager) Start(ctx context.Context) error {
	return run(ctx, "systemctl", "start", "open-go-panel-adminer.service")
}

func (m *Manager) Stop(ctx context.Context) error {
	return run(ctx, "systemctl", "stop", "open-go-panel-adminer.service")
}

func (m *Manager) Update(ctx context.Context) error {
	if _, err := os.Stat(indexFile); err != nil {
		return m.Install(ctx)
	}
	tmp, err := os.CreateTemp(rootDir, ".adminer-*.php")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(tmpPath)

	if err := run(ctx, "curl", "-fsSL", "https://www.adminer.org/latest.php", "-o", tmpPath); err != nil {
		return err
	}
	if info, err := os.Stat(tmpPath); err != nil || info.Size() < 100000 {
		if err != nil {
			return err
		}
		return errors.New("downloaded Adminer file is unexpectedly small")
	}
	if err := os.Chmod(tmpPath, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, indexFile); err != nil {
		return err
	}
	return run(ctx, "systemctl", "restart", "open-go-panel-adminer.service")
}

func (m *Manager) URL() string {
	return "http://" + listenAddr
}

func run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", filepath.Base(name), strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
