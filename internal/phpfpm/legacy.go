package phpfpm

import (
    "archive/tar"
    "compress/gzip"
    "context"
    "debug/elf"
    "errors"
    "fmt"
    "io"
    "net"
    "os"
    "os/exec"
    "os/user"
    "path/filepath"
    "runtime"
    "strconv"
    "strings"
    "time"
)

const (
    LegacyVersion = "7.4"
    LegacyImage = "open-go-panel/php74:local"
    legacyLoaderInside = "/opt/ogp-ioncube-loader.so"
    legacyErrorLogInside = "/var/log/ogp-php-error.log"
)

// Legacy PHP 7.4 is EOL. It is intentionally kept out of the system PHP-FPM
// service and confined to one unprivileged, resource-limited Docker container
// per site. No Docker socket or host root directory is mounted in a container.
func LegacyContainerName(id int64) string {
    return "ogp-php74-" + strconv.FormatInt(id, 10)
}

func LegacyLoaderPath() string {
    return filepath.Join("/usr/local/lib/open-go-panel/ioncube", runtime.GOARCH, "ioncube_loader_lin_7.4.so")
}

func LegacyLoaderPresent() bool {
    info, err := os.Stat(LegacyLoaderPath())
    return err == nil && info.Mode().IsRegular() && info.Size() >= 64
}

func dockerOutput(ctx context.Context, args ...string) (string, error) {
    cmd := exec.CommandContext(ctx, "docker", args...)
    result, err := cmd.CombinedOutput()
    if err != nil {
        return "", fmt.Errorf("docker %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(result)))
    }
    return strings.TrimSpace(string(result)), nil
}

// InstallLegacyLoader selects only the PHP 7.4 shared object from an official
// ionCube Linux tarball. Tar entry paths are never used as output filenames.
func InstallLegacyLoader(archive io.Reader) error {
    content, err := extractLegacyLoader(archive)
    if err != nil { return err }
    dir := filepath.Dir(LegacyLoaderPath())
    if err := os.MkdirAll(dir, 0755); err != nil { return err }
    file, err := os.CreateTemp(dir, ".ioncube-7.4-*")
    if err != nil { return err }
    defer os.Remove(file.Name())
    if _, err := file.Write(content); err != nil { _ = file.Close(); return err }
    if err := file.Chmod(0644); err != nil { _ = file.Close(); return err }
    if err := file.Sync(); err != nil { _ = file.Close(); return err }
    if err := file.Close(); err != nil { return err }
    return os.Rename(file.Name(), LegacyLoaderPath())
}

func extractLegacyLoader(archive io.Reader) ([]byte, error) {
    gz, err := gzip.NewReader(io.LimitReader(archive, 64<<20))
    if err != nil { return nil, fmt.Errorf("read ionCube gzip: %w", err) }
    defer gz.Close()
    tarReader := tar.NewReader(io.LimitReader(gz, 256<<20))
    target := "ioncube/ioncube_loader_lin_7.4.so"
    var content []byte
    for {
        header, err := tarReader.Next()
        if errors.Is(err, io.EOF) { break }
        if err != nil { return nil, fmt.Errorf("read ionCube archive: %w", err) }
        if strings.TrimPrefix(header.Name, "./") != target { continue }
        if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
            return nil, errors.New("ionCube Loader must be a regular file")
        }
        if header.Size < 64 || header.Size > 32<<20 {
            return nil, errors.New("invalid ionCube Loader size")
        }
        content, err = io.ReadAll(io.LimitReader(tarReader, header.Size+1))
        if err != nil { return nil, err }
        if int64(len(content)) != header.Size { return nil, errors.New("ionCube Loader truncated") }
        break
    }
    if content == nil { return nil, errors.New("archive does not contain ioncube_loader_lin_7.4.so") }
    machine := elf.EM_X86_64
    if runtime.GOARCH == "arm64" { machine = elf.EM_AARCH64 }
    if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
        return nil, errors.New("unsupported ionCube Loader architecture")
    }
    elfFile, err := elf.NewFile(strings.NewReader(string(content)))
    if err != nil || elfFile.Class != elf.ELFCLASS64 || elfFile.Type != elf.ET_DYN || elfFile.Machine != machine {
        return nil, errors.New("ionCube Loader is not a shared object for this server architecture")
    }
    return content, nil
}

