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

	"github.com/bau59/open-go-panel/internal/state"
)

var (
	databaseNameRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,62}package dbmanager

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

	"github.com/bau59/open-go-panel/internal/state"
)

)
	mysqlUserRE    = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,31}package dbmanager

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

	"github.com/bau59/open-go-panel/internal/state"
)

)
	postgresUserRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,62}package dbmanager

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

	"github.com/bau59/open-go-panel/internal/state"
)

)
)

type Database struct {
	ID        int64     `json:"id"`
	Engine    string    `json:"engine"`
	Name      string    `json:"name"`
	User      string    `json:"user"`
	Password  string    `json:"password"`
	CreatedAt time.Time `json:"created_at"`
}

type Attachment struct {
	AppID      int64
	DatabaseID int64
	EnvName    string
	CreatedAt  time.Time
}


type Status struct {
	MySQLInstalled    bool
	MySQLActive       bool
	PostgresInstalled bool
	PostgresActive    bool
}

type Manager struct {
	mu              sync.Mutex
	store           *state.Store
	legacyStateFile string
	mysqlConfigFile string
}

func New(store *state.Store, legacyStateFile string) *Manager {
	return &Manager{
		store:           store,
		legacyStateFile: legacyStateFile,
		mysqlConfigFile: "/etc/mysql/mysql.conf.d/99-open-go-panel.cnf",
	}
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


func (m *Manager) MySQLConfig() (string, error) {
	data, err := os.ReadFile(m.mysqlConfigFile)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read MySQL config: %w", err)
	}
	return string(data), nil
}

func (m *Manager) RecommendedMySQLConfig(memoryTotal uint64, cpus int) string {
	const gib = uint64(1024 * 1024 * 1024)
	bufferPool := "256M"
	tmpSize := "32M"
	maxConnections := 100
	threadCache := 32

	switch {
	case memoryTotal >= 16*gib:
		bufferPool = "4G"
		tmpSize = "128M"
		maxConnections = 300
		threadCache = 100
	case memoryTotal >= 8*gib:
		bufferPool = "2G"
		tmpSize = "64M"
		maxConnections = 250
		threadCache = 80
	case memoryTotal >= 4*gib:
		bufferPool = "1G"
		tmpSize = "64M"
		maxConnections = 200
		threadCache = 64
	case memoryTotal >= 2*gib:
		bufferPool = "512M"
		tmpSize = "32M"
		maxConnections = 150
		threadCache = 48
	}
	if cpus <= 1 && maxConnections > 100 {
		maxConnections = 100
	}

	return fmt.Sprintf(`# Managed by Open Go Panel
# Conservative shared-server preset. Review before applying.
[mysqld]
bind-address = 127.0.0.1

# InnoDB
innodb_buffer_pool_size = %s
innodb_flush_method = O_DIRECT
innodb_flush_log_at_trx_commit = 1

# Connections and caches
max_connections = %d
thread_cache_size = %d
table_open_cache = 2000
table_definition_cache = 1400

# Temporary tables
tmp_table_size = %s
max_heap_table_size = %s

# Safety / diagnostics
max_allowed_packet = 64M
slow_query_log = ON
long_query_time = 1
log_queries_not_using_indexes = OFF
`, bufferPool, maxConnections, threadCache, tmpSize, tmpSize)
}

