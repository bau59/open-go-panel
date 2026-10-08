package dbmanager

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

type RemoteImportTask struct {
	DatabaseID int64
	Engine     string
	SourceHost string
	Running    bool
	Error      string
	BackupPath string
	StartedAt  time.Time
	FinishedAt time.Time
}

type remoteConnection struct {
	Engine   string
	Host     string
	Port     string
	User     string
	Password string
	Database string
	SSLMode  string
}

func (m *Manager) ImportTask() RemoteImportTask {
	m.importMu.Lock()
	defer m.importMu.Unlock()
	return m.importTask
}

func (m *Manager) StartRemoteImport(id int64, connection string) error {
	target, err := m.Get(id)
	if err != nil {
		return err
	}
	source, err := parseRemoteConnection(connection)
	if err != nil {
		return err
	}
	if source.Engine != target.Engine {
		return fmt.Errorf("source is %s but target database is %s", source.Engine, target.Engine)
	}

	m.importMu.Lock()
	if m.importTask.Running {
		current := m.importTask
		m.importMu.Unlock()
		return fmt.Errorf("database import for #%d is already running", current.DatabaseID)
	}
	m.importTask = RemoteImportTask{
		DatabaseID: id,
		Engine: target.Engine,
		SourceHost: source.Host,
		Running: true,
		StartedAt: time.Now(),
	}
	m.importMu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()
		backupPath, importErr := m.remoteImport(ctx, target, source)

		m.importMu.Lock()
		m.importTask.Running = false
		m.importTask.FinishedAt = time.Now()
		m.importTask.BackupPath = backupPath
		if importErr != nil {
			m.importTask.Error = importErr.Error()
		} else {
			m.importTask.Error = ""
		}
		m.importMu.Unlock()
	}()
	return nil
}

func (m *Manager) remoteImport(ctx context.Context, target Database, source remoteConnection) (string, error) {
	preBackup, err := m.Backup(ctx, target.ID)
	if err != nil {
		return "", fmt.Errorf("create safety backup before import: %w", err)
	}

	tmp, err := os.CreateTemp("", "ogp-remote-import-*.sql.gz")
	if err != nil {
		return preBackup.Path, err
	}
	importPath := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(importPath)

	if err := dumpRemoteCompressed(ctx, source, importPath); err != nil {
		return preBackup.Path, err
	}
	if err := resetLocalDatabase(ctx, target); err != nil {
		return preBackup.Path, fmt.Errorf("reset local database: %w", err)
	}
	if err := restoreCompressedDump(ctx, target, importPath); err != nil {
		rollbackErr := m.Restore(context.Background(), target.ID, preBackup.Path)
		if rollbackErr != nil {
			return preBackup.Path, fmt.Errorf("remote import failed: %v; safety-backup rollback also failed: %v", err, rollbackErr)
		}
		return preBackup.Path, fmt.Errorf("remote import failed and local database was restored from safety backup: %w", err)
	}
	return preBackup.Path, nil
}

func parseRemoteConnection(raw string) (remoteConnection, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return remoteConnection{}, errors.New("remote database connection is required")
	}

	if strings.HasPrefix(raw, "mysql://") || strings.HasPrefix(raw, "postgres://") || strings.HasPrefix(raw, "postgresql://") {
		u, err := url.Parse(raw)
		if err != nil {
			return remoteConnection{}, fmt.Errorf("parse connection URL: %w", err)
		}
		engine := "mysql"
		defaultPort := "3306"
		if u.Scheme == "postgres" || u.Scheme == "postgresql" {
			engine = "postgres"
			defaultPort = "5432"
		}
		if u.User == nil || u.User.Username() == "" {
			return remoteConnection{}, errors.New("remote connection URL must include a username")
		}
		password, _ := u.User.Password()
		host := u.Hostname()
		if host == "" {
			return remoteConnection{}, errors.New("remote connection URL must include a host")
		}
		port := u.Port()
		if port == "" {
			port = defaultPort
		}
		database := strings.TrimPrefix(u.Path, "/")
		if database == "" {
			return remoteConnection{}, errors.New("remote connection URL must include a database name")
		}
		sslMode := u.Query().Get("sslmode")
		if engine == "mysql" && sslMode == "" {
			sslMode = u.Query().Get("ssl-mode")
		}
		return remoteConnection{
			Engine: engine, Host: host, Port: port, User: u.User.Username(),
			Password: password, Database: database, SSLMode: sslMode,
		}, nil
	}

	if at := strings.Index(raw, "@tcp("); at > 0 {
		credentials := raw[:at]
		rest := raw[at+5:]
		closeParen := strings.Index(rest, ")/")
		if closeParen > 0 {
			hostPort := rest[:closeParen]
			database := rest[closeParen+2:]
			if q := strings.IndexByte(database, '?'); q >= 0 {
				database = database[:q]
			}
			user, password, ok := strings.Cut(credentials, ":")
			if !ok || user == "" || database == "" {
				return remoteConnection{}, errors.New("invalid MySQL DSN")
			}
			host, port, err := net.SplitHostPort(hostPort)
			if err != nil {
				host = hostPort
				port = "3306"
			}
			return remoteConnection{
				Engine: "mysql", Host: host, Port: port, User: user,
				Password: password, Database: database,
			}, nil
		}
	}
	return remoteConnection{}, errors.New("supported formats: mysql://user:pass@host:3306/db, user:pass@tcp(host:3306)/db, postgres://user:pass@host:5432/db?sslmode=require")
}

