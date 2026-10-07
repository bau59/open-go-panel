package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bau59/open-go-panel/internal/linuxuser"
)

const (
	minPort = 8100
	maxPort = 8999
)

var appNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

type ServiceConfig struct {
	Mode        string `json:"mode,omitempty"`
	RunMode     string `json:"run_mode,omitempty"`
	Command     string `json:"command,omitempty"`
	Restart     string `json:"restart,omitempty"`
	RestartSec  int    `json:"restart_sec,omitempty"`
	CPUQuota    string `json:"cpu_quota,omitempty"`
	MemoryMax   string `json:"memory_max,omitempty"`
	Environment string `json:"environment,omitempty"`
	RawUnit     string `json:"raw_unit,omitempty"`
}

type App struct {
	ID        int64         `json:"id"`
	User      string        `json:"user"`
	Name      string        `json:"name"`
	Type      string        `json:"type"`
	Root      string        `json:"root"`
	Port      int           `json:"port,omitempty"`
	Command   string        `json:"command,omitempty"`
	Service   ServiceConfig `json:"service,omitempty"`
	CreatedAt time.Time     `json:"created_at"`
}

type Manager struct {
	mu          sync.Mutex
	stateFile   string
	serviceDir  string
	runnerDir   string
	envDir      string
	users       *linuxuser.Manager
}

func New(stateFile string, users *linuxuser.Manager) *Manager {
	return &Manager{
		stateFile:  stateFile,
		serviceDir: "/etc/systemd/system",
		runnerDir:  "/var/lib/open-go-panel/runners",
		envDir:     "/var/lib/open-go-panel/env",
		users:      users,
	}
}

func (m *Manager) List() ([]App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	apps, err := m.load()
	if err != nil {
		return nil, err
	}

	sort.Slice(apps, func(i, j int) bool {
		return apps[i].ID < apps[j].ID
	})

	return apps, nil
}

func (m *Manager) Create(username, name, appType string) (App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !appNamePattern.MatchString(name) {
		return App{}, errors.New("app name must contain only lowercase letters, digits, underscore or hyphen")
	}

	switch appType {
	case "go", "node", "static", "worker":
	default:
		return App{}, errors.New("unsupported app type")
	}

	managed, err := m.userManaged(username)
	if err != nil {
		return App{}, err
	}
	if !managed {
		return App{}, fmt.Errorf("user %q is not managed by Open Go Panel", username)
	}

	account, err := user.Lookup(username)
	if err != nil {
		return App{}, fmt.Errorf("lookup user %q: %w", username, err)
	}

	apps, err := m.load()
	if err != nil {
		return App{}, err
	}

	for _, existing := range apps {
		if existing.User == username && existing.Name == name {
			return App{}, fmt.Errorf("app %q already exists for user %q", name, username)
		}
	}

	root := filepath.Join(account.HomeDir, "apps", name)
	if err := os.MkdirAll(root, 0750); err != nil {
		return App{}, fmt.Errorf("create app directory: %w", err)
	}

	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return App{}, fmt.Errorf("parse uid: %w", err)
	}
	gid, err := strconv.Atoi(account.Gid)
	if err != nil {
		return App{}, fmt.Errorf("parse gid: %w", err)
	}

	if err := os.Chown(filepath.Join(account.HomeDir, "apps"), uid, gid); err != nil {
		return App{}, fmt.Errorf("chown apps directory: %w", err)
	}
	if err := os.Chown(root, uid, gid); err != nil {
		return App{}, fmt.Errorf("chown app directory: %w", err)
	}

	app := App{
		ID:        nextID(apps),
		User:      username,
		Name:      name,
		Type:      appType,
		Root:      root,
		CreatedAt: time.Now().UTC(),
	}

	if appType == "go" || appType == "node" {
		port, err := nextPort(apps)
		if err != nil {
			return App{}, err
		}
		app.Port = port
	}

	apps = append(apps, app)

	if err := m.save(apps); err != nil {
		return App{}, err
	}

	return app, nil
}

func (m *Manager) Get(id int64) (App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	apps, err := m.load()
	if err != nil {
		return App{}, err
	}

	for _, app := range apps {
		if app.ID == id {
			return app, nil
		}
	}

	return App{}, fmt.Errorf("app %d not found", id)
}

func (m *Manager) SetCommand(ctx context.Context, id int64, command string) error {
	return m.SetServiceConfig(ctx, id, ServiceConfig{
		Mode:       "form",
		RunMode:    "custom",
		Command:    command,
		Restart:    "on-failure",
		RestartSec: 3,
	})
}

func (m *Manager) SetServiceConfig(ctx context.Context, id int64, cfg ServiceConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	apps, err := m.load()
	if err != nil {
		return err
	}

	index := -1
	for i := range apps {
		if apps[i].ID == id {
			index = i
			break
		}
	}
	if index == -1 {
		return fmt.Errorf("app %d not found", id)
	}
	if apps[index].Type == "static" {
		return errors.New("static apps do not use systemd services")
	}

	cfg.Mode = strings.TrimSpace(cfg.Mode)
	if cfg.Mode == "" {
		cfg.Mode = "form"
	}

	if cfg.Mode == "raw" {
		cfg.RawUnit = strings.TrimSpace(cfg.RawUnit)
		if cfg.RawUnit == "" {
			return errors.New("raw unit is required")
		}
		apps[index].Service = cfg
		if err := m.writeRawUnit(ctx, apps[index]); err != nil {
			return err
		}
	} else {
		if err := validateServiceConfig(apps[index].Type, &cfg); err != nil {
			return err
		}
		apps[index].Service = cfg
		apps[index].Command = cfg.Command

		if err := m.writeEnvironment(apps[index]); err != nil {
			return err
		}
		if err := m.writeRunner(apps[index]); err != nil {
			return err
		}
		if err := m.writeUnit(apps[index]); err != nil {
			return err
		}
	}

	if err := systemctl(ctx, "daemon-reload"); err != nil {
		return err
	}
	if err := systemctl(ctx, "enable", serviceName(id)); err != nil {
		return err
	}

	return m.save(apps)
}

