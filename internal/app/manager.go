package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
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
	CreatedAt time.Time `json:"created_at"`
}

type Manager struct {
	mu        sync.Mutex
	stateFile string
	users     *linuxuser.Manager
}

func New(stateFile string, users *linuxuser.Manager) *Manager {
	return &Manager{
		stateFile: stateFile,
		users:     users,
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

func (m *Manager) userManaged(username string) (bool, error) {
	users, err := m.users.List(nilContext{})
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

// nilContext is enough for linuxuser.List, which only passes the context to local commands.
type nilContext struct{}

func (nilContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (nilContext) Done() <-chan struct{}       { return nil }
func (nilContext) Err() error                  { return nil }
func (nilContext) Value(any) any               { return nil }
