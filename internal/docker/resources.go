package docker

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const maxDockerLogBytes = 256 << 10

var cpuLimitRE = regexp.MustCompile(`^(0|[1-9][0-9]{0,2})(\.[0-9]{1,3})?$`)

// tailOutput caps memory while retaining the most recent log bytes.
type tailOutput struct {
	buf       []byte
	truncated bool
}

func (o *tailOutput) Write(p []byte) (int, error) {
	n := len(p)
	if n >= maxDockerLogBytes {
		o.buf = append(o.buf[:0], p[n-maxDockerLogBytes:]...)
		o.truncated = true
		return n, nil
	}
	if len(o.buf)+n > maxDockerLogBytes {
		drop := len(o.buf) + n - maxDockerLogBytes
		o.buf = append(o.buf[:0], o.buf[drop:]...)
		o.truncated = true
	}
	o.buf = append(o.buf, p...)
	return n, nil
}

func (o *tailOutput) String() string {
	result := strings.ToValidUTF8(string(o.buf), "�")
	if o.truncated {
		return "[Output truncated to the last 256 KiB]\n" + result
	}
	return result
}

// Logs reads existing Docker logs on demand. It never follows, polls or stores
// them, and bounds the number of lines, output bytes and execution time.
func (m *Manager) Logs(ctx context.Context, id string, lines int) (string, error) {
	if !containerNamePattern.MatchString(id) {
		return "", errors.New("invalid container ID")
	}
	if lines < 1 || lines > 1000 {
		return "", errors.New("log tail must be between 1 and 1000 lines")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return "", errors.New("Docker is not installed")
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "logs", "--timestamps", "--tail", strconv.Itoa(lines), id)
	var output tailOutput
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("read container logs: %w", ctx.Err())
		}
		return "", fmt.Errorf("docker logs: %w: %s", err, strings.TrimSpace(output.String()))
	}
	return output.String(), nil
}

// parseLimitArgs validates the two user-controlled resource settings.
// Zero removes a limit; memory is entered as whole MiB and passed to Docker
// as an exact number of bytes.
func parseLimitArgs(cpuRaw, memoryRaw string) ([]string, error) {
	cpuRaw = strings.TrimSpace(cpuRaw)
	memoryRaw = strings.TrimSpace(memoryRaw)
	if !cpuLimitRE.MatchString(cpuRaw) {
		return nil, errors.New("CPU limit must be 0 (unlimited) or a decimal number of cores")
	}
	cpu, err := strconv.ParseFloat(cpuRaw, 64)
	if err != nil || math.IsInf(cpu, 0) || math.IsNaN(cpu) || cpu > 256 || (cpu > 0 && cpu < 0.01) {
		return nil, errors.New("CPU limit must be 0 or between 0.01 and 256 cores")
	}
	if memoryRaw == "" {
		return nil, errors.New("RAM limit must be specified (0 means unlimited)")
	}
	mib, err := strconv.ParseInt(memoryRaw, 10, 64)
	if err != nil || mib < 0 || mib > 1048576 || (mib > 0 && mib < 6) {
		return nil, errors.New("RAM limit must be 0 or between 6 and 1048576 MiB")
	}
	return []string{"--cpus", cpuRaw, "--memory", strconv.FormatInt(mib*1048576, 10)}, nil
}

// UpdateLimits changes resource limits without recreating or restarting a
// container. Docker remains the source of truth; no local shadow state.
func (m *Manager) UpdateLimits(ctx context.Context, id, cpu, memoryMiB string) error {
	if !containerNamePattern.MatchString(id) {
		return errors.New("invalid container ID")
	}
	args, err := parseLimitArgs(cpu, memoryMiB)
	if err != nil {
		return err
	}
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	return dockerCommand(ctx, append(append([]string{"update"}, args...), id)...)
}