func (m *Manager) Logs(ctx context.Context, id int64, lines int) (string, error) {
	if lines <= 0 || lines > 1000 {
		lines = 200
	}
	if _, err := m.Get(id); err != nil {
		return "", err
	}

	cmd := exec.CommandContext(ctx, "journalctl", "-u", serviceName(id), "-n", strconv.Itoa(lines), "--no-pager", "-o", "short-iso")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("read app logs: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (m *Manager) Start(ctx context.Context, id int64) error {
	app, err := m.Get(id)
	if err != nil {
		return err
	}
	if err := validateRunnable(app); err != nil {
		return err
	}
	return systemctl(ctx, "start", serviceName(id))
}

func (m *Manager) Stop(ctx context.Context, id int64) error {
	app, err := m.Get(id)
	if err != nil {
		return err
	}
	if err := validateRunnable(app); err != nil {
		return err
	}
	return systemctl(ctx, "stop", serviceName(id))
}

func (m *Manager) Restart(ctx context.Context, id int64) error {
	app, err := m.Get(id)
	if err != nil {
		return err
	}
	if err := validateRunnable(app); err != nil {
		return err
	}
	return systemctl(ctx, "restart", serviceName(id))
}

func (m *Manager) Status(ctx context.Context, id int64) string {
	app, err := m.Get(id)
	if err != nil || app.Type == "static" || app.Command == "" {
		return "not configured"
	}

	cmd := exec.CommandContext(ctx, "systemctl", "is-active", serviceName(id))
	out, err := cmd.Output()
	status := strings.TrimSpace(string(out))
	if err != nil {
		if status != "" {
			return status
		}
		return "inactive"
	}

	return status
}

func (m *Manager) writeRunner(app App) error {
	if err := os.MkdirAll(m.runnerDir, 0755); err != nil {
		return fmt.Errorf("create runner directory: %w", err)
	}

	path := runnerPath(m.runnerDir, app.ID)
	command := runnerCommand(app)
	content := "#!/usr/bin/env bash\nset -e\n" + command + "\n"
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		return fmt.Errorf("write app runner: %w", err)
	}
	if err := os.Chmod(path, 0755); err != nil {
		return fmt.Errorf("chmod app runner: %w", err)
	}

	return nil
}

func (m *Manager) writeUnit(app App) error {
	var b strings.Builder
	fmt.Fprintf(&b, "[Unit]\nDescription=Open Go Panel app %d (%s/%s)\nAfter=network-online.target\nWants=network-online.target\n\n", app.ID, app.User, app.Name)
	fmt.Fprintf(&b, "[Service]\nType=simple\nUser=%s\nWorkingDirectory=%s\n", app.User, app.Root)
	if app.Port > 0 {
		fmt.Fprintf(&b, "Environment=PORT=%d\n", app.Port)
	}
	fmt.Fprintf(&b, "EnvironmentFile=-%s\n", envPath(m.envDir, app.ID))
	fmt.Fprintf(&b, "ExecStart=%s\n", runnerPath(m.runnerDir, app.ID))
	restart := app.Service.Restart
	if restart == "" {
		restart = "on-failure"
	}
	restartSec := app.Service.RestartSec
	if restartSec < 0 {
		restartSec = 0
	}
	fmt.Fprintf(&b, "Restart=%s\nRestartSec=%d\n", restart, restartSec)
	if app.Service.CPUQuota != "" {
		fmt.Fprintf(&b, "CPUQuota=%s\n", app.Service.CPUQuota)
	}
	if app.Service.MemoryMax != "" {
		fmt.Fprintf(&b, "MemoryMax=%s\n", app.Service.MemoryMax)
	}
	fmt.Fprintf(&b, "\n[Install]\nWantedBy=multi-user.target\n")

	path := filepath.Join(m.serviceDir, serviceName(app.ID))
	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		return fmt.Errorf("write systemd unit: %w", err)
	}

	return nil
}

