package app

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type ResourceUsage struct {
	AppID         int64   `json:"app_id"`
	Available     bool    `json:"available"`
	State         string  `json:"state"`
	CPUPercent    float64 `json:"cpu_percent"`
	MemoryBytes   uint64  `json:"memory_bytes"`
	MemoryLimit   uint64  `json:"memory_limit,omitempty"`
	Tasks         uint64  `json:"tasks"`
	TasksLimit    uint64  `json:"tasks_limit,omitempty"`
	ControlGroup  string  `json:"-"`
	Error         string  `json:"error,omitempty"`
}

type resourceSample struct {
	AppID        int64
	State        string
	ControlGroup string
	CPUUsec      uint64
}

func (m *Manager) ResourceUsage(ctx context.Context, id int64) ResourceUsage {
	app, err := m.Get(id)
	if err != nil {
		return ResourceUsage{AppID: id, Error: err.Error()}
	}
	if app.Type == "static" {
		return ResourceUsage{AppID: id, State: "static"}
	}
	result := m.ResourceUsageMany(ctx, []App{app})
	if len(result) == 0 {
		return ResourceUsage{AppID: id, Error: "resource usage unavailable"}
	}
	return result[0]
}

func (m *Manager) ResourceUsageMany(ctx context.Context, apps []App) []ResourceUsage {
	results := make([]ResourceUsage, len(apps))
	samples := make([]resourceSample, len(apps))

	for i, app := range apps {
		results[i].AppID = app.ID
		if app.Type == "static" {
			results[i].State = "static"
			continue
		}
		sample, err := readServiceSample(ctx, app.ID)
		if err != nil {
			results[i].Error = err.Error()
			continue
		}
		samples[i] = sample
		results[i].State = sample.State
		results[i].ControlGroup = sample.ControlGroup
		if sample.ControlGroup == "" || sample.State != "active" {
			continue
		}
	}

	start := time.Now()
	timer := time.NewTimer(250 * time.Millisecond)
	select {
	case <-ctx.Done():
		timer.Stop()
		return results
	case <-timer.C:
	}
	elapsedUsec := uint64(time.Since(start).Microseconds())
	if elapsedUsec == 0 {
		elapsedUsec = 1
	}

	for i, sample := range samples {
		if sample.ControlGroup == "" || results[i].State != "active" {
			continue
		}
		root, err := cgroupRoot(sample.ControlGroup)
		if err != nil {
			results[i].Error = err.Error()
			continue
		}
		secondCPU, err := readCPUUsec(filepath.Join(root, "cpu.stat"))
		if err != nil {
			results[i].Error = err.Error()
			continue
		}
		if secondCPU >= sample.CPUUsec {
			results[i].CPUPercent = float64(secondCPU-sample.CPUUsec) / float64(elapsedUsec) * 100
		}

		if value, err := readUintFile(filepath.Join(root, "memory.current")); err == nil {
			results[i].MemoryBytes = value
		}
		if value, unlimited, err := readLimitFile(filepath.Join(root, "memory.max")); err == nil && !unlimited {
			results[i].MemoryLimit = value
		}
		if value, err := readUintFile(filepath.Join(root, "pids.current")); err == nil {
			results[i].Tasks = value
		}
		if value, unlimited, err := readLimitFile(filepath.Join(root, "pids.max")); err == nil && !unlimited {
			results[i].TasksLimit = value
		}
		results[i].Available = true
	}
	return results
}

func readServiceSample(ctx context.Context, id int64) (resourceSample, error) {
	out, err := exec.CommandContext(ctx, "systemctl", "show",
		"--property=ActiveState",
		"--property=ControlGroup",
		serviceName(id),
	).CombinedOutput()
	if err != nil {
		return resourceSample{AppID: id}, fmt.Errorf("systemctl show: %w: %s", err, strings.TrimSpace(string(out)))
	}

	sample := resourceSample{AppID: id}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := scanner.Text()
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "ActiveState":
			sample.State = strings.TrimSpace(value)
		case "ControlGroup":
			sample.ControlGroup = strings.TrimSpace(value)
		}
	}
	if err := scanner.Err(); err != nil {
		return sample, err
	}
	if sample.ControlGroup == "" || sample.State != "active" {
		return sample, nil
	}
	root, err := cgroupRoot(sample.ControlGroup)
	if err != nil {
		return sample, err
	}
	sample.CPUUsec, err = readCPUUsec(filepath.Join(root, "cpu.stat"))
	if err != nil {
		return sample, err
	}
	return sample, nil
}

func cgroupRoot(controlGroup string) (string, error) {
	controlGroup = strings.TrimSpace(controlGroup)
	if controlGroup == "" || !strings.HasPrefix(controlGroup, "/") || strings.Contains(controlGroup, "..") {
		return "", errors.New("invalid systemd control group")
	}
	root := filepath.Join("/sys/fs/cgroup", strings.TrimPrefix(filepath.Clean(controlGroup), "/"))
	if root != "/sys/fs/cgroup" && !strings.HasPrefix(root, "/sys/fs/cgroup/") {
		return "", errors.New("invalid cgroup path")
	}
	return root, nil
}

func readCPUUsec(path string) (uint64, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && fields[0] == "usage_usec" {
			return strconv.ParseUint(fields[1], 10, 64)
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return 0, errors.New("usage_usec not found in cpu.stat")
}

func readUintFile(path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
}

func readLimitFile(path string) (uint64, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false, err
	}
	value := strings.TrimSpace(string(data))
	if value == "max" {
		return 0, true, nil
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	return parsed, false, err
}
