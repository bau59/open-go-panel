package dbmanager

import (
    "context"
    "errors"
    "fmt"
    "net"
    "net/url"
    "os"
    "os/exec"
    "path/filepath"
    "regexp"
    "strconv"
    "strings"
    "time"
)

const managedOffsiteRemote = "ogpdb"

type OffsiteConnection struct {
    Provider string
    Destination string
    Endpoint string
    Region string
    AccessKey string
    SecretKey string
    Host string
    Port int
    User string
    KeyFile string
    KnownHostsFile string
}

type OffsiteSchedule struct {
    Enabled bool
    HourUTC int
}

var safeOffsiteValue = regexp.MustCompile(`^[a-zA-Z0-9._@/-]+$`)

func (m *Manager) OffsiteConnection() OffsiteConnection {
    get := func(key string) string {
        value, _, _ := m.store.Setting(key)
        return value
    }
    provider := get("db.offsite_provider")
    if provider == "" { provider = "existing" }
    destination := m.BackupRemote()
    if provider != "existing" {
        destination = strings.TrimPrefix(destination, managedOffsiteRemote+":")
    }
    port, _ := strconv.Atoi(get("db.offsite_port"))
    if port == 0 { port = 22 }
    return OffsiteConnection{
        Provider: provider, Destination: destination,
        Endpoint: get("db.offsite_endpoint"), Region: get("db.offsite_region"),
        Host: get("db.offsite_host"), Port: port,
        User: get("db.offsite_user"), KeyFile: get("db.offsite_key_file"),
        KnownHostsFile: get("db.offsite_known_hosts"),
    }
}

func (m *Manager) OffsiteSchedule() OffsiteSchedule {
    result := OffsiteSchedule{HourUTC: 3}
    value, exists, _ := m.store.Setting("db.offsite_enabled")
    result.Enabled = value == "1"
    if !exists {
        // Preserve previously configured automatic remote backups on upgrade.
        // Newly created connections still require explicit schedule opt-in.
        _, migrated, _ := m.store.Setting("db.offsite_provider")
        if !migrated && m.BackupRemote() != "" { result.Enabled = true }
    }
    if value, _, _ := m.store.Setting("db.offsite_hour_utc"); value != "" {
        if hour, err := strconv.Atoi(value); err == nil && hour >= 0 && hour <= 23 {
            result.HourUTC = hour
        }
    }
    return result
}

