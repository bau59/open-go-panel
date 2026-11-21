package software

import (
    "archive/tar"
    "compress/gzip"
    "context"
    "encoding/binary"
    "errors"
    "fmt"
    "io"
    "net/http"
    "os"
    "os/exec"
    "path/filepath"
    "runtime"
    "strings"
    "time"

    "github.com/bau59/open-go-panel/internal/phpfpm"
)

type PHPExtension struct {
    ID string
    Name string
    Installed bool
}

var phpExtensionNames = []struct{ ID, Name string }{
    {"ioncube", "ionCube Loader"},
    {"gd", "GD"},
    {"intl", "Intl"},
    {"bcmath", "BCMath"},
    {"soap", "SOAP"},
    {"sqlite3", "SQLite3"},
    {"redis", "Redis"},
    {"imagick", "ImageMagick"},
    {"apcu", "APCu"},
    {"opcache", "OPcache"},
}

func allowedPHPExtension(id string) bool {
    for _, item := range phpExtensionNames {
        if item.ID == id { return true }
    }
    return false
}

// PHPExtensions displays extensions for the PHP-FPM version used by managed
// applications. This is a shared PHP runtime, not a per-app PHP installation.
func (m *Manager) PHPExtensions() (string, []PHPExtension) {
    php, err := phpfpm.Detect()
    if err != nil { return "", nil }
    var result []PHPExtension
    for _, item := range phpExtensionNames {
        files, _ := filepath.Glob(filepath.Join("/etc/php", php.Version, "fpm", "conf.d", "*-"+item.ID+".ini"))
        if item.ID == "ioncube" {
            files, _ = filepath.Glob(filepath.Join("/etc/php", php.Version, "fpm", "conf.d", "*ioncube*.ini"))
        }
        result = append(result, PHPExtension{
            ID: item.ID, Name: item.Name, Installed: len(files) != 0,
        })
    }
    return php.Version, result
}

func (m *Manager) StartPHPExtension(id string) error {
    if !allowedPHPExtension(id) { return errors.New("unsupported PHP extension") }
    php, err := phpfpm.Detect()
    if err != nil { return err }
    m.mu.Lock()
    if m.task.Running {
        current := m.task
        m.mu.Unlock()
        return fmt.Errorf("%s %s is already running", current.Action, current.SoftwareID)
    }
    m.task = Task{
        SoftwareID: "PHP "+php.Version+" "+id, Action: "install extension",
        Running: true, StartedAt: time.Now(),
    }
    m.mu.Unlock()
    go func() {
        ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
        defer cancel()
        err := installPHPExtension(ctx, php, id)
        m.mu.Lock()
        m.task.Running = false
        m.task.FinishedAt = time.Now()
        if err != nil { m.task.Error = err.Error() } else { m.task.Error = "" }
        m.mu.Unlock()
    }()
    return nil
}

func installPHPExtension(ctx context.Context, php phpfpm.Runtime, id string) error {
    if !allowedPHPExtension(id) { return errors.New("unsupported PHP extension") }
    if id == "ioncube" { return installIonCube(ctx, php) }

    // Package names come only from the allowlist and a detected /etc/php
    // version. There is no user-supplied shell command or package name.
    if err := aptInstall(ctx, "php"+php.Version+"-"+id); err != nil { return err }
    if err := run(ctx, php.Binary, "-t"); err != nil {
        return fmt.Errorf("PHP-FPM config validation failed after installing %s: %w", id, err)
    }
    return reloadPHPService(ctx, php.Service)
}

func reloadPHPService(ctx context.Context, service string) error {
    active := exec.CommandContext(ctx, "systemctl", "is-active", "--quiet", service).Run() == nil
    action := "start"
    if active { action = "reload" }
    if err := run(ctx, "systemctl", action, service); err != nil {
        return fmt.Errorf("%s %s: %w", action, service, err)
    }
    return nil
}

func ionCubeURL(goarch string) (string, uint16, error) {
    switch goarch {
    case "amd64":
        return "https://downloads.ioncube.com/loader_downloads/ioncube_loaders_lin_x86-64.tar.gz", 62, nil
    case "arm64":
        return "https://downloads.ioncube.com/loader_downloads/ioncube_loaders_lin_aarch64.tar.gz", 183, nil
    default:
        return "", 0, fmt.Errorf("ionCube Loader does not support this panel architecture: %s", goarch)
    }
}

