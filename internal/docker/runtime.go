package docker

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	maxEnvironmentBytes = 64 << 10
	maxVolumeBytes      = 8 << 10
)

var (
	envNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	shmSizeRE = regexp.MustCompile(`^[1-9][0-9]{0,5}[kKmMgG]$`)
	volumeNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
)

// RuntimeConfig is supplied when creating a container, not during image build.
// It is never embedded in an image, Dockerfile, build arguments or log line.
type RuntimeConfig struct {
	Environment string
	Volumes     string
	Init        bool
	ShmSize     string
}

type volumeMount struct {
	Source string
	Target string
	Named  bool
}

func parseEnvironment(raw string) (string, error) {
	if len(raw) > maxEnvironmentBytes {
		return "", errors.New("environment configuration exceeds 64 KiB")
	}
	var values []string
	seen := make(map[string]bool)
	for n, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || !envNameRE.MatchString(key) {
			return "", fmt.Errorf("environment line %d: expected KEY=value", n+1)
		}
		if seen[key] {
			return "", fmt.Errorf("environment line %d: duplicate variable %s", n+1, key)
		}
		seen[key] = true
		values = append(values, key+"="+value)
	}
	return strings.Join(values, "\n"), nil
}

func parseVolumes(raw string) ([]volumeMount, error) {
	if len(raw) > maxVolumeBytes {
		return nil, errors.New("volume configuration exceeds 8 KiB")
	}
	var volumes []volumeMount
	targets := map[string]bool{}
	for i, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) != 2 {
			return nil, fmt.Errorf("volume line %d: expected /host/path:/container/path or volume_name:/container/path", i+1)
		}
		source, target := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if source == "" || target == "" || !filepath.IsAbs(target) || filepath.Clean(target) != target || target == "/" ||
			strings.ContainsAny(line, "\r\n\x00,") {
			return nil, fmt.Errorf("volume line %d: invalid mount path", i+1)
		}
		mount := volumeMount{Source: source, Target: target}
		if filepath.IsAbs(source) {
			if filepath.Clean(source) != source || source == "/" {
				return nil, fmt.Errorf("volume line %d: invalid host path", i+1)
			}
		} else {
			if !volumeNameRE.MatchString(source) {
				return nil, fmt.Errorf("volume line %d: invalid named volume", i+1)
			}
			mount.Named = true
		}
		if targets[target] {
			return nil, fmt.Errorf("volume line %d: duplicate container path %s", i+1, target)
		}
		targets[target] = true
		volumes = append(volumes, mount)
	}
	if len(volumes) > 20 {
		return nil, errors.New("too many mounts (max 20)")
	}
	return volumes, nil
}

func (cfg RuntimeConfig) validate() (string, []volumeMount, error) {
	env, err := parseEnvironment(cfg.Environment)
	if err != nil {
		return "", nil, err
	}
	mounts, err := parseVolumes(cfg.Volumes)
	if err != nil {
		return "", nil, err
	}
	if cfg.ShmSize != "" && !shmSizeRE.MatchString(strings.TrimSpace(cfg.ShmSize)) {
		return "", nil, errors.New("shared memory must look like 512m or 1g")
	}
	return env, mounts, nil
}

func runtimeArgs(cfg RuntimeConfig, environment string, mounts []volumeMount) ([]string, func(), error) {
	args := []string{}
	cleanup := func() {}
	if cfg.Init {
		args = append(args, "--init")
	}
	if shm := strings.TrimSpace(cfg.ShmSize); shm != "" {
		args = append(args, "--shm-size", shm)
	}
	if environment != "" {
		file, err := os.CreateTemp("", "ogp-docker-env-")
		if err != nil {
			return nil, cleanup, fmt.Errorf("create temporary Docker environment file: %w", err)
		}
		cleanup = func() { _ = os.Remove(file.Name()) }
		if _, err := file.WriteString(environment + "\n"); err != nil {
			_ = file.Close()
			cleanup()
			return nil, func(){}, fmt.Errorf("write Docker environment file: %w", err)
		}
		if err := file.Close(); err != nil {
			cleanup()
			return nil, func(){}, fmt.Errorf("close Docker environment file: %w", err)
		}
		args = append(args, "--env-file", file.Name())
	}
	for _, mount := range mounts {
		mountType := "bind"
		if mount.Named {
			mountType = "volume"
		} else {
			info, err := os.Lstat(mount.Source)
			if errors.Is(err, os.ErrNotExist) {
				if err := os.MkdirAll(mount.Source, 0700); err != nil {
					cleanup()
					return nil, func(){}, fmt.Errorf("create host data directory: %w", err)
				}
			} else if err != nil {
				cleanup()
				return nil, func(){}, fmt.Errorf("inspect host data directory: %w", err)
			} else if !info.IsDir() {
				cleanup()
				return nil, func(){}, fmt.Errorf("host volume path %s is not a directory", mount.Source)
			}
		}
		args = append(args, "--mount", "type="+mountType+",src="+mount.Source+",dst="+mount.Target)
	}
	return args, cleanup, nil
}