func (m *Manager) SetOffsiteSchedule(enabled bool, hourUTC int) error {
    if hourUTC < 0 || hourUTC > 23 { return errors.New("off-site upload hour must be 0 to 23 UTC") }
    if enabled && m.BackupRemote() == "" {
        return errors.New("configure an external destination before enabling automatic export")
    }
    tx, err := m.store.DB().Begin()
    if err != nil { return err }
    defer tx.Rollback()
    flag := "0"
    if enabled { flag = "1" }
    for key, value := range map[string]string{"db.offsite_enabled": flag, "db.offsite_hour_utc": strconv.Itoa(hourUTC)} {
        if _, err := tx.Exec(`INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value); err != nil { return err }
    }
    return tx.Commit()
}

func (m *Manager) RunScheduledOffsiteIfDue(ctx context.Context, now time.Time) (bool, error) {
    schedule := m.OffsiteSchedule()
    if !schedule.Enabled { return false, nil }
    now = now.UTC()
    if now.Hour() < schedule.HourUTC { return false, nil }
    today := now.Format("2006-01-02")
    if last, _, err := m.store.Setting("db.offsite_last_date"); err == nil && last == today {
        return false, nil
    }
    if err := m.runScheduledTask(ctx, "offsite", "Scheduled external SQL backup upload", func(ctx context.Context) error {
        return m.UploadLatestAll(ctx)
    }); err != nil { return false, err }
    if err := m.SetOffsiteCompletedAt(time.Now()); err != nil { return true, err }
    if err := m.store.SetSetting("db.offsite_last_date", today); err != nil { return true, err }
    return true, nil
}

// SaveOffsiteConnection stores credentials only in a root-owned rclone config.
// SQLite contains destination, provider and non-secret connection metadata.
func (m *Manager) SaveOffsiteConnection(conn OffsiteConnection) error {
    if !m.operationMu.TryLock() { return errOperationInProgress }
    defer m.operationMu.Unlock()
    conn.Provider = strings.ToLower(strings.TrimSpace(conn.Provider))
    conn.Destination = strings.TrimSpace(conn.Destination)
    conn.Endpoint = strings.TrimSpace(conn.Endpoint)
    conn.Region = strings.TrimSpace(conn.Region)
    conn.Host = strings.TrimSpace(conn.Host)
    conn.User = strings.TrimSpace(conn.User)
    conn.KeyFile = strings.TrimSpace(conn.KeyFile)
    conn.KnownHostsFile = strings.TrimSpace(conn.KnownHostsFile)
    if conn.Destination != "" {
        if _, err := exec.LookPath("rclone"); err != nil {
            return errors.New("rclone is not installed; install it before configuring off-site storage")
        }
    }

    remote := conn.Destination
    config := ""
    if conn.Provider == "existing" {
        if err := validateBackupRemote(remote); err != nil { return err }
    } else {
        if !safeOffsiteValue.MatchString(conn.Destination) ||
            strings.HasPrefix(conn.Destination, "/") ||
            strings.HasSuffix(conn.Destination, "/") ||
            strings.Contains(conn.Destination, ":") ||
            strings.Contains(conn.Destination, "//") {
            return errors.New("destination must be a bucket/path (or folder/path for SFTP)")
        }
        for _, part := range strings.Split(conn.Destination, "/") {
            if part == "." || part == ".." { return errors.New("destination path cannot contain . or ..") }
        }
        remote = managedOffsiteRemote + ":" + conn.Destination
        switch conn.Provider {
        case "s3", "r2", "b2":
            // Leaving credentials blank retains saved root-only keys for the
            // same provider. Secrets never round-trip through the HTML form.
            if conn.Provider == m.OffsiteConnection().Provider &&
                (conn.AccessKey == "" || conn.SecretKey == "") {
                access, secret := m.savedS3Credentials()
                if conn.AccessKey == "" { conn.AccessKey = access }
                if conn.SecretKey == "" { conn.SecretKey = secret }
            }
            if conn.AccessKey == "" || conn.SecretKey == "" ||
                strings.ContainsAny(conn.AccessKey, "\r\n") || strings.ContainsAny(conn.SecretKey, "\r\n") {
                return errors.New("S3-compatible storage requires an access key and secret key")
            }
            provider := "AWS"
            if conn.Provider == "r2" { provider = "Cloudflare" }
            if conn.Provider == "b2" { provider = "Backblaze" }
            if conn.Provider != "s3" && conn.Endpoint == "" {
                return errors.New("R2 and B2 require the HTTPS S3 API endpoint")
            }
            if conn.Endpoint != "" {
                parsed, err := url.Parse(conn.Endpoint)
                if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil ||
                    parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
                    return errors.New("storage endpoint must be a plain HTTPS URL")
                }
            }
            if strings.ContainsAny(conn.Region, "\r\n=[]") { return errors.New("invalid storage region") }
            if conn.Provider == "r2" && conn.Region == "" { conn.Region = "auto" }
            config = "[" + managedOffsiteRemote + "]\ntype = s3\nprovider = " + provider +
                "\nenv_auth = false\naccess_key_id = " + conn.AccessKey +
                "\nsecret_access_key = " + conn.SecretKey + "\n"
            if conn.Endpoint != "" { config += "endpoint = " + conn.Endpoint + "\n" }
            if conn.Region != "" { config += "region = " + conn.Region + "\n" }
        case "sftp":
            if conn.Host == "" || strings.ContainsAny(conn.Host, "\r\n=[] ") || net.ParseIP(conn.Host) == nil && !validSFTPHost(conn.Host) {
                return errors.New("invalid SFTP host")
            }
            if conn.User == "" || !safeOffsiteValue.MatchString(conn.User) { return errors.New("invalid SFTP username") }
            if conn.Port <= 0 || conn.Port > 65535 { return errors.New("SFTP port must be 1 to 65535") }
            for _, field := range []string{conn.KeyFile, conn.KnownHostsFile} {
                if !filepath.IsAbs(field) || strings.ContainsAny(field, "\r\n=[]") {
                    return errors.New("SFTP requires absolute SSH key and known_hosts file paths")
                }
                info, err := os.Stat(field)
                if err != nil || !info.Mode().IsRegular() {
                    return fmt.Errorf("SFTP key or known_hosts file is unavailable: %s", field)
                }
            }
            config = fmt.Sprintf("[%s]\ntype = sftp\nhost = %s\nuser = %s\nport = %d\nkey_file = %s\nknown_hosts_file = %s\n",
                managedOffsiteRemote, conn.Host, conn.User, conn.Port, conn.KeyFile, conn.KnownHostsFile)
        default:
            return errors.New("supported destinations: existing rclone, S3, R2, B2 (S3 API), SFTP")
        }
    }

    // Only managed providers use the separate private config. The legacy
    // external rclone config remains supported unchanged.
    if config != "" {
        if err := savePrivateRcloneConfig(m.offsiteConfigFile, config); err != nil { return err }
    }
    values := map[string]string{
        backupRemoteSetting: remote,
        "db.offsite_provider": conn.Provider,
        "db.offsite_endpoint": conn.Endpoint,
        "db.offsite_region": conn.Region,
        "db.offsite_host": conn.Host,
        "db.offsite_port": strconv.Itoa(conn.Port),
        "db.offsite_user": conn.User,
        "db.offsite_key_file": conn.KeyFile,
        "db.offsite_known_hosts": conn.KnownHostsFile,
    }
    if remote == "" { values["db.offsite_enabled"] = "0" }
    tx, err := m.store.DB().Begin()
    if err != nil { return err }
    defer tx.Rollback()
    for key, value := range values {
        if _, err := tx.Exec(`INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value); err != nil { return err }
    }
    if err := tx.Commit(); err != nil { return err }
    if conn.Provider == "existing" && m.offsiteConfigFile != "" {
        _ = os.Remove(m.offsiteConfigFile)
    }
    return nil
}

