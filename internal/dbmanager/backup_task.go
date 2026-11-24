package dbmanager

import (
    "context"
    "errors"
    "fmt"
    "time"
)

// BackupTask reports progress of the one active manual database backup/export.
// Long operations run independently of the browser's HTTP connection.
type BackupTask struct {
    Kind string
    State string
    Description string
    StartedAt time.Time
    FinishedAt time.Time
    Error string
}

func (m *Manager) CurrentBackupTask() BackupTask {
    m.backupTaskMu.Lock()
    defer m.backupTaskMu.Unlock()
    return m.backupTask
}

// SetBackupWorkerContext allows application shutdown to cancel running tasks.
// Without an explicit service context, jobs are still detached from HTTP.
func (m *Manager) SetBackupWorkerContext(ctx context.Context) {
    if ctx == nil { return }
    m.backupTaskMu.Lock()
    m.backupWorkerCtx = ctx
    m.backupTaskMu.Unlock()
}

func (m *Manager) startBackupTask(kind, description string, fn func(context.Context) error) error {
    m.backupTaskMu.Lock()
    if m.backupTask.State == "running" {
        m.backupTaskMu.Unlock()
        return errors.New("another database backup/export task is already running")
    }
    ctx := m.backupWorkerCtx
    if ctx == nil { ctx = context.Background() }
    if err := ctx.Err(); err != nil { m.backupTaskMu.Unlock(); return err }
    m.backupTask = BackupTask{Kind: kind, State: "running", Description: description, StartedAt: time.Now().UTC()}
    m.backupTaskMu.Unlock()

    go func() {
        err := fn(ctx)
        m.backupTaskMu.Lock()
        m.backupTask.FinishedAt = time.Now().UTC()
        if err != nil {
            m.backupTask.State = "failed"
            m.backupTask.Error = err.Error()
        } else {
            m.backupTask.State = "success"
        }
        m.backupTaskMu.Unlock()
    }()
    return nil
}

func (m *Manager) StartLocalBackup(id int64, keep int) error {
    database, err := m.Get(id)
    if err != nil { return err }
    if keep < 1 || keep > 100 { return errors.New("retention must be between 1 and 100 copies") }
    return m.startBackupTask("local", "Local backup: "+database.Name, func(ctx context.Context) error {
        if _, err := m.Backup(ctx, id); err != nil { return err }
        return m.PruneBackups(id, keep)
    })
}

func (m *Manager) StartOffsiteUploadLatest() error {
    if m.BackupRemote() == "" { return errors.New("configure off-site storage first") }
    return m.startBackupTask("offsite", "Upload latest local backups", func(ctx context.Context) error {
        if err := m.UploadLatestAll(ctx); err != nil { return err }
        return m.SetOffsiteCompletedAt(time.Now())
    })
}

func (m *Manager) StartOffsiteUploadOne(id int64, backupPath string) error {
    if m.BackupRemote() == "" { return errors.New("configure off-site storage first") }
    database, err := m.Get(id)
    if err != nil { return err }
    if _, err := m.BackupFile(id, backupPath); err != nil { return err }
    return m.startBackupTask("offsite", fmt.Sprintf("Upload %s snapshot", database.Name), func(ctx context.Context) error {
        if err := m.UploadOneBackup(ctx, id, backupPath); err != nil { return err }
        return m.SetOffsiteCompletedAt(time.Now())
    })
}

func (m *Manager) StartOffsiteConnectionTest() error {
    if m.BackupRemote() == "" { return errors.New("configure off-site storage first") }
    return m.startBackupTask("connection", "Check external storage connection", func(ctx context.Context) error {
        // The test enumerates a directory, not a multi-GB transfer.
        checkCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
        defer cancel()
        return m.TestOffsiteConnection(checkCtx)
    })
}
