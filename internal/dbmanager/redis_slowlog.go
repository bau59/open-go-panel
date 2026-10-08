package dbmanager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type RedisSlowQuery struct {
	Time       time.Time
	DurationUS int64
	Command    string
	Client     string
}

// Redis SLOWLOG is global to a Redis instance and does not expose the logical DB number.
func (m *Manager) RedisSlowQueries(ctx context.Context) ([]RedisSlowQuery, error) {
	out, err := exec.CommandContext(ctx, "redis-cli", "--json", "SLOWLOG", "GET", "100").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("read Redis SLOWLOG: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return parseRedisSlowQueries(out)
}

func (m *Manager) RedisSlowLogLength(ctx context.Context) (int, error) {
	out, err := exec.CommandContext(ctx, "redis-cli", "--raw", "CONFIG", "GET", "slowlog-max-len").CombinedOutput()
	if err != nil { return 0, fmt.Errorf("read Redis slowlog-max-len: %w: %s", err, strings.TrimSpace(string(out))) }
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 { return 0, errors.New("unexpected Redis SLOWLOG configuration") }
	value, err := strconv.Atoi(strings.TrimSpace(lines[len(lines)-1]))
	if err != nil { return 0, err }
	return value, nil
}

func (m *Manager) SetRedisSlowLogLength(ctx context.Context, length int) error {
	switch length { case 128, 256, 512, 1024: default: return errors.New("invalid Redis slowlog-max-len") }
	out, err := exec.CommandContext(ctx, "redis-cli", "CONFIG", "SET", "slowlog-max-len", strconv.Itoa(length)).CombinedOutput()
	if err != nil { return fmt.Errorf("configure Redis slowlog-max-len: %w: %s", err, strings.TrimSpace(string(out))) }
	// Redis may prevent CONFIG REWRITE when started without a writable config file.
	// In that situation surface the failure rather than claiming persistent settings.
	out, err = exec.CommandContext(ctx, "redis-cli", "CONFIG", "REWRITE").CombinedOutput()
	if err != nil { return fmt.Errorf("Redis limit applied for this process, but persistence failed: %w: %s", err, strings.TrimSpace(string(out))) }
	return nil
}

func parseRedisSlowQueries(data []byte) ([]RedisSlowQuery, error) {
	var records [][]json.RawMessage
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("decode Redis SLOWLOG: %w", err)
	}
	entries := make([]RedisSlowQuery, 0, len(records))
	for _, record := range records {
		if len(record) < 4 {
			return nil, errors.New("unexpected Redis SLOWLOG format")
		}
		var stamp, duration int64
		if err := json.Unmarshal(record[1], &stamp); err != nil { return nil, err }
		if err := json.Unmarshal(record[2], &duration); err != nil { return nil, err }
		var args []string
		if err := json.Unmarshal(record[3], &args); err != nil { return nil, err }
		var client string
		if len(record) > 4 {
			_ = json.Unmarshal(record[4], &client)
		}
		// Keep a bounded preview: Redis command arguments may contain secrets or
		// very large values, so only expose a short excerpt in the administrator UI.
		command := strings.Join(args, " ")
		if len(command) > 2048 { command = command[:2048] + "…" }
		entries = append(entries, RedisSlowQuery{
			Time: time.Unix(stamp, 0), DurationUS: duration,
			Command: command, Client: client,
		})
	}
	return entries, nil
}

