package phpfpm

import (
    "bytes"
    "errors"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "strconv"
    "strings"
)

const (
    phpErrorLogDir = "/var/log/open-go-panel/php"
    phpLogrotateConfig = "/etc/logrotate.d/open-go-panel-php"
    phpLogReadMaxBytes = 256 << 10
    phpLogReadMaxLines = 200
)

// ErrorLogPath is derived from the numeric application ID, never a user path.
func ErrorLogPath(id int64) string {
    return filepath.Join(phpErrorLogDir, "app-"+strconv.FormatInt(id, 10)+".log")
}

func ensureErrorLog(id int64, uid, gid int) error {
    if id <= 0 || uid < 0 || gid < 0 {
        return errors.New("invalid PHP error log owner or application ID")
    }
    if err := os.MkdirAll(phpErrorLogDir, 0755); err != nil {
        return fmt.Errorf("create PHP log directory: %w", err)
    }
    dir, err := os.Lstat(phpErrorLogDir)
    if err != nil { return err }
    if !dir.IsDir() || dir.Mode().Perm()&0022 != 0 {
        return errors.New("PHP log directory is not a safe root-managed directory")
    }
    path := ErrorLogPath(id)
    if stat, err := os.Lstat(path); err == nil {
        if !stat.Mode().IsRegular() {
            return errors.New("PHP error log must be a regular file")
        }
    } else if !errors.Is(err, os.ErrNotExist) {
        return err
    }
    file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
    if err != nil { return fmt.Errorf("open PHP error log: %w", err) }
    if err := file.Close(); err != nil { return err }
    if err := os.Chown(path, uid, gid); err != nil { return fmt.Errorf("own PHP error log: %w", err) }
    if err := os.Chmod(path, 0600); err != nil { return fmt.Errorf("protect PHP error log: %w", err) }
    return ensurePHPLogRotation()
}

// copytruncate preserves open file descriptors held by active PHP workers.
func ensurePHPLogRotation() error {
    policy := strings.Join([]string{
        "/var/log/open-go-panel/php/*.log {",
        "    daily",
        "    maxsize 5M",
        "    rotate 7",
        "    compress",
        "    delaycompress",
        "    missingok",
        "    notifempty",
        "    copytruncate",
        "    su root root",
        "}",
        "",
    }, "\n")
    file, err := os.OpenFile(phpLogrotateConfig, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
    if errors.Is(err, os.ErrExist) { return nil } // Preserve admin overrides.
    if err != nil { return fmt.Errorf("configure PHP log rotation: %w", err) }
    if _, err := io.WriteString(file, policy); err != nil {
        _ = file.Close()
        _ = os.Remove(phpLogrotateConfig)
        return err
    }
    if err := file.Close(); err != nil { return err }
    return nil
}

// ErrorLoggingConfigured detects whether the existing pool has site-specific
// PHP error logging; reading a log must never mutate pool configuration.
func ErrorLoggingConfigured(id int64) bool {
    if id <= 0 { return false }
    found, err := runtimes()
    if err != nil { return false }
    needle := "\nphp_admin_value[error_log] = " + ErrorLogPath(id) + "\n"
    for _, php := range found {
        data, err := os.ReadFile(poolPath(php, id))
        if err == nil && strings.Contains("\n"+strings.TrimSpace(string(data))+"\n", needle) {
            return true
        }
    }
    return false
}

func ReadErrorLog(id int64) (string, error) {
    if id <= 0 { return "", errors.New("invalid PHP application ID") }
    text, err := readErrorLogFile(ErrorLogPath(id))
    if errors.Is(err, os.ErrNotExist) { return "", nil }
    return text, err
}

// readErrorLogFile returns at most 200 recent lines from a 256-KiB file suffix.
// This works without background collectors, database writes, or shell processes.
func readErrorLogFile(path string) (string, error) {
    file, err := os.Open(path)
    if err != nil { return "", err }
    defer file.Close()
    info, err := file.Stat()
    if err != nil { return "", err }
    if !info.Mode().IsRegular() {
        return "", errors.New("PHP error log must be a regular file")
    }
    if info.Size() == 0 { return "", nil }
    start := info.Size() - phpLogReadMaxBytes
    if start < 0 { start = 0 }
    buf := make([]byte, info.Size()-start)
    n, err := file.ReadAt(buf, start)
    if err != nil && !errors.Is(err, io.EOF) { return "", err }
    buf = buf[:n]
    if start > 0 {
        if nl := bytes.IndexByte(buf, '\n'); nl >= 0 {
            buf = buf[nl+1:]
        } else {
            return "", nil
        }
    }
    lines := strings.Split(strings.TrimRight(string(buf), "\r\n"), "\n")
    if len(lines) > phpLogReadMaxLines {
        lines = lines[len(lines)-phpLogReadMaxLines:]
    }
    return strings.Join(lines, "\n"), nil
}
