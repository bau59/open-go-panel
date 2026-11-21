package phpfpm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var accountNameRE = regexp.MustCompile(`^[a-z_][a-z0-9_-]*$`)

type Runtime struct {
	Version string
	Binary  string
	Service string
	PoolDir string
}

func Socket(id int64) string {
	return fmt.Sprintf("/run/php/ogp-app-%d.sock", id)
}

func poolName(id int64) string {
	return fmt.Sprintf("ogp-app-%d", id)
}

func poolPath(runtime Runtime, id int64) string {
	return filepath.Join(runtime.PoolDir, poolName(id)+".conf")
}

func versionLess(a, b string) bool {
	left, right := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(left) || i < len(right); i++ {
		x, y := 0, 0
		if i < len(left) { x, _ = strconv.Atoi(left[i]) }
		if i < len(right) { y, _ = strconv.Atoi(right[i]) }
		if x != y { return x < y }
	}
	return false
}

func runtimes() ([]Runtime, error) {
	dirs, err := filepath.Glob("/etc/php/*/fpm/pool.d")
	if err != nil { return nil, err }
	var found []Runtime
	for _, dir := range dirs {
		version := filepath.Base(filepath.Dir(filepath.Dir(dir)))
		if !regexp.MustCompile(`^[0-9]+\.[0-9]+$`).MatchString(version) { continue }
		binary := "/usr/sbin/php-fpm" + version
		if info, err := os.Stat(binary); err != nil || info.IsDir() { continue }
		found = append(found, Runtime{
			Version: version, Binary: binary,
			Service: "php" + version + "-fpm.service",
			PoolDir: dir,
		})
	}
	sort.Slice(found, func(i, j int) bool { return versionLess(found[j].Version, found[i].Version) })
	return found, nil
}

func Detect() (Runtime, error) {
	found, err := runtimes()
	if err != nil { return Runtime{}, err }
	if len(found) == 0 {
		return Runtime{}, errors.New("PHP-FPM is not installed; install PHP-FPM from Software first")
	}
	return found[0], nil
}

func poolConfig(id int64, username, group string) string {
	return fmt.Sprintf(`[%s]
user = %s
group = %s
listen = %s
listen.owner = caddy
listen.group = caddy
listen.mode = 0660
pm = ondemand
pm.max_children = 5
pm.process_idle_timeout = 10s
pm.max_requests = 500
security.limit_extensions = .php
`, poolName(id), username, group, Socket(id))
}

func run(ctx context.Context, name string, args ...string) error {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil { return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out))) }
	return nil
}

func serviceActive(ctx context.Context, name string) bool {
	return exec.CommandContext(ctx, "systemctl", "is-active", "--quiet", name).Run() == nil
}

func writePool(path, data string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ogp-pool-*.conf")
	if err != nil { return err }
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(data); err != nil { _ = tmp.Close(); return err }
	if err := tmp.Chmod(0644); err != nil { _ = tmp.Close(); return err }
	if err := tmp.Close(); err != nil { return err }
	return os.Rename(tmp.Name(), path)
}

// Ensure gives each application its own on-demand PHP-FPM pool running as its
// Linux owner. The FastCGI socket is accessible to Caddy, not other apps.
func Ensure(ctx context.Context, id int64, username string) error {
	if id <= 0 { return errors.New("invalid PHP application id") }
	runtime, err := Detect()
	if err != nil { return err }
	if _, err := user.Lookup("caddy"); err != nil { return errors.New("Caddy system user is not available") }
	account, err := user.Lookup(username)
	if err != nil { return fmt.Errorf("lookup PHP app user: %w", err) }
	group, err := user.LookupGroupId(account.Gid)
	if err != nil { return fmt.Errorf("lookup PHP app group: %w", err) }
	if !accountNameRE.MatchString(account.Username) || !accountNameRE.MatchString(group.Name) {
		return errors.New("unsupported Linux user or group name for PHP-FPM")
	}

	path := poolPath(runtime, id)
	original, readErr := os.ReadFile(path)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) { return readErr }
	expected := poolConfig(id, account.Username, group.Name)
	changed := string(original) != expected
	if changed {
		if err := writePool(path, expected); err != nil { return fmt.Errorf("write PHP-FPM pool: %w", err) }
	}
	rollback := func() {
		if !changed { return }
		if errors.Is(readErr, os.ErrNotExist) { _ = os.Remove(path) } else { _ = writePool(path, string(original)) }
		_ = run(context.Background(), "systemctl", "reload", runtime.Service)
	}
	if err := run(ctx, runtime.Binary, "-t"); err != nil {
		rollback()
		return fmt.Errorf("invalid PHP-FPM pool: %w", err)
	}
	if err := run(ctx, "systemctl", "enable", "--now", runtime.Service); err != nil {
		rollback()
		return fmt.Errorf("start PHP-FPM: %w", err)
	}
	if changed {
		if err := run(ctx, "systemctl", "reload", runtime.Service); err != nil {
			rollback()
			return fmt.Errorf("reload PHP-FPM: %w", err)
		}
	}
	for attempt := 0; attempt < 30; attempt++ {
		if _, err := os.Stat(Socket(id)); err == nil { return nil }
		if err := ctx.Err(); err != nil { return err }
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("PHP-FPM socket %s was not created", Socket(id))
}

func Status(ctx context.Context, id int64) string {
	found, err := runtimes()
	if err != nil { return "not configured" }
	for _, runtime := range found {
		if _, err := os.Stat(poolPath(runtime, id)); err == nil {
			if !serviceActive(ctx, runtime.Service) { return "inactive" }
			if _, err := os.Stat(Socket(id)); err == nil { return "active" }
			return "inactive"
		}
	}
	return "not configured"
}

func Remove(ctx context.Context, id int64) error {
	found, err := runtimes()
	if err != nil { return err }
	for _, runtime := range found {
		path := poolPath(runtime, id)
		original, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) { continue }
		if err != nil { return err }
		if err := os.Remove(path); err != nil { return err }
		if !serviceActive(ctx, runtime.Service) { continue }
		if err := run(ctx, "systemctl", "reload", runtime.Service); err != nil {
			_ = writePool(path, string(original))
			_ = run(context.Background(), "systemctl", "reload", runtime.Service)
			return fmt.Errorf("reload PHP-FPM after pool removal: %w", err)
		}
	}
	return nil
}
