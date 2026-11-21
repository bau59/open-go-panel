package dbmanager

import (
    "compress/gzip"
    "context"
    "errors"
    "fmt"
    "io"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
    "time"

    "github.com/bau59/open-go-panel/internal/uploadlimits"
)

const (
    MaxSQLUploadBytes = uploadlimits.FileBytes
    MaxSQLExpandedBytes = uploadlimits.ExpandedBytes
)

func sqlUploadFormat(name string) (compressed bool, err error) {
    switch {
    case strings.HasSuffix(strings.ToLower(name), ".sql.gz"):
        return true, nil
    case strings.HasSuffix(strings.ToLower(name), ".sql"):
        return false, nil
    default:
        return false, errors.New("select a .sql or .sql.gz file")
    }
}

// stageSQLFile stores the upload outside the backup directory, with 0600
// permissions. The caller owns the file and removes it after processing.
func stageSQLFile(reader io.Reader) (path string, err error) {
    temp, err := os.CreateTemp("", "ogp-sql-upload-*")
    if err != nil { return "", fmt.Errorf("stage SQL upload: %w", err) }
    defer func() {
        if err != nil { _ = os.Remove(temp.Name()) }
    }()
    size, copyErr := io.Copy(temp, io.LimitReader(reader, MaxSQLUploadBytes+1))
    if copyErr == nil && (size == 0 || size > MaxSQLUploadBytes) {
        copyErr = errors.New("SQL upload must be between 1 byte and 1 GiB")
    }
    if copyErr == nil { copyErr = temp.Sync() }
    closeErr := temp.Close()
    if copyErr != nil { return "", copyErr }
    if closeErr != nil { return "", closeErr }
    return temp.Name(), nil
}

func validateSQLFile(path string, compressed bool) error {
    input, err := os.Open(path)
    if err != nil { return err }
    defer input.Close()
    if !compressed {
        info, err := input.Stat()
        if err != nil { return err }
        if info.Size() == 0 || info.Size() > MaxSQLUploadBytes {
            return errors.New("SQL file is empty or too large")
        }
        return nil
    }
    gz, err := gzip.NewReader(input)
    if err != nil { return fmt.Errorf("invalid compressed SQL: %w", err) }
    defer gz.Close()
    count, err := io.Copy(io.Discard, io.LimitReader(gz, MaxSQLExpandedBytes+1))
    if err != nil { return fmt.Errorf("invalid compressed SQL: %w", err) }
    if count == 0 || count > MaxSQLExpandedBytes {
        return errors.New("decompressed SQL must be between 1 byte and 4 GiB")
    }
    return nil
}

// StartFileImport executes a SQL script against a specific managed database.
// The default is to apply it to the existing database; replace explicitly
// resets the selected database first. Both modes create a safety snapshot.
func (m *Manager) StartFileImport(id int64, filename string, input io.Reader, replace bool) error {
    compressed, err := sqlUploadFormat(filename)
    if err != nil { return err }
    if !m.operationMu.TryLock() { return errOperationInProgress }
    accepted := false
    defer func() {
        if !accepted { m.operationMu.Unlock() }
    }()
    target, err := m.Get(id)
    if err != nil { return err }
    if target.Engine != "mysql" && target.Engine != "postgres" {
        return errors.New("SQL file import only supports MySQL and PostgreSQL")
    }
    path, err := stageSQLFile(input)
    if err != nil { return err }
    m.importMu.Lock()
    m.importTask = RemoteImportTask{
        DatabaseID: id, Engine: target.Engine, SourceHost: filepath.Base(filename),
        SourceKind: "file", Running: true, StartedAt: time.Now(),
    }
    m.importMu.Unlock()

    accepted = true
    go func() {
        defer m.operationMu.Unlock()
        defer os.Remove(path)
        ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
        defer cancel()
        backupPath, importErr := m.importFile(ctx, target, path, compressed, replace)
        m.importMu.Lock()
        m.importTask.Running = false
        m.importTask.FinishedAt = time.Now()
        m.importTask.BackupPath = backupPath
        if importErr != nil { m.importTask.Error = importErr.Error() } else { m.importTask.Error = "" }
        m.importMu.Unlock()
    }()
    return nil
}