const legacyDockerfile = `FROM php:7.4-fpm-bullseye
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
      libfreetype6-dev libjpeg62-turbo-dev libpng-dev libzip-dev libonig-dev \
      libicu-dev libmagickwand-dev; \
    docker-php-ext-configure gd --with-freetype --with-jpeg; \
    docker-php-ext-install -j2 mysqli pdo_mysql gd zip mbstring intl opcache bcmath; \
    pecl install imagick-3.7.0; \
    docker-php-ext-enable imagick; \
    rm -rf /var/lib/apt/lists/*
`

// BuildLegacyImage is called only by the existing asynchronous Software task.
// The build uses an upstream official PHP image and never downloads ionCube;
// Loader is supplied separately via admin upload and mounted read-only.
func BuildLegacyImage(ctx context.Context) error {
    if _, err := exec.LookPath("docker"); err != nil { return errors.New("Docker Engine must be installed first") }
    dir, err := os.MkdirTemp("", "ogp-php74-build-*")
    if err != nil { return err }
    defer os.RemoveAll(dir)
    if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(legacyDockerfile), 0600); err != nil { return err }
    output, err := os.CreateTemp("", "ogp-php74-build-output-*")
    if err != nil { return err }
    defer os.Remove(output.Name())
    defer output.Close()
    cmd := exec.CommandContext(ctx, "docker", "build", "--pull", "-t", LegacyImage, dir)
    cmd.Stdout, cmd.Stderr = output, output
    if err := cmd.Run(); err != nil {
        _, _ = output.Seek(-min64(16<<10, mustFileSize(output)), io.SeekEnd)
        tail, _ := io.ReadAll(io.LimitReader(output, 16<<10))
        return fmt.Errorf("PHP 7.4 Docker image build: %w: %s", err, strings.TrimSpace(string(tail)))
    }
    required := []string{"curl", "gd", "imagick", "intl", "mbstring", "mysqli", "openssl", "pdo", "pdo_mysql", "zip"}
    modules, err := dockerOutput(ctx, "run", "--rm", "--network", "none", LegacyImage, "php", "-m")
    if err != nil { return err }
    installed := "\n" + strings.ToLower(modules) + "\n"
    for _, module := range required {
        if !strings.Contains(installed, "\n"+module+"\n") {
            return fmt.Errorf("PHP 7.4 image missing required extension %s", module)
        }
    }
    return nil
}

func min64(a, b int64) int64 {
    if a < b { return a }
    return b
}
func mustFileSize(file *os.File) int64 {
    info, err := file.Stat()
    if err != nil { return 0 }
    return info.Size()
}

func LegacyImageInstalled(ctx context.Context) bool {
    _, err := dockerOutput(ctx, "image", "inspect", LegacyImage)
    return err == nil
}

func LegacyStatus(ctx context.Context, id int64) string {
    if id <= 0 { return "not configured" }
    output, err := dockerOutput(ctx, "container", "inspect", "--format", "{{.State.Status}}", LegacyContainerName(id))
    if err != nil { return "not configured" }
    if output == "running" { return "active" }
    if output == "exited" || output == "created" { return "inactive" }
    return output
}

