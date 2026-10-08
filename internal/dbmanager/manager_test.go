package dbmanager

import (
	"context"
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