func dumpRemoteCompressed(ctx context.Context, source remoteConnection, path string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	gz := gzip.NewWriter(file)

	var cmd *exec.Cmd
	switch source.Engine {
	case "mysql":
		if _, err := exec.LookPath("mysqldump"); err != nil {
			return errors.New("mysqldump is not installed")
		}
		args := []string{
			"--host=" + source.Host,
			"--port=" + source.Port,
			"--user=" + source.User,
			"--single-transaction",
			"--routines",
			"--triggers",
			"--events",
			"--hex-blob",
		}
		if source.SSLMode != "" {
			args = append(args, "--ssl-mode="+source.SSLMode)
		}
		args = append(args, source.Database)
		cmd = exec.CommandContext(ctx, "mysqldump", args...)
		cmd.Env = append(os.Environ(), "MYSQL_PWD="+source.Password)
	case "postgres":
		if _, err := exec.LookPath("pg_dump"); err != nil {
			return errors.New("pg_dump is not installed")
		}
		cmd = exec.CommandContext(ctx, "pg_dump", "--clean", "--if-exists", "--no-owner", "--no-privileges")
		cmd.Env = append(os.Environ(),
			"PGHOST="+source.Host,
			"PGPORT="+source.Port,
			"PGUSER="+source.User,
			"PGPASSWORD="+source.Password,
			"PGDATABASE="+source.Database,
		)
		if source.SSLMode != "" {
			cmd.Env = append(cmd.Env, "PGSSLMODE="+source.SSLMode)
		}
	default:
		return errors.New("unsupported remote database engine")
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	if _, err := io.Copy(gz, stdout); err != nil {
		_ = cmd.Process.Kill()
		_ = gz.Close()
		return fmt.Errorf("write remote dump: %w", err)
	}
	if err := cmd.Wait(); err != nil {
		_ = gz.Close()
		return fmt.Errorf("remote database dump failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return file.Sync()
}

func resetLocalDatabase(ctx context.Context, target Database) error {
	switch target.Engine {
	case "mysql":
		sql := fmt.Sprintf(
			"DROP DATABASE IF EXISTS %s; CREATE DATABASE %s CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci; GRANT ALL PRIVILEGES ON %s.* TO '%s'@'localhost'; FLUSH PRIVILEGES;",
			mysqlIdent(target.Name), mysqlIdent(target.Name), mysqlIdent(target.Name), sqlString(target.User),
		)
		return run(ctx, sql, "mysql", "--protocol=socket", "-uroot")
	case "postgres":
		terminate := fmt.Sprintf("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname='%s' AND pid <> pg_backend_pid();", sqlString(target.Name))
		if err := run(ctx, terminate, "runuser", "-u", "postgres", "--", "psql", "-v", "ON_ERROR_STOP=1", "-d", "postgres"); err != nil {
			return err
		}
		if err := run(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %s;", pgIdent(target.Name)), "runuser", "-u", "postgres", "--", "psql", "-v", "ON_ERROR_STOP=1", "-d", "postgres"); err != nil {
			return err
		}
		return run(ctx, fmt.Sprintf("CREATE DATABASE %s OWNER %s;", pgIdent(target.Name), pgIdent(target.User)), "runuser", "-u", "postgres", "--", "psql", "-v", "ON_ERROR_STOP=1", "-d", "postgres")
	default:
		return errors.New("unsupported target database engine")
	}
}

func restoreCompressedDump(ctx context.Context, target Database, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gz.Close()

	var cmd *exec.Cmd
	switch target.Engine {
	case "mysql":
		cmd = exec.CommandContext(ctx, "mysql", "--protocol=socket", "-uroot", target.Name)
	case "postgres":
		cmd = exec.CommandContext(ctx, "runuser", "-u", "postgres", "--", "psql", "-v", "ON_ERROR_STOP=1", "-d", target.Name)
	default:
		return errors.New("unsupported target database engine")
	}
	cmd.Stdin = gz
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("restore imported database: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func mysqlIdent(value string) string {
	q := string(rune(96))
	return q + strings.ReplaceAll(value, q, q+q) + q
}

func pgIdent(value string) string {
	return "\"" + strings.ReplaceAll(value, "\"", "\"\"") + "\""
}