func (m *Manager) ApplyMySQLConfig(ctx context.Context, config string) error {
	if _, err := exec.LookPath("mysqld"); err != nil {
		return errors.New("MySQL server is not installed")
	}
	config = strings.TrimSpace(config)
	if config == "" {
		return errors.New("MySQL config cannot be empty")
	}
	if !strings.Contains(config, "[mysqld]") {
		return errors.New("MySQL config must contain [mysqld]")
	}
	config += "\n"

	if err := os.MkdirAll(filepath.Dir(m.mysqlConfigFile), 0755); err != nil {
		return fmt.Errorf("create MySQL config directory: %w", err)
	}

	var previous []byte
	hadPrevious := false
	if data, err := os.ReadFile(m.mysqlConfigFile); err == nil {
		previous = data
		hadPrevious = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(m.mysqlConfigFile), ".open-go-panel-mysql-*.cnf")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.WriteString(config); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0644); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, m.mysqlConfigFile); err != nil {
		return err
	}

	restore := func() {
		if hadPrevious {
			_ = os.WriteFile(m.mysqlConfigFile, previous, 0644)
		} else {
			_ = os.Remove(m.mysqlConfigFile)
		}
	}

	if out, err := exec.CommandContext(ctx, "mysqld", "--validate-config").CombinedOutput(); err != nil {
		restore()
		return fmt.Errorf("invalid MySQL config: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.CommandContext(ctx, "systemctl", "restart", "mysql.service").CombinedOutput(); err != nil {
		restore()
		_ = exec.CommandContext(ctx, "systemctl", "restart", "mysql.service").Run()
		return fmt.Errorf("restart MySQL: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
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

func (m *Manager) Get(id int64) (Database, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	items, err := m.load()
	if err != nil { return Database{}, err }
	for _, item := range items {
		if item.ID == id {
			return item, nil
		}
	}
	return Database{}, fmt.Errorf("database %d not found", id)
}

func (m *Manager) Attach(appID, databaseID int64, envName string) error {
	envName = strings.TrimSpace(envName)
	if appID <= 0 || databaseID <= 0 || envName == "" {
		return errors.New("invalid database attachment")
	}
	_, err := m.store.DB().Exec(`
		INSERT INTO app_databases(app_id, database_id, env_name, created_at)
		VALUES(?, ?, ?, ?)
		ON CONFLICT(app_id, env_name) DO UPDATE SET
			database_id=excluded.database_id,
			created_at=excluded.created_at
	`, appID, databaseID, envName, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("save database attachment: %w", err)
	}
	return nil
}

func (m *Manager) Detach(appID int64, envName string) error {
	envName = strings.TrimSpace(envName)
	if appID <= 0 || envName == "" {
		return errors.New("invalid database attachment")
	}
	_, err := m.store.DB().Exec(
		`DELETE FROM app_databases WHERE app_id = ? AND env_name = ?`,
		appID, envName,
	)
	if err != nil {
		return fmt.Errorf("delete database attachment: %w", err)
	}
	return nil
}

func (m *Manager) AttachmentsForApp(appID int64) ([]Attachment, error) {
	rows, err := m.store.DB().Query(`
		SELECT app_id, database_id, env_name, created_at
		FROM app_databases
		WHERE app_id = ?
		ORDER BY env_name
	`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []Attachment
	for rows.Next() {
		var item Attachment
		var createdAt string
		if err := rows.Scan(&item.AppID, &item.DatabaseID, &item.EnvName, &createdAt); err != nil {
			return nil, err
		}
		if t, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
			item.CreatedAt = t
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (m *Manager) AttachmentsForDatabase(databaseID int64) ([]Attachment, error) {
	rows, err := m.store.DB().Query(`
		SELECT app_id, database_id, env_name, created_at
		FROM app_databases
		WHERE database_id = ?
		ORDER BY app_id, env_name
	`, databaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []Attachment
	for rows.Next() {
		var item Attachment
		var createdAt string
		if err := rows.Scan(&item.AppID, &item.DatabaseID, &item.EnvName, &createdAt); err != nil {
			return nil, err
		}
		if t, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
			item.CreatedAt = t
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (m *Manager) Create(ctx context.Context, engine, name, username string) (Database, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	engine = strings.TrimSpace(engine)
	name = strings.TrimSpace(name)
	username = strings.TrimSpace(username)

	if !databaseNameRE.MatchString(name) {
		return Database{}, errors.New("database name must contain only letters, digits and underscore")
	}
	if username == "" {
		username = "ogp_" + strings.ToLower(name)
		if len(username) > 63 { username = username[:63] }
	}
	userRE := postgresUserRE
	if engine == "mysql" {
		userRE = mysqlUserRE
	}
	if !userRE.MatchString(username) {
		if engine == "mysql" {
			return Database{}, errors.New("MySQL user must be 1-32 characters and contain only letters, digits and underscore")
		}
		return Database{}, errors.New("PostgreSQL user must contain only letters, digits and underscore")
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

	attachments, err := m.AttachmentsForDatabase(id)
	if err != nil {
		return fmt.Errorf("check database attachments: %w", err)
	}
	if len(attachments) > 0 {
		return fmt.Errorf("database %q is attached to %d app environment variable(s); detach it first", item.Name, len(attachments))
	}

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
	rows, err := m.store.DB().Query(`
		SELECT id, engine, name, username, password, created_at
		FROM databases
		ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("query databases state: %w", err)
	}
	defer rows.Close()

	var items []Database
	for rows.Next() {
		var item Database
		var createdAt string
		if err := rows.Scan(&item.ID, &item.Engine, &item.Name, &item.User, &item.Password, &createdAt); err != nil {
			return nil, fmt.Errorf("scan database state: %w", err)
		}
		if createdAt != "" {
			if t, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
				item.CreatedAt = t
			}
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate databases state: %w", err)
	}

	if len(items) == 0 {
		legacy, err := m.loadLegacy()
		if err != nil {
			return nil, err
		}
		if len(legacy) > 0 {
			if err := m.save(legacy); err != nil {
				return nil, fmt.Errorf("migrate legacy databases state: %w", err)
			}
			return legacy, nil
		}
	}

	return items, nil
}

func (m *Manager) loadLegacy() ([]Database, error) {
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
	var items []Database
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (m *Manager) save(items []Database) error {
	tx, err := m.store.DB().Begin()
	if err != nil {
		return fmt.Errorf("begin databases state transaction: %w", err)
	}
	defer tx.Rollback()

	keep := make(map[int64]struct{}, len(items))
	for _, item := range items {
		createdAt := item.CreatedAt.UTC().Format(time.RFC3339Nano)
		if item.CreatedAt.IsZero() {
			createdAt = time.Now().UTC().Format(time.RFC3339Nano)
		}
		if _, err := tx.Exec(`
			INSERT INTO databases(id, engine, name, username, password, created_at)
			VALUES(?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				engine=excluded.engine,
				name=excluded.name,
				username=excluded.username,
				password=excluded.password,
				created_at=excluded.created_at
		`, item.ID, item.Engine, item.Name, item.User, item.Password, createdAt); err != nil {
			return fmt.Errorf("save database %d state: %w", item.ID, err)
		}
		keep[item.ID] = struct{}{}
	}

	rows, err := tx.Query(`SELECT id FROM databases`)
	if err != nil {
		return fmt.Errorf("query existing database ids: %w", err)
	}
	var stale []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if _, ok := keep[id]; !ok {
			stale = append(stale, id)
		}
	}
	rows.Close()
	for _, id := range stale {
		if _, err := tx.Exec(`DELETE FROM databases WHERE id = ?`, id); err != nil {
			return fmt.Errorf("delete stale database %d state: %w", id, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit databases state: %w", err)
	}
	return nil
}