// extractIonCube selects only the loader matching the detected PHP major.minor
// version. No tar entry paths are ever used to write files on the host.
func extractIonCube(archive io.Reader, version string, expectedMachine uint16) ([]byte, error) {
    gz, err := gzip.NewReader(io.LimitReader(archive, 64<<20))
    if err != nil { return nil, fmt.Errorf("ionCube download is not gzip: %w", err) }
    defer gz.Close()
    reader := tar.NewReader(io.LimitReader(gz, 256<<20))
    selectedName := "ioncube/ioncube_loader_lin_" + version + ".so"
    for {
        header, err := reader.Next()
        if errors.Is(err, io.EOF) { break }
        if err != nil { return nil, fmt.Errorf("read ionCube archive: %w", err) }
        if strings.TrimPrefix(header.Name, "./") != selectedName { continue }
        if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
            return nil, errors.New("ionCube loader archive entry is not a regular file")
        }
        if header.Size < 64 || header.Size > 32<<20 {
            return nil, errors.New("ionCube loader has an invalid size")
        }
        data, err := io.ReadAll(io.LimitReader(reader, header.Size+1))
        if err != nil { return nil, err }
        if int64(len(data)) != header.Size { return nil, errors.New("ionCube loader truncated") }
        if err := validateLoaderELF(data, expectedMachine); err != nil { return nil, err }
        return data, nil
    }
    return nil, fmt.Errorf("official ionCube package does not include a loader for PHP %s", version)
}

func validateLoaderELF(data []byte, machine uint16) error {
    if len(data) < 64 || string(data[:4]) != "\x7fELF" || data[4] != 2 || data[5] != 1 {
        return errors.New("downloaded ionCube loader is not a 64-bit Linux ELF")
    }
    if binary.LittleEndian.Uint16(data[18:20]) != machine {
        return errors.New("ionCube loader architecture does not match the server")
    }
    if binary.LittleEndian.Uint16(data[16:18]) != 3 {
        return errors.New("ionCube loader is not a shared object")
    }
    return nil
}

const (
    ionCubeMaxArchiveBytes = 64 << 20
    ionCubeDownloadAttempts = 3
)

// downloadIonCube retries only network failures (including interrupted body
// reads) and transient HTTP status codes. Never use bytes from a partial
// response, and never retry malformed/unsupported loader binaries.
func downloadIonCube(ctx context.Context, version, arch string) ([]byte, error) {
    url, machine, err := ionCubeURL(arch)
    if err != nil { return nil, err }

    client := &http.Client{
        Timeout: 4*time.Minute,
        CheckRedirect: func(req *http.Request, via []*http.Request) error {
            if len(via) >= 3 || req.URL.Scheme != "https" || req.URL.Hostname() != "downloads.ioncube.com" {
                return errors.New("unexpected ionCube download redirect")
            }
            return nil
        },
    }
    return downloadIonCubeWithRetry(ctx, client, url, version, machine)
}

func downloadIonCubeWithRetry(ctx context.Context, client *http.Client, url, version string, machine uint16) ([]byte, error) {
    var lastErr error
    for attempt := 1; attempt <= ionCubeDownloadAttempts; attempt++ {
        if err := ctx.Err(); err != nil { return nil, err }
        data, retryable, err := fetchIonCubeOnce(ctx, client, url, version, machine)
        if err == nil { return data, nil }
        lastErr = err
        if !retryable || attempt == ionCubeDownloadAttempts {
            return nil, fmt.Errorf("ionCube download attempt %d/%d: %w", attempt, ionCubeDownloadAttempts, lastErr)
        }
        // Short bounded backoff and cancellation between attempts.
        timer := time.NewTimer(time.Duration(attempt*2) * time.Second)
        select {
        case <-ctx.Done():
            timer.Stop()
            return nil, ctx.Err()
        case <-timer.C:
        }
    }
    return nil, lastErr
}

func fetchIonCubeOnce(ctx context.Context, client *http.Client, url, version string, machine uint16) ([]byte, bool, error) {
    request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
    if err != nil { return nil, false, err }
    request.Header.Set("Accept-Encoding", "identity")
    response, err := client.Do(request)
    if err != nil { return nil, ctx.Err() == nil, fmt.Errorf("contact official ionCube host: %w", err) }
    defer response.Body.Close()

    if response.StatusCode != http.StatusOK {
        retryable := response.StatusCode == http.StatusRequestTimeout ||
            response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500
        return nil, retryable, fmt.Errorf("ionCube download HTTP status %d", response.StatusCode)
    }
    if response.ContentLength > ionCubeMaxArchiveBytes {
        return nil, false, errors.New("ionCube package exceeds 64 MiB")
    }

    // Download the complete vendor archive to a private temporary file.
    // Extracting directly from the HTTP stream could accept a partial
    // archive if the matching loader happened to appear before the timeout.
    archive, err := os.CreateTemp("", "ogp-ioncube-download-*")
    if err != nil { return nil, false, fmt.Errorf("stage ionCube package: %w", err) }
    defer os.Remove(archive.Name())
    defer archive.Close()
    if err := archive.Chmod(0600); err != nil { return nil, false, err }

    size, err := io.Copy(archive, io.LimitReader(response.Body, ionCubeMaxArchiveBytes+1))
    if err != nil { return nil, ctx.Err() == nil, fmt.Errorf("read ionCube download (%d bytes received): %w", size, err) }
    if size > ionCubeMaxArchiveBytes {
        return nil, false, errors.New("ionCube package exceeds 64 MiB")
    }
    if size == 0 || (response.ContentLength >= 0 && size != response.ContentLength) {
        return nil, true, fmt.Errorf("incomplete ionCube download: received %d bytes, expected %d", size, response.ContentLength)
    }
    if _, err := archive.Seek(0, io.SeekStart); err != nil { return nil, false, err }
    loader, err := extractIonCube(archive, version, machine)
    if err != nil { return nil, false, err }
    return loader, false, nil
}

