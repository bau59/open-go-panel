package systeminfo

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Info struct {
	Hostname       string
	OS             string
	Kernel         string
	CPUs           int
	Load1          string
	Load5          string
	Load15         string
	MemoryUsed     uint64
	MemoryTotal    uint64
	MemoryPercent  float64
	DiskUsed       uint64
	DiskTotal      uint64
	DiskPercent    float64
	Uptime         time.Duration
}

func Read() (Info, error) {
	info := Info{CPUs: runtime.NumCPU()}

	if hostname, err := os.Hostname(); err == nil {
		info.Hostname = hostname
	}

	if data, err := os.ReadFile("/etc/os-release"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				info.OS = strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), """)
				break
			}
		}
	}

	var uname syscall.Utsname
	if err := syscall.Uname(&uname); err == nil {
		info.Kernel = charsToString(uname.Release[:])
	}

	if data, err := os.ReadFile("/proc/loadavg"); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) >= 3 {
			info.Load1, info.Load5, info.Load15 = fields[0], fields[1], fields[2]
		}
	}

	if err := readMemory(&info); err != nil {
		return Info{}, err
	}

	var fs syscall.Statfs_t
	if err := syscall.Statfs("/", &fs); err == nil {
		info.DiskTotal = fs.Blocks * uint64(fs.Bsize)
		free := fs.Bavail * uint64(fs.Bsize)
		info.DiskUsed = info.DiskTotal - free
		if info.DiskTotal > 0 {
			info.DiskPercent = float64(info.DiskUsed) / float64(info.DiskTotal) * 100
		}
	}

	if data, err := os.ReadFile("/proc/uptime"); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) > 0 {
			if seconds, err := strconv.ParseFloat(fields[0], 64); err == nil {
				info.Uptime = time.Duration(seconds * float64(time.Second))
			}
		}
	}

	return info, nil
}

func readMemory(info *Info) error {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return fmt.Errorf("open /proc/meminfo: %w", err)
	}
	defer file.Close()

	values := map[string]uint64{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		if key != "MemTotal" && key != "MemAvailable" {
			continue
		}
		v, err := strconv.ParseUint(fields[1], 10, 64)
		if err == nil {
			values[key] = v * 1024
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read /proc/meminfo: %w", err)
	}

	info.MemoryTotal = values["MemTotal"]
	available := values["MemAvailable"]
	if info.MemoryTotal >= available {
		info.MemoryUsed = info.MemoryTotal - available
	}
	if info.MemoryTotal > 0 {
		info.MemoryPercent = float64(info.MemoryUsed) / float64(info.MemoryTotal) * 100
	}

	return nil
}

func charsToString(chars []int8) string {
	var b strings.Builder
	for _, c := range chars {
		if c == 0 {
			break
		}
		b.WriteByte(byte(c))
	}
	return b.String()
}