func (m *Manager) importFile(ctx context.Context, target Database, path string, compressed, replace bool) (string, error) {
    if err := validateSQLFile(path, compressed); err != nil {
        return "", err
    }
    safety, err := m.backupUnlocked(ctx, target.ID)
    if err != nil { return "", fmt.Errorf("cannot import without a safety backup: %w", err) }

    if replace {
        if err := resetLocalDatabase(ctx, target); err != nil {
            return safety.Path, m.recoverFileImport(target, safety.Path, fmt.Errorf("reset target: %w", err))
        }
    }
    if err := executeSQLFile(ctx, target, path, compressed); err != nil {
        return safety.Path, m.recoverFileImport(target, safety.Path, err)
    }
    return safety.Path, nil
}

func (m *Manager) recoverFileImport(target Database, backupPath string, cause error) error {
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
    defer cancel()
    if err := m.resetAndRestoreFromSafety(ctx, target, backupPath); err != nil {
        return fmt.Errorf("SQL file import failed: %w; automatic recovery also failed: %v; backup at %s", cause, err, backupPath)
    }
    return fmt.Errorf("SQL file import failed: %w; target recovered from safety backup %s", cause, backupPath)
}

// SQL execution uses the managed database user rather than the server-level
// root/postgres database account. A dump cannot access other databases unless
// its managed user already has permissions there.
func sqlFileCommand(ctx context.Context, target Database) (*exec.Cmd, error) {
    var cmd *exec.Cmd
    switch target.Engine {
    case "mysql":
        // MySQL's binary mode disables client-side shell/source commands in
        // noninteractive SQL input. Run with an unprivileged Linux identity
        // even if the uploaded script contains other client directives.
        cmd = exec.CommandContext(ctx, "runuser", "-u", "nobody", "--",
            "mysql", "--binary-mode", "--protocol=socket", "-u", target.User, "--database="+target.Name)
        cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "MYSQL_PWD="+target.Password}
    case "postgres":
        // psql supports client-side metacommands such as \\!; never execute
        // uploaded SQL with panel's root OS privileges.
        cmd = exec.CommandContext(ctx, "runuser", "-u", "nobody", "--",
            "psql", "-X", "-h", "127.0.0.1", "-U", target.User,
            "-d", target.Name, "-v", "ON_ERROR_STOP=1")
        cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "PGPASSWORD="+target.Password}
    default:
        return nil, errors.New("unsupported database engine")
    }
    return cmd, nil
}

type cappedSQLStderr struct { text strings.Builder }
func (w *cappedSQLStderr) Write(p []byte) (int, error) {
    total := len(p)
    if w.text.Len() < 16<<10 {
        limit := (16 << 10) - w.text.Len()
        if len(p) > limit { p = p[:limit] }
        _, _ = w.text.Write(p)
    }
    return total, nil
}

func executeSQLFile(ctx context.Context, target Database, path string, compressed bool) error {
    file, err := os.Open(path)
    if err != nil { return err }
    defer file.Close()
    var source io.Reader = file
    if compressed {
        gz, err := gzip.NewReader(file)
        if err != nil { return err }
        defer gz.Close()
        source = io.LimitReader(gz, MaxSQLExpandedBytes+1)
    }
    cmd, err := sqlFileCommand(ctx, target)
    if err != nil { return err }
    cmd.Stdin = source
    cmd.Stdout = io.Discard
    stderr := new(cappedSQLStderr)
    cmd.Stderr = stderr
    if err := cmd.Run(); err != nil {
        return fmt.Errorf("execute SQL file: %w: %s", err, strings.TrimSpace(stderr.text.String()))
    }
    return nil
}