func validateServiceConfig(appType string, cfg *ServiceConfig) error {
	if cfg.Mode != "form" {
		return errors.New("unsupported service mode")
	}

	switch cfg.Restart {
	case "", "no", "on-failure", "always":
	default:
		return errors.New("unsupported restart policy")
	}
	if cfg.Restart == "" {
		cfg.Restart = "on-failure"
	}
	if cfg.RestartSec < 0 || cfg.RestartSec > 300 {
		return errors.New("restart delay must be between 0 and 300 seconds")
	}

	switch cfg.RunMode {
	case "":
		cfg.RunMode = "custom"
	case "custom", "go-air", "go-build", "node-npm":
	default:
		return errors.New("unsupported run mode")
	}

	if cfg.RunMode == "go-air" || cfg.RunMode == "go-build" {
		if appType != "go" {
			return errors.New("selected run mode is only available for Go apps")
		}
	}
	if cfg.RunMode == "node-npm" && appType != "node" {
		return errors.New("node npm mode is only available for Node.js apps")
	}
	if cfg.RunMode == "custom" && strings.TrimSpace(cfg.Command) == "" {
		return errors.New("custom command is required")
	}

	if cfg.CPUQuota != "" {
		ok, _ := regexp.MatchString(`^[0-9]{1,4}%package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bau59/open-go-panel/internal/linuxuser"
)

const (
	minPort = 8100
	maxPort = 8999
)

var appNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

type ServiceConfig struct {
	Mode        string `json:"mode,omitempty"`
	RunMode     string `json:"run_mode,omitempty"`
	Command     string `json:"command,omitempty"`
	Restart     string `json:"restart,omitempty"`
	RestartSec  int    `json:"restart_sec,omitempty"`
	CPUQuota    string `json:"cpu_quota,omitempty"`
	MemoryMax   string `json:"memory_max,omitempty"`
	Environment string `json:"environment,omitempty"`
	RawUnit     string `json:"raw_unit,omitempty"`
}

type App struct {
	ID        int64         `json:"id"`
	User      string        `json:"user"`
	Name      string        `json:"name"`
	Type      string        `json:"type"`
	Root      string        `json:"root"`
	Port      int           `json:"port,omitempty"`
	Command   string        `json:"command,omitempty"`
	Service   ServiceConfig `json:"service,omitempty"`
	CreatedAt time.Time     `json:"created_at"`
}

type Manager struct {
	mu          sync.Mutex
	stateFile   string
	serviceDir  string
	runnerDir   string
	envDir      string
	users       *linuxuser.Manager
}

func New(stateFile string, users *linuxuser.Manager) *Manager {
	return &Manager{
		stateFile:  stateFile,
		serviceDir: "/etc/systemd/system",
		runnerDir:  "/var/lib/open-go-panel/runners",
		envDir:     "/var/lib/open-go-panel/env",
		users:      users,
	}
}

func (m *Manager) List() ([]App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	apps, err := m.load()
	if err != nil {
		return nil, err
	}

	sort.Slice(apps, func(i, j int) bool {
		return apps[i].ID < apps[j].ID
	})

	return apps, nil
}

func (m *Manager) Create(username, name, appType string) (App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !appNamePattern.MatchString(name) {
		return App{}, errors.New("app name must contain only lowercase letters, digits, underscore or hyphen")
	}

	switch appType {
	case "go", "node", "static", "worker":
	default:
		return App{}, errors.New("unsupported app type")
	}

	managed, err := m.userManaged(username)
	if err != nil {
		return App{}, err
	}
	if !managed {
		return App{}, fmt.Errorf("user %q is not managed by Open Go Panel", username)
	}

	account, err := user.Lookup(username)
	if err != nil {
		return App{}, fmt.Errorf("lookup user %q: %w", username, err)
	}

	apps, err := m.load()
	if err != nil {
		return App{}, err
	}

	for _, existing := range apps {
		if existing.User == username && existing.Name == name {
			return App{}, fmt.Errorf("app %q already exists for user %q", name, username)
		}
	}

	root := filepath.Join(account.HomeDir, "apps", name)
	if err := os.MkdirAll(root, 0750); err != nil {
		return App{}, fmt.Errorf("create app directory: %w", err)
	}

	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return App{}, fmt.Errorf("parse uid: %w", err)
	}
	gid, err := strconv.Atoi(account.Gid)
	if err != nil {
		return App{}, fmt.Errorf("parse gid: %w", err)
	}

	if err := os.Chown(filepath.Join(account.HomeDir, "apps"), uid, gid); err != nil {
		return App{}, fmt.Errorf("chown apps directory: %w", err)
	}
	if err := os.Chown(root, uid, gid); err != nil {
		return App{}, fmt.Errorf("chown app directory: %w", err)
	}

	app := App{
		ID:        nextID(apps),
		User:      username,
		Name:      name,
		Type:      appType,
		Root:      root,
		CreatedAt: time.Now().UTC(),
	}

	if appType == "go" || appType == "node" {
		port, err := nextPort(apps)
		if err != nil {
			return App{}, err
		}
		app.Port = port
	}

	apps = append(apps, app)

	if err := m.save(apps); err != nil {
		return App{}, err
	}

	return app, nil
}

func (m *Manager) Get(id int64) (App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	apps, err := m.load()
	if err != nil {
		return App{}, err
	}

	for _, app := range apps {
		if app.ID == id {
			return app, nil
		}
	}

	return App{}, fmt.Errorf("app %d not found", id)
}

func (m *Manager) SetCommand(ctx context.Context, id int64, command string) error {
	return m.SetServiceConfig(ctx, id, ServiceConfig{
		Mode:       "form",
		RunMode:    "custom",
		Command:    command,
		Restart:    "on-failure",
		RestartSec: 3,
	})
}

func (m *Manager) SetServiceConfig(ctx context.Context, id int64, cfg ServiceConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	apps, err := m.load()
	if err != nil {
		return err
	}

	index := -1
	for i := range apps {
		if apps[i].ID == id {
			index = i
			break
		}
	}
	if index == -1 {
		return fmt.Errorf("app %d not found", id)
	}
	if apps[index].Type == "static" {
		return errors.New("static apps do not use systemd services")
	}

	cfg.Mode = strings.TrimSpace(cfg.Mode)
	if cfg.Mode == "" {
		cfg.Mode = "form"
	}

	if cfg.Mode == "raw" {
		cfg.RawUnit = strings.TrimSpace(cfg.RawUnit)
		if cfg.RawUnit == "" {
			return errors.New("raw unit is required")
		}
		apps[index].Service = cfg
		if err := m.writeRawUnit(ctx, apps[index]); err != nil {
			return err
		}
	} else {
		if err := validateServiceConfig(apps[index].Type, &cfg); err != nil {
			return err
		}
		apps[index].Service = cfg
		apps[index].Command = cfg.Command

		if err := m.writeEnvironment(apps[index]); err != nil {
			return err
		}
		if err := m.writeRunner(apps[index]); err != nil {
			return err
		}
		if err := m.writeUnit(apps[index]); err != nil {
			return err
		}
	}

	if err := systemctl(ctx, "daemon-reload"); err != nil {
		return err
	}
	if err := systemctl(ctx, "enable", serviceName(id)); err != nil {
		return err
	}

	return m.save(apps)
}

func (m *Manager) Logs(ctx context.Context, id int64, lines int) (string, error) {
	if lines <= 0 || lines > 1000 {
		lines = 200
	}
	if _, err := m.Get(id); err != nil {
		return "", err
	}

	cmd := exec.CommandContext(ctx, "journalctl", "-u", serviceName(id), "-n", strconv.Itoa(lines), "--no-pager", "-o", "short-iso")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("read app logs: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (m *Manager) Start(ctx context.Context, id int64) error {
	app, err := m.Get(id)
	if err != nil {
		return err
	}
	if err := validateRunnable(app); err != nil {
		return err
	}
	return systemctl(ctx, "start", serviceName(id))
}

func (m *Manager) Stop(ctx context.Context, id int64) error {
	app, err := m.Get(id)
	if err != nil {
		return err
	}
	if err := validateRunnable(app); err != nil {
		return err
	}
	return systemctl(ctx, "stop", serviceName(id))
}

func (m *Manager) Restart(ctx context.Context, id int64) error {
	app, err := m.Get(id)
	if err != nil {
		return err
	}
	if err := validateRunnable(app); err != nil {
		return err
	}
	return systemctl(ctx, "restart", serviceName(id))
}

func (m *Manager) Status(ctx context.Context, id int64) string {
	app, err := m.Get(id)
	if err != nil || app.Type == "static" || app.Command == "" {
		return "not configured"
	}

	cmd := exec.CommandContext(ctx, "systemctl", "is-active", serviceName(id))
	out, err := cmd.Output()
	status := strings.TrimSpace(string(out))
	if err != nil {
		if status != "" {
			return status
		}
		return "inactive"
	}

	return status
}

func (m *Manager) writeRunner(app App) error {
	if err := os.MkdirAll(m.runnerDir, 0755); err != nil {
		return fmt.Errorf("create runner directory: %w", err)
	}

	path := runnerPath(m.runnerDir, app.ID)
	command := runnerCommand(app)
	content := "#!/usr/bin/env bash\nset -e\n" + command + "\n"
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		return fmt.Errorf("write app runner: %w", err)
	}
	if err := os.Chmod(path, 0755); err != nil {
		return fmt.Errorf("chmod app runner: %w", err)
	}

	return nil
}

func (m *Manager) writeUnit(app App) error {
	var b strings.Builder
	fmt.Fprintf(&b, "[Unit]\nDescription=Open Go Panel app %d (%s/%s)\nAfter=network-online.target\nWants=network-online.target\n\n", app.ID, app.User, app.Name)
	fmt.Fprintf(&b, "[Service]\nType=simple\nUser=%s\nWorkingDirectory=%s\n", app.User, app.Root)
	if app.Port > 0 {
		fmt.Fprintf(&b, "Environment=PORT=%d\n", app.Port)
	}
	fmt.Fprintf(&b, "EnvironmentFile=-%s\n", envPath(m.envDir, app.ID))
	fmt.Fprintf(&b, "ExecStart=%s\n", runnerPath(m.runnerDir, app.ID))
	restart := app.Service.Restart
	if restart == "" {
		restart = "on-failure"
	}
	restartSec := app.Service.RestartSec
	if restartSec < 0 {
		restartSec = 0
	}
	fmt.Fprintf(&b, "Restart=%s\nRestartSec=%d\n", restart, restartSec)
	if app.Service.CPUQuota != "" {
		fmt.Fprintf(&b, "CPUQuota=%s\n", app.Service.CPUQuota)
	}
	if app.Service.MemoryMax != "" {
		fmt.Fprintf(&b, "MemoryMax=%s\n", app.Service.MemoryMax)
	}
	fmt.Fprintf(&b, "\n[Install]\nWantedBy=multi-user.target\n")

	path := filepath.Join(m.serviceDir, serviceName(app.ID))
	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		return fmt.Errorf("write systemd unit: %w", err)
	}

	return nil
}
, cfg.CPUQuota)
		if !ok {
			return errors.New("CPU quota must look like 100% or 200%")
		}
	}
	if cfg.MemoryMax != "" {
		ok, _ := regexp.MatchString(`^[0-9]+[KMGTP]package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bau59/open-go-panel/internal/linuxuser"
)

const (
	minPort = 8100
	maxPort = 8999
)

var appNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

type ServiceConfig struct {
	Mode        string `json:"mode,omitempty"`
	RunMode     string `json:"run_mode,omitempty"`
	Command     string `json:"command,omitempty"`
	Restart     string `json:"restart,omitempty"`
	RestartSec  int    `json:"restart_sec,omitempty"`
	CPUQuota    string `json:"cpu_quota,omitempty"`
	MemoryMax   string `json:"memory_max,omitempty"`
	Environment string `json:"environment,omitempty"`
	RawUnit     string `json:"raw_unit,omitempty"`
}

type App struct {
	ID        int64         `json:"id"`
	User      string        `json:"user"`
	Name      string        `json:"name"`
	Type      string        `json:"type"`
	Root      string        `json:"root"`
	Port      int           `json:"port,omitempty"`
	Command   string        `json:"command,omitempty"`
	Service   ServiceConfig `json:"service,omitempty"`
	CreatedAt time.Time     `json:"created_at"`
}

type Manager struct {
	mu          sync.Mutex
	stateFile   string
	serviceDir  string
	runnerDir   string
	envDir      string
	users       *linuxuser.Manager
}

func New(stateFile string, users *linuxuser.Manager) *Manager {
	return &Manager{
		stateFile:  stateFile,
		serviceDir: "/etc/systemd/system",
		runnerDir:  "/var/lib/open-go-panel/runners",
		envDir:     "/var/lib/open-go-panel/env",
		users:      users,
	}
}

func (m *Manager) List() ([]App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	apps, err := m.load()
	if err != nil {
		return nil, err
	}

	sort.Slice(apps, func(i, j int) bool {
		return apps[i].ID < apps[j].ID
	})

	return apps, nil
}

func (m *Manager) Create(username, name, appType string) (App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !appNamePattern.MatchString(name) {
		return App{}, errors.New("app name must contain only lowercase letters, digits, underscore or hyphen")
	}

	switch appType {
	case "go", "node", "static", "worker":
	default:
		return App{}, errors.New("unsupported app type")
	}

	managed, err := m.userManaged(username)
	if err != nil {
		return App{}, err
	}
	if !managed {
		return App{}, fmt.Errorf("user %q is not managed by Open Go Panel", username)
	}

	account, err := user.Lookup(username)
	if err != nil {
		return App{}, fmt.Errorf("lookup user %q: %w", username, err)
	}

	apps, err := m.load()
	if err != nil {
		return App{}, err
	}

	for _, existing := range apps {
		if existing.User == username && existing.Name == name {
			return App{}, fmt.Errorf("app %q already exists for user %q", name, username)
		}
	}

	root := filepath.Join(account.HomeDir, "apps", name)
	if err := os.MkdirAll(root, 0750); err != nil {
		return App{}, fmt.Errorf("create app directory: %w", err)
	}

	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return App{}, fmt.Errorf("parse uid: %w", err)
	}
	gid, err := strconv.Atoi(account.Gid)
	if err != nil {
		return App{}, fmt.Errorf("parse gid: %w", err)
	}

	if err := os.Chown(filepath.Join(account.HomeDir, "apps"), uid, gid); err != nil {
		return App{}, fmt.Errorf("chown apps directory: %w", err)
	}
	if err := os.Chown(root, uid, gid); err != nil {
		return App{}, fmt.Errorf("chown app directory: %w", err)
	}

	app := App{
		ID:        nextID(apps),
		User:      username,
		Name:      name,
		Type:      appType,
		Root:      root,
		CreatedAt: time.Now().UTC(),
	}

	if appType == "go" || appType == "node" {
		port, err := nextPort(apps)
		if err != nil {
			return App{}, err
		}
		app.Port = port
	}

	apps = append(apps, app)

	if err := m.save(apps); err != nil {
		return App{}, err
	}

	return app, nil
}

func (m *Manager) Get(id int64) (App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	apps, err := m.load()
	if err != nil {
		return App{}, err
	}

	for _, app := range apps {
		if app.ID == id {
			return app, nil
		}
	}

	return App{}, fmt.Errorf("app %d not found", id)
}

func (m *Manager) SetCommand(ctx context.Context, id int64, command string) error {
	return m.SetServiceConfig(ctx, id, ServiceConfig{
		Mode:       "form",
		RunMode:    "custom",
		Command:    command,
		Restart:    "on-failure",
		RestartSec: 3,
	})
}

func (m *Manager) SetServiceConfig(ctx context.Context, id int64, cfg ServiceConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	apps, err := m.load()
	if err != nil {
		return err
	}

	index := -1
	for i := range apps {
		if apps[i].ID == id {
			index = i
			break
		}
	}
	if index == -1 {
		return fmt.Errorf("app %d not found", id)
	}
	if apps[index].Type == "static" {
		return errors.New("static apps do not use systemd services")
	}

	cfg.Mode = strings.TrimSpace(cfg.Mode)
	if cfg.Mode == "" {
		cfg.Mode = "form"
	}

	if cfg.Mode == "raw" {
		cfg.RawUnit = strings.TrimSpace(cfg.RawUnit)
		if cfg.RawUnit == "" {
			return errors.New("raw unit is required")
		}
		apps[index].Service = cfg
		if err := m.writeRawUnit(ctx, apps[index]); err != nil {
			return err
		}
	} else {
		if err := validateServiceConfig(apps[index].Type, &cfg); err != nil {
			return err
		}
		apps[index].Service = cfg
		apps[index].Command = cfg.Command

		if err := m.writeEnvironment(apps[index]); err != nil {
			return err
		}
		if err := m.writeRunner(apps[index]); err != nil {
			return err
		}
		if err := m.writeUnit(apps[index]); err != nil {
			return err
		}
	}

	if err := systemctl(ctx, "daemon-reload"); err != nil {
		return err
	}
	if err := systemctl(ctx, "enable", serviceName(id)); err != nil {
		return err
	}

	return m.save(apps)
}

func (m *Manager) Logs(ctx context.Context, id int64, lines int) (string, error) {
	if lines <= 0 || lines > 1000 {
		lines = 200
	}
	if _, err := m.Get(id); err != nil {
		return "", err
	}

	cmd := exec.CommandContext(ctx, "journalctl", "-u", serviceName(id), "-n", strconv.Itoa(lines), "--no-pager", "-o", "short-iso")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("read app logs: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (m *Manager) Start(ctx context.Context, id int64) error {
	app, err := m.Get(id)
	if err != nil {
		return err
	}
	if err := validateRunnable(app); err != nil {
		return err
	}
	return systemctl(ctx, "start", serviceName(id))
}

func (m *Manager) Stop(ctx context.Context, id int64) error {
	app, err := m.Get(id)
	if err != nil {
		return err
	}
	if err := validateRunnable(app); err != nil {
		return err
	}
	return systemctl(ctx, "stop", serviceName(id))
}

func (m *Manager) Restart(ctx context.Context, id int64) error {
	app, err := m.Get(id)
	if err != nil {
		return err
	}
	if err := validateRunnable(app); err != nil {
		return err
	}
	return systemctl(ctx, "restart", serviceName(id))
}

func (m *Manager) Status(ctx context.Context, id int64) string {
	app, err := m.Get(id)
	if err != nil || app.Type == "static" || app.Command == "" {
		return "not configured"
	}

	cmd := exec.CommandContext(ctx, "systemctl", "is-active", serviceName(id))
	out, err := cmd.Output()
	status := strings.TrimSpace(string(out))
	if err != nil {
		if status != "" {
			return status
		}
		return "inactive"
	}

	return status
}

func (m *Manager) writeRunner(app App) error {
	if err := os.MkdirAll(m.runnerDir, 0755); err != nil {
		return fmt.Errorf("create runner directory: %w", err)
	}

	path := runnerPath(m.runnerDir, app.ID)
	command := runnerCommand(app)
	content := "#!/usr/bin/env bash\nset -e\n" + command + "\n"
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		return fmt.Errorf("write app runner: %w", err)
	}
	if err := os.Chmod(path, 0755); err != nil {
		return fmt.Errorf("chmod app runner: %w", err)
	}

	return nil
}

func (m *Manager) writeUnit(app App) error {
	var b strings.Builder
	fmt.Fprintf(&b, "[Unit]\nDescription=Open Go Panel app %d (%s/%s)\nAfter=network-online.target\nWants=network-online.target\n\n", app.ID, app.User, app.Name)
	fmt.Fprintf(&b, "[Service]\nType=simple\nUser=%s\nWorkingDirectory=%s\n", app.User, app.Root)
	if app.Port > 0 {
		fmt.Fprintf(&b, "Environment=PORT=%d\n", app.Port)
	}
	fmt.Fprintf(&b, "EnvironmentFile=-%s\n", envPath(m.envDir, app.ID))
	fmt.Fprintf(&b, "ExecStart=%s\n", runnerPath(m.runnerDir, app.ID))
	restart := app.Service.Restart
	if restart == "" {
		restart = "on-failure"
	}
	restartSec := app.Service.RestartSec
	if restartSec < 0 {
		restartSec = 0
	}
	fmt.Fprintf(&b, "Restart=%s\nRestartSec=%d\n", restart, restartSec)
	if app.Service.CPUQuota != "" {
		fmt.Fprintf(&b, "CPUQuota=%s\n", app.Service.CPUQuota)
	}
	if app.Service.MemoryMax != "" {
		fmt.Fprintf(&b, "MemoryMax=%s\n", app.Service.MemoryMax)
	}
	fmt.Fprintf(&b, "\n[Install]\nWantedBy=multi-user.target\n")

	path := filepath.Join(m.serviceDir, serviceName(app.ID))
	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		return fmt.Errorf("write systemd unit: %w", err)
	}

	return nil
}
, cfg.MemoryMax)
		if !ok {
			return errors.New("memory limit must look like 512M or 2G")
		}
	}

	for _, line := range strings.Split(cfg.Environment, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid environment line %q", line)
		}
		if ok, _ := regexp.MatchString(`^[A-Za-z_][A-Za-z0-9_]*package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bau59/open-go-panel/internal/linuxuser"
)

const (
	minPort = 8100
	maxPort = 8999
)

var appNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

type ServiceConfig struct {
	Mode        string `json:"mode,omitempty"`
	RunMode     string `json:"run_mode,omitempty"`
	Command     string `json:"command,omitempty"`
	Restart     string `json:"restart,omitempty"`
	RestartSec  int    `json:"restart_sec,omitempty"`
	CPUQuota    string `json:"cpu_quota,omitempty"`
	MemoryMax   string `json:"memory_max,omitempty"`
	Environment string `json:"environment,omitempty"`
	RawUnit     string `json:"raw_unit,omitempty"`
}

type App struct {
	ID        int64         `json:"id"`
	User      string        `json:"user"`
	Name      string        `json:"name"`
	Type      string        `json:"type"`
	Root      string        `json:"root"`
	Port      int           `json:"port,omitempty"`
	Command   string        `json:"command,omitempty"`
	Service   ServiceConfig `json:"service,omitempty"`
	CreatedAt time.Time     `json:"created_at"`
}

type Manager struct {
	mu          sync.Mutex
	stateFile   string
	serviceDir  string
	runnerDir   string
	envDir      string
	users       *linuxuser.Manager
}

func New(stateFile string, users *linuxuser.Manager) *Manager {
	return &Manager{
		stateFile:  stateFile,
		serviceDir: "/etc/systemd/system",
		runnerDir:  "/var/lib/open-go-panel/runners",
		envDir:     "/var/lib/open-go-panel/env",
		users:      users,
	}
}

func (m *Manager) List() ([]App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	apps, err := m.load()
	if err != nil {
		return nil, err
	}

	sort.Slice(apps, func(i, j int) bool {
		return apps[i].ID < apps[j].ID
	})

	return apps, nil
}

func (m *Manager) Create(username, name, appType string) (App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !appNamePattern.MatchString(name) {
		return App{}, errors.New("app name must contain only lowercase letters, digits, underscore or hyphen")
	}

	switch appType {
	case "go", "node", "static", "worker":
	default:
		return App{}, errors.New("unsupported app type")
	}

	managed, err := m.userManaged(username)
	if err != nil {
		return App{}, err
	}
	if !managed {
		return App{}, fmt.Errorf("user %q is not managed by Open Go Panel", username)
	}

	account, err := user.Lookup(username)
	if err != nil {
		return App{}, fmt.Errorf("lookup user %q: %w", username, err)
	}

	apps, err := m.load()
	if err != nil {
		return App{}, err
	}

	for _, existing := range apps {
		if existing.User == username && existing.Name == name {
			return App{}, fmt.Errorf("app %q already exists for user %q", name, username)
		}
	}

	root := filepath.Join(account.HomeDir, "apps", name)
	if err := os.MkdirAll(root, 0750); err != nil {
		return App{}, fmt.Errorf("create app directory: %w", err)
	}

	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return App{}, fmt.Errorf("parse uid: %w", err)
	}
	gid, err := strconv.Atoi(account.Gid)
	if err != nil {
		return App{}, fmt.Errorf("parse gid: %w", err)
	}

	if err := os.Chown(filepath.Join(account.HomeDir, "apps"), uid, gid); err != nil {
		return App{}, fmt.Errorf("chown apps directory: %w", err)
	}
	if err := os.Chown(root, uid, gid); err != nil {
		return App{}, fmt.Errorf("chown app directory: %w", err)
	}

	app := App{
		ID:        nextID(apps),
		User:      username,
		Name:      name,
		Type:      appType,
		Root:      root,
		CreatedAt: time.Now().UTC(),
	}

	if appType == "go" || appType == "node" {
		port, err := nextPort(apps)
		if err != nil {
			return App{}, err
		}
		app.Port = port
	}

	apps = append(apps, app)

	if err := m.save(apps); err != nil {
		return App{}, err
	}

	return app, nil
}

func (m *Manager) Get(id int64) (App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	apps, err := m.load()
	if err != nil {
		return App{}, err
	}

	for _, app := range apps {
		if app.ID == id {
			return app, nil
		}
	}

	return App{}, fmt.Errorf("app %d not found", id)
}

func (m *Manager) SetCommand(ctx context.Context, id int64, command string) error {
	return m.SetServiceConfig(ctx, id, ServiceConfig{
		Mode:       "form",
		RunMode:    "custom",
		Command:    command,
		Restart:    "on-failure",
		RestartSec: 3,
	})
}

func (m *Manager) SetServiceConfig(ctx context.Context, id int64, cfg ServiceConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	apps, err := m.load()
	if err != nil {
		return err
	}

	index := -1
	for i := range apps {
		if apps[i].ID == id {
			index = i
			break
		}
	}
	if index == -1 {
		return fmt.Errorf("app %d not found", id)
	}
	if apps[index].Type == "static" {
		return errors.New("static apps do not use systemd services")
	}

	cfg.Mode = strings.TrimSpace(cfg.Mode)
	if cfg.Mode == "" {
		cfg.Mode = "form"
	}

	if cfg.Mode == "raw" {
		cfg.RawUnit = strings.TrimSpace(cfg.RawUnit)
		if cfg.RawUnit == "" {
			return errors.New("raw unit is required")
		}
		apps[index].Service = cfg
		if err := m.writeRawUnit(ctx, apps[index]); err != nil {
			return err
		}
	} else {
		if err := validateServiceConfig(apps[index].Type, &cfg); err != nil {
			return err
		}
		apps[index].Service = cfg
		apps[index].Command = cfg.Command

		if err := m.writeEnvironment(apps[index]); err != nil {
			return err
		}
		if err := m.writeRunner(apps[index]); err != nil {
			return err
		}
		if err := m.writeUnit(apps[index]); err != nil {
			return err
		}
	}

	if err := systemctl(ctx, "daemon-reload"); err != nil {
		return err
	}
	if err := systemctl(ctx, "enable", serviceName(id)); err != nil {
		return err
	}

	return m.save(apps)
}

func (m *Manager) Logs(ctx context.Context, id int64, lines int) (string, error) {
	if lines <= 0 || lines > 1000 {
		lines = 200
	}
	if _, err := m.Get(id); err != nil {
		return "", err
	}

	cmd := exec.CommandContext(ctx, "journalctl", "-u", serviceName(id), "-n", strconv.Itoa(lines), "--no-pager", "-o", "short-iso")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("read app logs: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (m *Manager) Start(ctx context.Context, id int64) error {
	app, err := m.Get(id)
	if err != nil {
		return err
	}
	if err := validateRunnable(app); err != nil {
		return err
	}
	return systemctl(ctx, "start", serviceName(id))
}

func (m *Manager) Stop(ctx context.Context, id int64) error {
	app, err := m.Get(id)
	if err != nil {
		return err
	}
	if err := validateRunnable(app); err != nil {
		return err
	}
	return systemctl(ctx, "stop", serviceName(id))
}

func (m *Manager) Restart(ctx context.Context, id int64) error {
	app, err := m.Get(id)
	if err != nil {
		return err
	}
	if err := validateRunnable(app); err != nil {
		return err
	}
	return systemctl(ctx, "restart", serviceName(id))
}

func (m *Manager) Status(ctx context.Context, id int64) string {
	app, err := m.Get(id)
	if err != nil || app.Type == "static" || app.Command == "" {
		return "not configured"
	}

	cmd := exec.CommandContext(ctx, "systemctl", "is-active", serviceName(id))
	out, err := cmd.Output()
	status := strings.TrimSpace(string(out))
	if err != nil {
		if status != "" {
			return status
		}
		return "inactive"
	}

	return status
}

func (m *Manager) writeRunner(app App) error {
	if err := os.MkdirAll(m.runnerDir, 0755); err != nil {
		return fmt.Errorf("create runner directory: %w", err)
	}

	path := runnerPath(m.runnerDir, app.ID)
	command := runnerCommand(app)
	content := "#!/usr/bin/env bash\nset -e\n" + command + "\n"
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		return fmt.Errorf("write app runner: %w", err)
	}
	if err := os.Chmod(path, 0755); err != nil {
		return fmt.Errorf("chmod app runner: %w", err)
	}

	return nil
}

func (m *Manager) writeUnit(app App) error {
	var b strings.Builder
	fmt.Fprintf(&b, "[Unit]\nDescription=Open Go Panel app %d (%s/%s)\nAfter=network-online.target\nWants=network-online.target\n\n", app.ID, app.User, app.Name)
	fmt.Fprintf(&b, "[Service]\nType=simple\nUser=%s\nWorkingDirectory=%s\n", app.User, app.Root)
	if app.Port > 0 {
		fmt.Fprintf(&b, "Environment=PORT=%d\n", app.Port)
	}
	fmt.Fprintf(&b, "EnvironmentFile=-%s\n", envPath(m.envDir, app.ID))
	fmt.Fprintf(&b, "ExecStart=%s\n", runnerPath(m.runnerDir, app.ID))
	restart := app.Service.Restart
	if restart == "" {
		restart = "on-failure"
	}
	restartSec := app.Service.RestartSec
	if restartSec < 0 {
		restartSec = 0
	}
	fmt.Fprintf(&b, "Restart=%s\nRestartSec=%d\n", restart, restartSec)
	if app.Service.CPUQuota != "" {
		fmt.Fprintf(&b, "CPUQuota=%s\n", app.Service.CPUQuota)
	}
	if app.Service.MemoryMax != "" {
		fmt.Fprintf(&b, "MemoryMax=%s\n", app.Service.MemoryMax)
	}
	fmt.Fprintf(&b, "\n[Install]\nWantedBy=multi-user.target\n")

	path := filepath.Join(m.serviceDir, serviceName(app.ID))
	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		return fmt.Errorf("write systemd unit: %w", err)
	}

	return nil
}
, parts[0]); !ok {
			return fmt.Errorf("invalid environment variable name %q", parts[0])
		}
	}

	return nil
}

func runnerCommand(app App) string {
	switch app.Service.RunMode {
	case "go-air":
		return "exec air"
	case "go-build":
		return "go build -o .ogp-app . && exec ./.ogp-app"
	case "node-npm":
		return "exec npm start"
	default:
		return app.Service.Command
	}
}

func (m *Manager) writeEnvironment(app App) error {
	if err := os.MkdirAll(m.envDir, 0700); err != nil {
		return fmt.Errorf("create environment directory: %w", err)
	}
	return os.WriteFile(envPath(m.envDir, app.ID), []byte(strings.TrimSpace(app.Service.Environment)+"\n"), 0600)
}

func (m *Manager) writeRawUnit(ctx context.Context, app App) error {
	tmp, err := os.CreateTemp(m.serviceDir, ".open-go-panel-*.service")
	if err != nil {
		return fmt.Errorf("create temporary systemd unit: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.WriteString(app.Service.RawUnit + "\n"); err != nil {
		tmp.Close()
		return fmt.Errorf("write temporary systemd unit: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary systemd unit: %w", err)
	}

	verify := exec.CommandContext(ctx, "systemd-analyze", "verify", tmpName)
	if out, err := verify.CombinedOutput(); err != nil {
		return fmt.Errorf("invalid systemd unit: %w: %s", err, strings.TrimSpace(string(out)))
	}

	path := filepath.Join(m.serviceDir, serviceName(app.ID))
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("install systemd unit: %w", err)
	}
	return nil
}

func envPath(dir string, id int64) string {
	return filepath.Join(dir, fmt.Sprintf("app-%d.env", id))
}

func validateRunnable(app App) error {
	if app.Type == "static" {
		return errors.New("static apps do not use systemd services")
	}
	if app.Service.Mode == "raw" {
		if strings.TrimSpace(app.Service.RawUnit) == "" {
			return errors.New("configure the systemd unit first")
		}
		return nil
	}
	if strings.TrimSpace(runnerCommand(app)) == "" {
		return errors.New("configure a start command first")
	}
	return nil
}

func systemctl(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "systemctl", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func serviceName(id int64) string {
	return fmt.Sprintf("open-go-panel-app-%d.service", id)
}

func runnerPath(dir string, id int64) string {
	return filepath.Join(dir, fmt.Sprintf("app-%d.sh", id))
}

func (m *Manager) userManaged(username string) (bool, error) {
	users, err := m.users.List(context.Background())
	if err != nil {
		return false, err
	}

	for _, u := range users {
		if u.Username == username {
			return true, nil
		}
	}

	return false, nil
}

func (m *Manager) load() ([]App, error) {
	data, err := os.ReadFile(m.stateFile)
	if errors.Is(err, os.ErrNotExist) {
		return []App{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read apps state: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return []App{}, nil
	}

	var apps []App
	if err := json.Unmarshal(data, &apps); err != nil {
		return nil, fmt.Errorf("decode apps state: %w", err)
	}

	return apps, nil
}

func (m *Manager) save(apps []App) error {
	if err := os.MkdirAll(filepath.Dir(m.stateFile), 0700); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}

	data, err := json.MarshalIndent(apps, "", "  ")
	if err != nil {
		return fmt.Errorf("encode apps state: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(m.stateFile), ".apps-*.json")
	if err != nil {
		return fmt.Errorf("create temporary apps state: %w", err)
	}
	tmpName := tmp.Name()

	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write apps state: %w", err)
	}
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod apps state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close apps state: %w", err)
	}

	if err := os.Rename(tmpName, m.stateFile); err != nil {
		return fmt.Errorf("replace apps state: %w", err)
	}

	return nil
}

func nextID(apps []App) int64 {
	var maxID int64
	for _, app := range apps {
		if app.ID > maxID {
			maxID = app.ID
		}
	}
	return maxID + 1
}

func nextPort(apps []App) (int, error) {
	used := make(map[int]struct{}, len(apps))
	for _, app := range apps {
		if app.Port > 0 {
			used[app.Port] = struct{}{}
		}
	}

	for port := minPort; port <= maxPort; port++ {
		if _, exists := used[port]; !exists {
			return port, nil
		}
	}

	return 0, errors.New("no free application ports available")
}

