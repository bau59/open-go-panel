package dbmanager

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var nameRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,62}$`)

type Database struct {
	ID        int64     `json:"id"`
	Engine    string    `json:"engine"`
	Name      string    `json:"name"`
	User      string    `json:"user"`
	Password  string    `json:"password"`
	CreatedAt time.Time `json:"created_at"`
}

type Status struct {
	MySQLInstalled    bool
	MySQLActive       bool
	PostgresInstalled bool
	PostgresActive    bool
}

type Manager struct {
	mu        sync.Mutex
	stateFile string
}

func New(stateFile string) *Manager {
	return &Manager{stateFile: stateFile}
}

func (m *Manager) Status(ctx context.Context) Status {
	_, mysqlErr := exec.LookPath("mysql")
	_, psqlErr := exec.LookPath("psql")
	return Status{
		MySQLInstalled:    mysqlErr == nil,
		MySQLActive:       serviceActive(ctx, "mysql.service"),
		PostgresInstalled: psqlErr == nil,
		PostgresActive:    serviceActive(ctx, "postgresql.service"),
	}
}

func (m *Manager) Install(ctx context.Context, engine string) error {
	switch engine {
	case "mysql":
		if err := run(ctx, "", "apt-get", "update"); err != nil { return err }
		if err := run(ctx, "", "apt-get", "install", "-y", "mysql-server"); err != nil { return err }
		return run(ctx, "", "systemctl", "enable", "--now", "mysql.service")
	case "postgres":
		if err := run(ctx, "", "apt-get", "update"); err != nil { return err }
		if err := run(ctx, "", "apt-get", "install", "-y", "postgresql", "postgresql-contrib"); err != nil { return err }
		return run(ctx, "", "systemctl", "enable", "--now", "postgresql.service")
	default:
		return errors.New("unsupported database engine")
	}
}

func (m *Manager) List() ([]Database, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	items, err := m.load()
	if err != nil { return nil, err }
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}

func (m *Manager) Create(ctx context.Context, engine, name, username string) (Database, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	engine = strings.TrimSpace(engine)
	name = strings.TrimSpace(name)
	username = strings.TrimSpace(username)

	if !nameRE.MatchString(name) {
		return Database{}, errors.New("database name must contain only letters, digits and underscore")
	}
	if username == "" {
		username = "ogp_" + strings.ToLower(name)
		if len(username) > 63 { username = username[:63] }
	}
	if !nameRE.MatchString(username) {
		return Database{}, errors.New("database user must contain only letters, digits and underscore")
	}

	items, err := m.load()
	if err != nil { return Database{}, err }
	for _, item := range items {
		if item.Engine == engine && item.Name == name {
			return Database{}, fmt.Errorf("database %q already exists in Open Go Panel", name)
		}
	}

	password, err := randomPassword()
	if err != nil { return Database{}, err }

	switch engine {
	case "mysql":
		if _, err := exec.LookPath("mysql"); err != nil {
			return Database{}, errors.New("MySQL is not installed")
		}
		sql := fmt.Sprintf(
			"CREATE DATABASE `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci; CREATE USER '%s'@'localhost' IDENTIFIED BY '%s'; GRANT ALL PRIVILEGES ON `%s`.* TO '%s'@'localhost'; FLUSH PRIVILEGES;",
			name, username, sqlString(password), name, username,
		)
		if err := run(ctx, sql, "mysql", "--protocol=socket", "-uroot"); err != nil {
			return Database{}, err
		}
	case "postgres":
		if _, err := exec.LookPath("psql"); err != nil {
			return Database{}, errors.New("PostgreSQL is not installed")
		}
		roleSQL := fmt.Sprintf("CREATE ROLE \"%s\" LOGIN PASSWORD '%s';", username, sqlString(password))
		if err := run(ctx, roleSQL, "runuser", "-u", "postgres", "--", "psql", "-v", "ON_ERROR_STOP=1", "-d", "postgres"); err != nil {
			return Database{}, err
		}
		createSQL := fmt.Sprintf("CREATE DATABASE \"%s\" OWNER \"%s\";", name, username)
		if err := run(ctx, createSQL, "runuser", "-u", "postgres", "--", "psql", "-v", "ON_ERROR_STOP=1", "-d", "postgres"); err != nil {
			_ = run(ctx, fmt.Sprintf("DROP ROLE IF EXISTS \"%s\";", username), "runuser", "-u", "postgres", "--", "psql", "-d", "postgres")
			return Database{}, err
		}
	default:
		return Database{}, errors.New("unsupported database engine")
	}

	item := Database{
		ID: nextID(items), Engine: engine, Name: name, User: username,
		Password: password, CreatedAt: time.Now().UTC(),
	}
	items = append(items, item)
	if err := m.save(items); err != nil {
		return Database{}, err
	}
	return item, nil
}

