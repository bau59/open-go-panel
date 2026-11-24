package dbmanager

import (
    "context"
    "os"
    "path/filepath"
    "strings"
    "testing"
    "time"

    "github.com/bau59/open-go-panel/internal/state"
)

func TestOffsiteConnectionAndIndependentSchedules(t *testing.T) {
    store, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
    if err != nil { t.Fatal(err) }
    defer store.Close()
    manager := New(store, "")
    manager.offsiteConfigFile = filepath.Join(t.TempDir(), "secrets", "db-rclone.conf")

    bin := t.TempDir()
    if err := os.WriteFile(filepath.Join(bin, "rclone"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil { t.Fatal(err) }
    t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

    if err := manager.SetBackupSchedule(true, 2, 5); err != nil { t.Fatal(err) }
    if err := manager.SaveOffsiteConnection(OffsiteConnection{
        Provider: "r2", Destination: "mybucket/sql", Endpoint: "https://abc.r2.cloudflarestorage.com",
        AccessKey: "ACCESS", SecretKey: "SECRET",
    }); err != nil { t.Fatal(err) }
    if got := manager.BackupRemote(); got != "ogpdb:mybucket/sql" { t.Fatalf("remote=%q", got) }
    raw, err := os.ReadFile(manager.offsiteConfigFile)
    if err != nil { t.Fatal(err) }
    if !strings.Contains(string(raw), "secret_access_key = SECRET") { t.Fatal("secret missing from root-only config") }
    if info, err := os.Stat(manager.offsiteConfigFile); err != nil || info.Mode().Perm() != 0600 {
        t.Fatalf("config permissions %v: %v", info, err)
    }
    // Root-only secrets must not appear in SQLite values.
    for _, key := range []string{"db.offsite_provider", "db.offsite_endpoint", backupRemoteSetting} {
        value, _, err := store.Setting(key)
        if err != nil { t.Fatal(err) }
        if strings.Contains(value, "SECRET") { t.Fatalf("secret leaked into %s", key) }
    }
    if err := manager.SetOffsiteSchedule(true, 4); err != nil { t.Fatal(err) }
    if !manager.OffsiteSchedule().Enabled || manager.OffsiteSchedule().HourUTC != 4 {
        t.Fatal("off-site schedule not persisted")
    }
    // Editing destination without credentials retains existing root-only keys.
    if err := manager.SaveOffsiteConnection(OffsiteConnection{
        Provider: "r2", Destination: "mybucket/changed", Endpoint: "https://abc.r2.cloudflarestorage.com",
    }); err != nil { t.Fatal(err) }
    updated, err := os.ReadFile(manager.offsiteConfigFile)
    if err != nil { t.Fatal(err) }
    if !strings.Contains(string(updated), "secret_access_key = SECRET") { t.Fatal("saved secret lost on edit") }
    if err := manager.SaveOffsiteConnection(OffsiteConnection{Provider:"existing", Destination:""}); err != nil { t.Fatal(err) }
    if manager.OffsiteSchedule().Enabled { t.Fatal("external schedule remained enabled after clearing destination") }
    if !manager.BackupSchedule().Enabled || manager.BackupSchedule().Keep != 5 {
        t.Fatal("editing external storage changed local retention")
    }
}

func TestBackupTaskRunsWithoutHTTPRequestDeadline(t *testing.T) {
    manager := &Manager{}
    release := make(chan struct{})
    if err := manager.startBackupTask("local", "test", func(context.Context) error {
        <-release
        return nil
    }); err != nil { t.Fatal(err) }
    if task := manager.CurrentBackupTask(); task.State != "running" { t.Fatalf("state=%s", task.State) }
    if err := manager.startBackupTask("offsite", "duplicate", func(context.Context) error { return nil }); err == nil {
        t.Fatal("overlapping backup job accepted")
    }
    close(release)
    deadline := time.After(2 * time.Second)
    for {
        if task := manager.CurrentBackupTask(); task.State == "success" { break }
        select { case <-deadline: t.Fatal("background job did not finish"); default: time.Sleep(time.Millisecond) }
    }
}