func validSFTPHost(host string) bool {
    if len(host) > 253 { return false }
    for _, part := range strings.Split(host, ".") {
        if part == "" || len(part) > 63 { return false }
        for _, c := range part {
            if c != '-' && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
                return false
            }
        }
    }
    return true
}

func savePrivateRcloneConfig(path, data string) error {
    if path == "" { return errors.New("missing private rclone configuration path") }
    dir := filepath.Dir(path)
    if err := os.MkdirAll(dir, 0700); err != nil { return fmt.Errorf("create rclone config directory: %w", err) }
    // Respect existing directories but never weaken them.
    info, err := os.Lstat(dir)
    if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
        return errors.New("invalid private rclone config directory")
    }
    file, err := os.CreateTemp(dir, ".db-rclone-*.tmp")
    if err != nil { return err }
    defer os.Remove(file.Name())
    defer file.Close()
    if err := file.Chmod(0600); err != nil { return err }
    if _, err := file.WriteString(data); err != nil { return err }
    if err := file.Sync(); err != nil { return err }
    if err := file.Close(); err != nil { return err }
    if err := os.Rename(file.Name(), path); err != nil { return err }
    return nil
}

func (m *Manager) TestOffsiteConnection(ctx context.Context) error {
    remote := m.BackupRemote()
    if remote == "" { return errors.New("external destination is not configured") }
    if err := validateBackupRemote(remote); err != nil { return err }
    args := []string{"lsf", remote, "--max-depth", "1", "--timeout", "30m", "--contimeout", "1m", "--retries", "3"}
    if conn := m.OffsiteConnection(); conn.Provider != "existing" {
        args = append([]string{"--config", m.offsiteConfigFile}, args...)
    }
    cmd := exec.CommandContext(ctx, "rclone", args...)
    var stderr limitedOutput
    cmd.Stderr = &stderr
    cmd.Stdout = &limitedOutput{}
    if err := cmd.Run(); err != nil { return fmt.Errorf("external storage check failed: %w: %s", err, stderr.String()) }
    return nil
}

// Current backups are not removed from the remote. External retention remains
// independent of the panel's local keep-copies setting.
func (m *Manager) UploadLatestAll(ctx context.Context) error {
    if !m.operationMu.TryLock() { return errOperationInProgress }
    defer m.operationMu.Unlock()
    if m.BackupRemote() == "" { return errors.New("external backup destination is not configured") }
    databases, err := m.List()
    if err != nil { return err }
    var failed []string
    for _, database := range databases {
        backups, err := m.Backups(database.ID)
        if err != nil {
            failed = append(failed, database.Name+": "+err.Error())
            continue
        }
        if len(backups) == 0 {
            failed = append(failed, database.Name+": no local backup available")
            continue
        }
        if err := m.UploadBackup(ctx, backups[0]); err != nil {
            failed = append(failed, database.Name+": "+err.Error())
        }
    }
    if len(failed) > 0 { return errors.New(strings.Join(failed, "; ")) }
    return nil
}

func (m *Manager) UploadOneBackup(ctx context.Context, id int64, path string) error {
    if !m.operationMu.TryLock() { return errOperationInProgress }
    defer m.operationMu.Unlock()
    if m.BackupRemote() == "" { return errors.New("external destination is not configured") }
    clean, err := m.BackupFile(id, path)
    if err != nil { return err }
    info, err := os.Stat(clean)
    if err != nil { return err }
    database, err := m.Get(id)
    if err != nil { return err }
    return m.UploadBackup(ctx, Backup{Engine: database.Engine, Database: database.Name, Path: clean, Size: info.Size(), CreatedAt: info.ModTime().UTC()})
}

func (m *Manager) LastBackupDate() string {
    value, _, _ := m.store.Setting("db.backup_last_date")
    return value
}

func (m *Manager) LastOffsiteDate() string {
    value, _, _ := m.store.Setting("db.offsite_last_date")
    return value
}

func (m *Manager) SetOffsiteCompletedAt(t time.Time) error {
    return m.store.SetSetting("db.offsite_last_success", t.UTC().Format(time.RFC3339))
}

func (m *Manager) OffsiteCompletedAt() string {
    value, _, _ := m.store.Setting("db.offsite_last_success")
    return value
}

// Saved secrets are read internally only to support editing a connection
// without asking the administrator to re-enter an unchanged access key.
func (m *Manager) savedS3Credentials() (string, string) {
    info, err := os.Lstat(m.offsiteConfigFile)
    if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 { return "", "" }
    data, err := os.ReadFile(m.offsiteConfigFile)
    if err != nil { return "", "" }
    var access, secret string
    for _, line := range strings.Split(string(data), "\n") {
        if value, ok := strings.CutPrefix(line, "access_key_id = "); ok { access = value }
        if value, ok := strings.CutPrefix(line, "secret_access_key = "); ok { secret = value }
    }
    return access, secret
}
