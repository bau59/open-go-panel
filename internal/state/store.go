package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

type AuditEntry struct {
	ID        int64
	CreatedAt string
	Action    string
	Target    string
	Details   string
}


func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite state: %w", err)
	}
	db.SetMaxOpenConns(1)

	ctx := context.Background()
	for _, pragma := range []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA busy_timeout = 5000",
	} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("configure sqlite state: %w", err)
		}
	}

	store := &Store{db: db}
	if err := store.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("chmod sqlite state: %w", err)
	}
	return store, nil
}

func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) migrate(ctx context.Context) error {
	const schema = `
CREATE TABLE IF NOT EXISTS apps (
	id INTEGER PRIMARY KEY,
	owner TEXT NOT NULL,
	name TEXT NOT NULL,
	type TEXT NOT NULL,
	root TEXT NOT NULL,
	port INTEGER NOT NULL DEFAULT 0,
	command TEXT NOT NULL DEFAULT '',
	service_json TEXT NOT NULL DEFAULT '{}',
	created_at TEXT NOT NULL,
	UNIQUE(owner, name)
);

CREATE TABLE IF NOT EXISTS databases (
	id INTEGER PRIMARY KEY,
	engine TEXT NOT NULL,
	name TEXT NOT NULL,
	username TEXT NOT NULL,
	password TEXT NOT NULL,
	created_at TEXT NOT NULL,
	UNIQUE(engine, name)
);

CREATE TABLE IF NOT EXISTS domains (
	app_id INTEGER PRIMARY KEY,
	domain TEXT NOT NULL UNIQUE,
	port INTEGER NOT NULL,
	root TEXT NOT NULL DEFAULT '',
	kind TEXT NOT NULL DEFAULT 'proxy',
	template TEXT NOT NULL DEFAULT '',
	FOREIGN KEY(app_id) REFERENCES apps(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS standalone_domains (
	id INTEGER PRIMARY KEY,
	domain TEXT NOT NULL UNIQUE,
	port INTEGER NOT NULL DEFAULT 0,
	root TEXT NOT NULL DEFAULT '',
	kind TEXT NOT NULL DEFAULT 'parked',
	template TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS app_databases (
	app_id INTEGER NOT NULL,
	database_id INTEGER NOT NULL,
	env_name TEXT NOT NULL,
	created_at TEXT NOT NULL,
	PRIMARY KEY(app_id, env_name),
	FOREIGN KEY(app_id) REFERENCES apps(id) ON DELETE CASCADE,
	FOREIGN KEY(database_id) REFERENCES databases(id) ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_app_databases_database_id
	ON app_databases(database_id);

CREATE TABLE IF NOT EXISTS deployments (
	app_id INTEGER PRIMARY KEY,
	repository TEXT NOT NULL DEFAULT '',
	branch TEXT NOT NULL DEFAULT 'main',
	current_commit TEXT NOT NULL DEFAULT '',
	previous_commit TEXT NOT NULL DEFAULT '',
	deployed_at TEXT NOT NULL DEFAULT '',
	FOREIGN KEY(app_id) REFERENCES apps(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS settings (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS audit_log (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	created_at TEXT NOT NULL,
	action TEXT NOT NULL,
	target TEXT NOT NULL DEFAULT '',
	details TEXT NOT NULL DEFAULT ''
);
`
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("migrate sqlite state: %w", err)
	}
	for _, statement := range []string{
		"ALTER TABLE domains ADD COLUMN root TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE domains ADD COLUMN kind TEXT NOT NULL DEFAULT 'proxy'",
		"ALTER TABLE deployments ADD COLUMN auto_deploy INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE deployments ADD COLUMN auto_deploy_interval_sec INTEGER NOT NULL DEFAULT 300",
		"ALTER TABLE deployments ADD COLUMN last_checked_at TEXT NOT NULL DEFAULT ''",
	} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			return fmt.Errorf("migrate sqlite state: %w", err)
		}
	}
	return nil
}

func (s *Store) Setting(key string) (string, bool, error) {
	var value string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`
		INSERT INTO settings(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value
	`, key, value)
	return err
}

func (s *Store) Audit(ctx context.Context, action, target, details string) {
	if s == nil || s.db == nil {
		return
	}
	_, _ = s.db.ExecContext(
		ctx,
		`INSERT INTO audit_log(created_at, action, target, details)
		 VALUES(strftime('%Y-%m-%dT%H:%M:%fZ','now'), ?, ?, ?)`,
		action, target, details,
	)
}

func (s *Store) RecentAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, created_at, action, target, details
		FROM audit_log
		ORDER BY id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []AuditEntry
	for rows.Next() {
		var entry AuditEntry
		if err := rows.Scan(&entry.ID, &entry.CreatedAt, &entry.Action, &entry.Target, &entry.Details); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}