// EnsureLegacy starts the isolated PHP-FPM backend bound only to localhost.
// Caddy keeps the same app document root inside and outside the container.
func EnsureLegacy(ctx context.Context, id int64, username, root string, port int) error {
    if id <= 0 || port < 1024 || port > 65535 {
        return errors.New("invalid legacy PHP runtime target")
    }
    if !filepath.IsAbs(root) || strings.ContainsAny(root, ",\r\n") {
        return errors.New("unsafe PHP application root")
    }
    if !LegacyLoaderPresent() {
        return errors.New("PHP 7.4 ionCube Loader is missing; upload the official archive in Software")
    }
    if !LegacyImageInstalled(ctx) {
        return errors.New("PHP 7.4 Docker image is not installed; build it in Software first")
    }
    account, err := user.Lookup(username)
    if err != nil { return fmt.Errorf("lookup PHP site user: %w", err) }
    uid, err := strconv.Atoi(account.Uid)
    if err != nil { return err }
    gid, err := strconv.Atoi(account.Gid)
    if err != nil { return err }
    if uid <= 0 || gid <= 0 { return errors.New("legacy PHP must not run as root") }
    if err := ensureErrorLog(id, uid, gid); err != nil { return err }

    // Validate this exact loader against the container's PHP 7.4 runtime.
    loaderMount := "type=bind,src=" + LegacyLoaderPath() + ",dst=" + legacyLoaderInside + ",readonly"
    verify, err := dockerOutput(ctx, "run", "--rm", "--network", "none",
        "--mount", loaderMount, LegacyImage, "php", "-n",
        "-d", "zend_extension="+legacyLoaderInside, "-v")
    if err != nil || !strings.Contains(strings.ToLower(verify), "with the ioncube php loader") {
        return fmt.Errorf("PHP 7.4 ionCube compatibility check failed: %v: %s", err, verify)
    }

    name := LegacyContainerName(id)
    status := LegacyStatus(ctx, id)
    if status == "active" { return nil }
    if status == "inactive" {
        if _, err := dockerOutput(ctx, "start", name); err != nil { return err }
    } else if status == "not configured" {
        args := []string{
            "run", "-d", "--name", name, "--restart", "unless-stopped",
            "--user", fmt.Sprintf("%d:%d", uid, gid),
            "--cap-drop", "ALL", "--security-opt", "no-new-privileges",
            "--read-only", "--tmpfs", "/tmp:rw,nosuid,noexec,size=64m",
            "--tmpfs", "/usr/local/var/run:rw,nosuid,size=8m",
            "--memory", "512m", "--pids-limit", "128", "--cpus", "1",
            "--publish", fmt.Sprintf("127.0.0.1:%d:9000", port),
            "--add-host", "host.docker.internal:host-gateway",
            "--mount", "type=bind,src="+root+",dst="+root,
            "--mount", loaderMount,
            "--mount", "type=bind,src="+ErrorLogPath(id)+",dst="+legacyErrorLogInside,
        }
        // Preserve localhost/socket MySQL access where a host MySQL server
        // uses the standard Ubuntu Unix socket. TCP localhost still resolves
        // inside the container and requires explicit application config.
        if stat, err := os.Stat("/run/mysqld"); err == nil && stat.IsDir() {
            args = append(args, "--mount", "type=bind,src=/run/mysqld,dst=/run/mysqld,readonly")
        }
        args = append(args,
            "--workdir", root,
            LegacyImage, "php-fpm", "-F",
            "-d", "zend_extension="+legacyLoaderInside,
            "-d", "log_errors=1", "-d", "display_errors=0",
            "-d", "error_log="+legacyErrorLogInside,
            "-d", "mysqli.default_socket=/run/mysqld/mysqld.sock",
            "-d", "pdo_mysql.default_socket=/run/mysqld/mysqld.sock",
        )
        if _, err := dockerOutput(ctx, args...); err != nil { return err }
    } else {
        return fmt.Errorf("legacy PHP container in unexpected state %s", status)
    }

    // Validate an actual FastCGI listener, not merely Docker's running flag.
    target := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
    for attempt := 0; attempt < 60; attempt++ {
        conn, err := net.DialTimeout("tcp", target, 200*time.Millisecond)
        if err == nil { _ = conn.Close(); return nil }
        if ctx.Err() != nil { return ctx.Err() }
        if LegacyStatus(ctx, id) != "active" { break }
        time.Sleep(150 * time.Millisecond)
    }
    _ = RemoveLegacy(context.Background(), id)
    return fmt.Errorf("PHP 7.4 container did not expose its FastCGI listener at %s", target)
}

func RemoveLegacy(ctx context.Context, id int64) error {
    if id <= 0 { return errors.New("invalid application ID") }
    name := LegacyContainerName(id)
    status := LegacyStatus(ctx, id)
    if status == "not configured" { return nil }
    _, err := dockerOutput(ctx, "rm", "-f", name)
    return err
}
