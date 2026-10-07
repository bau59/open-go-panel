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

type App struct {
	ID        int64     `json:"id"`
	User      string    `json:"user"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Root      string    `json:"root"`
	Port      int       `json:"port,omitempty"`
	Command   string    `json:"command,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type Manager struct {
	mu          sync.Mutex
	stateFile   string
	serviceDir  string
	runnerDir   string
	users       *linuxuser.Manager
}

func New(stateFile string, users *linuxuser.Manager) *Manager {
	return &Manager{
		stateFile:  stateFile,
		serviceDir: "/etc/systemd/system",
		runnerDir:  "/var/lib/open-go-panel/runners",
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
	m.mu.Lock()
	defer m.mu.Unlock()

	command = strings.TrimSpace(command)
	if command == "" {
		return errors.New("start command is required")
	}

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

	apps[index].Command = command

	if err := m.writeRunner(apps[index]); err != nil {
		return err
	}
	if err := m.writeUnit(apps[index]); err != nil {
		return err
	}
	if err := systemctl(ctx, "daemon-reload"); err != nil {
		return err
	}
	if err := systemctl(ctx, "enable", serviceName(id)); err != nil {
		return err
	}

	return m.save(apps)
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
	content := "#!/usr/bin/env bash\nset -e\n" + app.Command + "\n"
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
	fmt.Fprintf(&b, "ExecStart=%s\nRestart=on-failure\nRestartSec=3\n\n[Install]\nWantedBy=multi-user.target\n", runnerPath(m.runnerDir, app.ID))

	path := filepath.Join(m.serviceDir, serviceName(app.ID))
	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		return fmt.Errorf("write systemd unit: %w", err)
	}

	return nil
}

func validateRunnable(app App) error {
	if app.Type == "static" {
		return errors.New("static apps do not use systemd services")
	}
	if strings.TrimSpace(app.Command) == "" {
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