func ionCubeLoaded(output []byte) bool {
    normalized := strings.ToLower(string(output))
    return strings.Contains(normalized, "with the ioncube php loader") &&
        !strings.Contains(normalized, "failed loading")
}

func installIonCube(ctx context.Context, php phpfpm.Runtime) error {
    if php.Version == "" || strings.ContainsAny(php.Version, "/\\") {
        return errors.New("invalid detected PHP version")
    }
    arch := runtime.GOARCH
    data, err := downloadIonCube(ctx, php.Version, arch)
    if err != nil { return err }

    dir := filepath.Join("/usr/local/lib/open-go-panel/ioncube", arch)
    if err := os.MkdirAll(dir, 0755); err != nil { return err }
    library := filepath.Join(dir, "ioncube_loader_lin_"+php.Version+".so")
    config := filepath.Join("/etc/php", php.Version, "fpm", "conf.d", "00-ioncube.ini")
    // Do not load the vendor module twice if another administrator already
    // enabled ionCube outside Open Go Panel's managed configuration.
    configured, err := filepath.Glob(filepath.Join("/etc/php", php.Version, "fpm", "conf.d", "*ioncube*.ini"))
    if err != nil { return err }
    for _, candidate := range configured {
        if candidate != config {
            return fmt.Errorf("ionCube is already configured in %s; remove the duplicate manually before managing it here", candidate)
        }
    }
    tmp, err := os.CreateTemp(dir, ".ioncube-*.so")
    if err != nil { return err }
    tempName := tmp.Name()
    defer os.Remove(tempName)
    if _, err := tmp.Write(data); err != nil { tmp.Close(); return err }
    if err := tmp.Chmod(0644); err != nil { tmp.Close(); return err }
    if err := tmp.Sync(); err != nil { tmp.Close(); return err }
    if err := tmp.Close(); err != nil { return err }

    // Load-check the vendor binary before putting it into PHP-FPM's config.
    phpCLI := "php"+php.Version
    check := exec.CommandContext(ctx, phpCLI, "-n", "-d", "zend_extension="+tempName, "-v")
    output, err := check.CombinedOutput()
    if err != nil || !ionCubeLoaded(output) {
        return fmt.Errorf("ionCube failed PHP %s loader validation: %s", php.Version, strings.TrimSpace(string(output)))
    }

    oldLibrary, readErr := os.ReadFile(library)
    if readErr != nil && !errors.Is(readErr, os.ErrNotExist) { return readErr }
    oldConfig, configErr := os.ReadFile(config)
    if configErr != nil && !errors.Is(configErr, os.ErrNotExist) { return configErr }
    restore := func() {
        if readErr == nil { _ = os.WriteFile(library, oldLibrary, 0644) } else { _ = os.Remove(library) }
        if configErr == nil { _ = os.WriteFile(config, oldConfig, 0644) } else { _ = os.Remove(config) }
        rollback, cancel := context.WithTimeout(context.Background(), 30*time.Second)
        defer cancel()
        _ = run(rollback, php.Binary, "-t")
        _ = reloadPHPService(rollback, php.Service)
    }
    if err := os.Rename(tempName, library); err != nil { return err }
    configTemp, err := os.CreateTemp(filepath.Dir(config), ".ioncube-*.ini")
    if err != nil { restore(); return err }
    configTmpName := configTemp.Name()
    defer os.Remove(configTmpName)
    if _, err := io.WriteString(configTemp, "zend_extension="+library+"\n"); err != nil {
        configTemp.Close(); restore(); return err
    }
    if err := configTemp.Chmod(0644); err != nil { configTemp.Close(); restore(); return err }
    if err := configTemp.Close(); err != nil { restore(); return err }
    if err := os.Rename(configTmpName, config); err != nil { restore(); return err }
    if err := run(ctx, php.Binary, "-t"); err != nil {
        restore()
        return fmt.Errorf("ionCube PHP-FPM config test failed (previous config restored): %w", err)
    }
    // php-fpm -t validates the ini syntax, while -v also confirms that the
    // Loader was actually recognized by the FPM binary before live reload.
    fpmOutput, verifyErr := exec.CommandContext(ctx, php.Binary, "-v").CombinedOutput()
    if verifyErr != nil || !ionCubeLoaded(fpmOutput) {
        restore()
        return fmt.Errorf("ionCube is not active in PHP-FPM %s (previous config restored): %s", php.Version, strings.TrimSpace(string(fpmOutput)))
    }
    if err := reloadPHPService(ctx, php.Service); err != nil {
        restore()
        return fmt.Errorf("ionCube PHP-FPM reload failed (previous config restored): %w", err)
    }
    return nil
}
