package dbmanager

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

type RedisMetrics struct {
	Version          string
	UptimeSeconds    int64
	UsedMemoryBytes  int64
	PeakMemoryBytes  int64
	ConnectedClients int64
	TotalKeys        int64
}

type RedisKey struct {
	Name        string
	Type        string
	TTLSeconds  int64
	MemoryBytes int64
}

type RedisScanResult struct {
	Cursor string
	Keys   []RedisKey
}

type RedisKeyDetail struct {
	Key         RedisKey
	Preview     string
	Truncated   bool
}

func (m *Manager) RedisMetrics(ctx context.Context) (RedisMetrics, error) {
	if _, err := exec.LookPath("redis-cli"); err != nil {
		return RedisMetrics{}, errors.New("redis-cli is not installed")
	}
	out, err := exec.CommandContext(ctx, "redis-cli", "--raw", "INFO").CombinedOutput()
	if err != nil {
		return RedisMetrics{}, fmt.Errorf("read Redis INFO: %w: %s", err, strings.TrimSpace(string(out)))
	}
	values := make(map[string]string)
	var totalKeys int64
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		values[key] = value
		if strings.HasPrefix(key, "db") {
			for _, part := range strings.Split(value, ",") {
				if strings.HasPrefix(part, "keys=") {
					n, _ := strconv.ParseInt(strings.TrimPrefix(part, "keys="), 10, 64)
					totalKeys += n
				}
			}
		}
	}
	parse := func(key string) int64 {
		n, _ := strconv.ParseInt(values[key], 10, 64)
		return n
	}
	return RedisMetrics{
		Version:          values["redis_version"],
		UptimeSeconds:    parse("uptime_in_seconds"),
		UsedMemoryBytes:  parse("used_memory"),
		PeakMemoryBytes:  parse("used_memory_peak"),
		ConnectedClients: parse("connected_clients"),
		TotalKeys:        totalKeys,
	}, nil
}

func (m *Manager) RedisScan(ctx context.Context, db int, cursor, query string, count int) (RedisScanResult, error) {
	if db < 0 || db > 1024 {
		return RedisScanResult{}, errors.New("invalid Redis database index")
	}
	if cursor == "" {
		cursor = "0"
	}
	if count < 1 {
		count = 50
	}
	if count > 200 {
		count = 200
	}
	args := []string{"--raw", "-n", strconv.Itoa(db), "SCAN", cursor}
	if query = strings.TrimSpace(query); query != "" {
		pattern := query
		if !strings.ContainsAny(pattern, "*?[") {
			pattern = "*" + pattern + "*"
		}
		args = append(args, "MATCH", pattern)
	}
	args = append(args, "COUNT", strconv.Itoa(count))
	out, err := exec.CommandContext(ctx, "redis-cli", args...).CombinedOutput()
	if err != nil {
		return RedisScanResult{}, fmt.Errorf("Redis SCAN failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return RedisScanResult{}, errors.New("Redis SCAN returned no cursor")
	}
	result := RedisScanResult{Cursor: strings.TrimSpace(lines[0])}
	for _, key := range lines[1:] {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		info := RedisKey{Name: key, TTLSeconds: -2}
		if value, err := redisRaw(ctx, db, "TYPE", key); err == nil {
			info.Type = strings.TrimSpace(value)
		}
		if value, err := redisRaw(ctx, db, "TTL", key); err == nil {
			info.TTLSeconds, _ = strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		}
		if value, err := redisRaw(ctx, db, "MEMORY", "USAGE", key); err == nil {
			info.MemoryBytes, _ = strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		}
		result.Keys = append(result.Keys, info)
	}
	return result, nil
}

func (m *Manager) RedisKeyDetail(ctx context.Context, db int, key string) (RedisKeyDetail, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return RedisKeyDetail{}, errors.New("Redis key is required")
	}
	typeName, err := redisRaw(ctx, db, "TYPE", key)
	if err != nil {
		return RedisKeyDetail{}, err
	}
	typeName = strings.TrimSpace(typeName)
	if typeName == "none" {
		return RedisKeyDetail{}, errors.New("Redis key does not exist")
	}
	ttlRaw, _ := redisRaw(ctx, db, "TTL", key)
	memoryRaw, _ := redisRaw(ctx, db, "MEMORY", "USAGE", key)
	ttl, _ := strconv.ParseInt(strings.TrimSpace(ttlRaw), 10, 64)
	memory, _ := strconv.ParseInt(strings.TrimSpace(memoryRaw), 10, 64)

	var preview string
	truncated := false
	switch typeName {
	case "string":
		preview, err = redisRaw(ctx, db, "GET", key)
		if len(preview) > 64*1024 {
			preview = preview[:64*1024]
			truncated = true
		}
	case "hash":
		preview, err = redisRaw(ctx, db, "HSCAN", key, "0", "COUNT", "100")
	case "list":
		preview, err = redisRaw(ctx, db, "LRANGE", key, "0", "99")
	case "set":
		preview, err = redisRaw(ctx, db, "SSCAN", key, "0", "COUNT", "100")
	case "zset":
		preview, err = redisRaw(ctx, db, "ZRANGE", key, "0", "99", "WITHSCORES")
	case "stream":
		preview, err = redisRaw(ctx, db, "XRANGE", key, "-", "+", "COUNT", "100")
	default:
		preview = "Preview is not available for Redis type " + typeName + "."
	}
	if err != nil {
		return RedisKeyDetail{}, err
	}
	return RedisKeyDetail{
		Key: RedisKey{Name: key, Type: typeName, TTLSeconds: ttl, MemoryBytes: memory},
		Preview: strings.TrimRight(preview, "\n"),
		Truncated: truncated,
	}, nil
}

func (m *Manager) RedisDeleteKey(ctx context.Context, db int, key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("Redis key is required")
	}
	_, err := redisRaw(ctx, db, "DEL", key)
	return err
}

func (m *Manager) RedisFlushAll(ctx context.Context) error {
	_, err := redisRaw(ctx, 0, "FLUSHALL", "ASYNC")
	return err
}

func redisRaw(ctx context.Context, db int, args ...string) (string, error) {
	if _, err := exec.LookPath("redis-cli"); err != nil {
		return "", errors.New("redis-cli is not installed")
	}
	base := []string{"--raw", "-n", strconv.Itoa(db)}
	base = append(base, args...)
	out, err := exec.CommandContext(ctx, "redis-cli", base...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("redis-cli %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