func (m *Manager) Delete(ctx context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	items, err := m.load()
	if err != nil { return err }

	idx := -1
	var item Database
	for i := range items {
		if items[i].ID == id {
			idx, item = i, items[i]
			break
		}
	}
	if idx == -1 { return fmt.Errorf("database %d not found", id) }

	switch item.Engine {
	case "mysql":
		sql := fmt.Sprintf(
			"DROP DATABASE IF EXISTS `%s`; DROP USER IF EXISTS '%s'@'localhost'; FLUSH PRIVILEGES;",
			item.Name, item.User,
		)
		if err := run(ctx, sql, "mysql", "--protocol=socket", "-uroot"); err != nil { return err }
	case "postgres":
		sql := fmt.Sprintf(
			"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname='%s' AND pid <> pg_backend_pid(); DROP DATABASE IF EXISTS \"%s\"; DROP ROLE IF EXISTS \"%s\";",
			sqlString(item.Name), item.Name, item.User,
		)
		if err := run(ctx, sql, "runuser", "-u", "postgres", "--", "psql", "-v", "ON_ERROR_STOP=1", "-d", "postgres"); err != nil { return err }
	default:
		return errors.New("unsupported database engine")
	}

	items = append(items[:idx], items[idx+1:]...)
	return m.save(items)
}

func (d Database) DSN() string {
	switch d.Engine {
	case "mysql":
		return fmt.Sprintf("%s:%s@tcp(127.0.0.1:3306)/%s", d.User, d.Password, d.Name)
	case "postgres":
		return fmt.Sprintf("postgres://%s:%s@127.0.0.1:5432/%s?sslmode=disable", d.User, d.Password, d.Name)
	default:
		return ""
	}
}

func randomPassword() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil { return "", err }
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func sqlString(v string) string {
	return strings.ReplaceAll(v, "'", "''")
}

func serviceActive(ctx context.Context, name string) bool {
	return exec.CommandContext(ctx, "systemctl", "is-active", "--quiet", name).Run() == nil
}

func run(ctx context.Context, stdin, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func nextID(items []Database) int64 {
	var max int64
	for _, item := range items {
		if item.ID > max { max = item.ID }
	}
	return max + 1
}

func (m *Manager) load() ([]Database, error) {
	data, err := os.ReadFile(m.stateFile)
	if errors.Is(err, os.ErrNotExist) { return []Database{}, nil }
	if err != nil { return nil, err }
	if len(strings.TrimSpace(string(data))) == 0 { return []Database{}, nil }
	var items []Database
	if err := json.Unmarshal(data, &items); err != nil { return nil, err }
	return items, nil
}

func (m *Manager) save(items []Database) error {
	if err := os.MkdirAll(filepath.Dir(m.stateFile), 0755); err != nil { return err }
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil { return err }
	data = append(data, '\n')

	tmp, err := os.CreateTemp(filepath.Dir(m.stateFile), ".databases-*")
	if err != nil { return err }
	path := tmp.Name()
	defer os.Remove(path)

	if _, err := tmp.Write(data); err != nil { _ = tmp.Close(); return err }
	if err := tmp.Chmod(0600); err != nil { _ = tmp.Close(); return err }
	if err := tmp.Close(); err != nil { return err }
	return os.Rename(path, m.stateFile)
}
