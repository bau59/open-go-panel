package docker

import (
	"context"
	"encoding/json"
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

// CPUCount returns the number of logical CPUs reported by the Docker daemon,
// which is the authority enforcing the --cpus upper bound.
func (m *Manager) CPUCount(ctx context.Context) (int, error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return 0, errors.New("Docker is not installed")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "info", "--format", "{{.NCPU}}").CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("read Docker CPU count: %w: %s", err, strings.TrimSpace(string(out)))
	}
	count, err := parseCPUCount(string(out))
	if err != nil {
		return 0, err
	}
	return count, nil
}

func parseCPUCount(raw string) (int, error) {
	count, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || count <= 0 {
		return 0, fmt.Errorf("invalid CPU count returned by Docker: %q", strings.TrimSpace(raw))
	}
	return count, nil
}

// validateAvailableCPUs runs before docker update. Invalid limits never reach
// the daemon; zero retains Docker's unlimited-CPU behavior.
func validateAvailableCPUs(cpuRaw string, available int) error {
	if available <= 0 {
		return errors.New("Docker CPU count is unavailable")
	}
	cpu, err := strconv.ParseFloat(strings.TrimSpace(cpuRaw), 64)
	if err != nil {
		return fmt.Errorf("invalid CPU limit %q: %w", cpuRaw, err)
	}
	if cpu > float64(available) {
		return fmt.Errorf("CPU limit %s exceeds Docker's %d available cores; use 0.5 for 50%% of one core (0 = unlimited)", cpuRaw, available)
	}
	return nil
}

// containerMemoryLimits reads the daemon's current settings immediately before
// an update; form data is never treated as the source of truth.
type containerMemoryLimits struct {
	Memory     int64 `json:"Memory"`
	MemorySwap int64 `json:"MemorySwap"`
}

func inspectContainerMemoryLimits(ctx context.Context, id string) (containerMemoryLimits, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{json .HostConfig}}", id).CombinedOutput()
	if err != nil {
		return containerMemoryLimits{}, fmt.Errorf("inspect Docker memory limits: %w: %s", err, strings.TrimSpace(string(out)))
	}
	var limits containerMemoryLimits
	if err := json.Unmarshal(out, &limits); err != nil {
		return containerMemoryLimits{}, fmt.Errorf("decode Docker memory limits: %w", err)
	}
	if limits.Memory < 0 || limits.MemorySwap < -1 {
		return containerMemoryLimits{}, errors.New("Docker returned invalid memory limits")
	}
	return limits, nil
}

// memoryUpdateArgs preserves the container's existing swap allowance, not the
// old combined RAM+swap ceiling. A finite swap allowance is (MemorySwap-Memory).
// Updating --memory and --memory-swap together avoids Docker rejecting a new
// RAM limit greater than its old combined ceiling.
func memoryUpdateArgs(current containerMemoryLimits, desiredMemory int64) ([]string, error) {
	if desiredMemory < 0 {
		return nil, errors.New("RAM limit must not be negative")
	}
	if current.Memory == desiredMemory {
		return nil, nil // CPU-only edit: never touch memory or swap settings.
	}
	if desiredMemory == 0 {
		return []string{"--memory", "0", "--memory-swap", "0"}, nil
	}
	var newSwap int64
	switch {
	case current.MemorySwap == -1:
		newSwap = -1 // Existing unlimited swap remains unlimited.
	case current.MemorySwap == 0:
		// Mirror Docker's default when RAM is limited and swap was not set.
		newSwap = desiredMemory * 2
	case current.MemorySwap > 0 && current.Memory > 0 && current.MemorySwap >= current.Memory:
		swapAllowance := current.MemorySwap - current.Memory
		if swapAllowance > math.MaxInt64-desiredMemory {
			return nil, errors.New("RAM and swap limit exceeds int64 capacity")
		}
		newSwap = desiredMemory + swapAllowance
	default:
		return nil, fmt.Errorf("cannot safely preserve Docker swap configuration (memory=%d, memoryswap=%d)", current.Memory, current.MemorySwap)
	}
	return []string{"--memory", strconv.FormatInt(desiredMemory, 10), "--memory-swap", strconv.FormatInt(newSwap, 10)}, nil
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
	available, err := m.CPUCount(ctx)
	if err != nil {
		return fmt.Errorf("check Docker CPU availability: %w", err)
	}
	if err := validateAvailableCPUs(cpu, available); err != nil {
		return err
	}
	newMemory, err := strconv.ParseInt(args[3], 10, 64)
	if err != nil {
		return fmt.Errorf("invalid validated RAM limit: %w", err)
	}
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()

	current, err := inspectContainerMemoryLimits(ctx, id)
	if err != nil {
		return err
	}
	memoryArgs, err := memoryUpdateArgs(current, newMemory)
	if err != nil {
		return err
	}
	updateArgs := append([]string{"update"}, args[:2]...)
	updateArgs = append(updateArgs, memoryArgs...)
	return dockerCommand(ctx, append(updateArgs, id)...)
}
