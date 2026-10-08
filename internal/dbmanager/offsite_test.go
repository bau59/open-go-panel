package dbmanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bau59/open-go-panel/internal/state"
)

func TestValidateBackupRemote(t *testing.T) {
	for _, tc := range []struct {
		target string
		valid  bool
	}{
		{"", true},
		{"s3:bucket/panel", true},
		{"b2:backups/example/db", true},
		{"sftp:folder", true},
		{"s3:", false},
		{"s3:/root", false},
		{"-x:bucket", false},
		{"s3:bucket/../escape", false},
		{"s3:bucket//invalid", false},
		{"s3:bucket with spaces", false},
	} {
		err := validateBackupRemote(tc.target)
		if (err == nil) != tc.valid {
			t.Errorf("validateBackupRemote(%q): error %v, want valid %v", tc.target, err, tc.valid)
		}
	}
}

func TestUploadBackupNoRemoteAndFailedTransfer(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	m := New(store, "")
	backup := Backup{Engine: "mysql", Database: "demo", Path: "/nonexistent/backup.sql.gz"}
	if err := m.UploadBackup(context.Background(), backup); err != nil {
		t.Fatalf("remote disabled: %v", err)
	}

	if err := store.SetSetting(backupRemoteSetting, "s3:backup-bucket/panel"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	executable := filepath.Join(dir, "rclone")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\necho simulated transfer failure >&2\nexit 13\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	err = m.UploadBackup(context.Background(), backup)
	if err == nil || !strings.Contains(err.Error(), "local copy preserved") {
		t.Fatalf("expected safe transfer failure, got: %v", err)
	}
}

func TestBackupRemotePath(t *testing.T) {
	backup := Backup{Engine: "postgres", Database: "app_db", Path: "/tmp/20261008T020000Z.sql.gz"}
	got := backupRemotePath("b2:bucket/open-go-panel", backup)
	want := "b2:bucket/open-go-panel/postgres/app_db/20261008T020000Z.sql.gz"
	if got != want {
		t.Fatalf("remote path = %q, want %q", got, want)
	}
}

var _ = errors.Is
