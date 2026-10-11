package caddy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os/exec"
	"regexp"
	"strings"
)

// QueryIPAccessLogs searches existing Caddy journald entries only on request.
// It does not acquire, record or persist any additional HTTP events.
// journalctl prefilters by IP, followed by exact parsed client-IP matching.
func (m *Manager) QueryIPAccessLogs(ctx context.Context, ip, window string) (LogResult, error) {
	address, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil || address.Zone() != "" {
		return LogResult{}, fmt.Errorf("invalid client IP address")
	}
	if _, err := exec.LookPath("journalctl"); err != nil {
		return LogResult{}, fmt.Errorf("journalctl is not installed")
	}
	since := "24 hours ago"
	switch window {
	case "24h", "":
	case "7d":
		since = "7 days ago"
	case "30d":
		since = "30 days ago"
	default:
		return LogResult{}, fmt.Errorf("unsupported log period")
	}

	// An explicit bound avoids excessively large on-demand investigations.
	const maximum = 200
	args := []string{"--no-pager", "-r", "-o", "json", "-u", "caddy.service",
		"--since", since, "--grep", regexp.QuoteMeta(address.String())}
	commandCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, "journalctl", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return LogResult{}, err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return LogResult{}, err
	}

	result := LogResult{Entries: make([]LogEntry, 0, 50)}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	enough := false
	for scanner.Scan() {
		var envelope journalEnvelope
		if err := json.Unmarshal(scanner.Bytes(), &envelope); err != nil {
			continue
		}
		entry, ok := parseCaddyLog(envelope.Message)
		if !ok || entry.Kind != "access" {
			continue
		}
		found, err := netip.ParseAddr(entry.ClientIP)
		if err != nil || found.Unmap() != address.Unmap() {
			continue
		}
		// Never send raw JSON or HTTP headers to the investigation page.
		entry.Raw = ""
		entry.Message = ""
		result.Entries = append(result.Entries, entry)
		if len(result.Entries) > maximum {
			result.HasNext = true
			result.Entries = result.Entries[:maximum]
			enough = true
			cancel()
			break
		}
	}
	scanErr := scanner.Err()
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return LogResult{}, ctx.Err()
	}
	if scanErr != nil {
		return LogResult{}, scanErr
	}
	if waitErr != nil && !enough {
		return LogResult{}, fmt.Errorf("journalctl: %w: %s", waitErr, strings.TrimSpace(stderr.String()))
	}
	result.PerPage = maximum
	return result, nil
}
