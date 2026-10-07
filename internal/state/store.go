package state

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
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
	template TEXT NOT NULL DEFAULT '',
	FOREIGN KEY(app_id) REFERENCES apps(id) ON DELETE CASCADE
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
	return nil
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
