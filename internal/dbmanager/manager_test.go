package dbmanager

import (
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"errors"
	"testing"
)

func TestBackupPruneCandidates(t *testing.T) {
	backups := []Backup{{Path: "new"}, {Path: "middle"}, {Path: "old"}}
	tests := []struct {
		name  string
		items []Backup
		keep  int
		want  int
	}{
		{name: "empty", keep: 7},
		{name: "fewer than retention", items: backups[:1], keep: 7},
		{name: "equal to retention", items: backups, keep: 3},
		{name: "prune old copies", items: backups, keep: 2, want: 1},
		{name: "keep one", items: backups, keep: 1, want: 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := backupPruneCandidates(tc.items, tc.keep)
			if len(got) != tc.want {
				t.Fatalf("candidates = %d, want %d", len(got), tc.want)
			}
		})
	}
}

func TestDatabaseOperationsRejectOverlap(t *testing.T) {
	m := &Manager{}
	m.operationMu.Lock()
	defer m.operationMu.Unlock()

	tests := []struct {
		name string
		run  func() error
	}{
		{name: "backup", run: func() error { _, err := m.Backup(context.Background(), 1); return err }},
		{name: "restore", run: func() error { return m.Restore(context.Background(), 1, "/tmp/example.sql.gz") }},
		{name: "prune", run: func() error { return m.PruneBackups(1, 7) }},
		{name: "delete", run: func() error { return m.Delete(context.Background(), 1) }},
		{name: "import", run: func() error { return m.StartRemoteImport(1, "mysql://user:pass@example.com:3306/db") }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, errOperationInProgress) {
				t.Fatalf("expected operation conflict, got %v", err)
			}
		})
	}
}

func TestWriteGzipBackupPublishesOnlyCompletedDump(t *testing.T) {
	dir := t.TempDir()
	want := "CREATE TABLE items (id integer);\n"
	path, err := writeGzipBackup(dir, "success", func(w io.Writer) error {
		_, err := io.WriteString(w, want)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "success.sql.gz" {
		t.Fatalf("unexpected backup path: %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	data, err := io.ReadAll(gz)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("backup content = %q, want %q", data, want)
	}
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("backup permissions = %o", info.Mode().Perm())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected only final backup, found %d files", len(entries))
	}
}

func TestWriteGzipBackupRemovesFailedDump(t *testing.T) {
	dir := t.TempDir()
	sentinel := errors.New("dump command failed")
	_, err := writeGzipBackup(dir, "broken", func(w io.Writer) error {
		_, _ = io.WriteString(w, strings.Repeat("incomplete", 100))
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected dump failure, got %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("unfinished backup left %d files", len(entries))
	}
}

func TestValidateCompressedBackup(t *testing.T) {
	dir := t.TempDir()
	valid, err := writeGzipBackup(dir, "valid", func(w io.Writer) error {
		_, err := io.WriteString(w, "CREATE TABLE messages (id INTEGER);\n")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := validateCompressedBackup(valid); err != nil {
		t.Fatalf("valid backup rejected: %v", err)
	}

	bytes, err := os.ReadFile(valid)
	if err != nil {
		t.Fatal(err)
	}
	check := func(name string, payload []byte) {
		t.Helper()
		path := filepath.Join(dir, name+".sql.gz")
		if err := os.WriteFile(path, payload, 0600); err != nil {
			t.Fatal(err)
		}
		if err := validateCompressedBackup(path); err == nil {
			t.Fatalf("%s: invalid archive accepted", name)
		}
	}

	check("truncated", bytes[:len(bytes)-6])
	corrupted := append([]byte(nil), bytes...)
	corrupted[len(corrupted)-5] ^= 0xff
	check("checksum", corrupted)
	check("not-gzip", []byte("plain text"))

	empty, err := writeGzipBackup(dir, "empty", func(io.Writer) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := validateCompressedBackup(empty); err == nil {
		t.Fatal("empty gzip backup accepted")
	}
}
