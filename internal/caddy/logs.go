package caddy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type LogQuery struct {
	Domain  string
	Search  string
	Kind    string
	Since   string
	Until   string
	Page    int
	PerPage int
}

type LogEntry struct {
	Time       time.Time
	Level      string
	Kind       string
	Domain     string
	Method     string
	URI        string
	Status     int
	ClientIP   string
	Protocol   string
	DurationMS float64
	Size       int64
	Message    string
	Raw        string
}

type LogResult struct {
	Entries []LogEntry
	Page    int
	PerPage int
	HasNext bool
}

type journalEnvelope struct {
	Message string `json:"MESSAGE"`
}

type caddyLogMessage struct {
	Level    string  `json:"level"`
	Ts       float64 `json:"ts"`
	Logger   string  `json:"logger"`
	Msg      string  `json:"msg"`
	Status   int     `json:"status"`
	Duration float64 `json:"duration"`
	Size     int64   `json:"size"`
	Request  struct {
		RemoteIP string `json:"remote_ip"`
		ClientIP string `json:"client_ip"`
		Method   string `json:"method"`
		Host     string `json:"host"`
		URI      string `json:"uri"`
		Proto    string `json:"proto"`
	} `json:"request"`
}

func (m *Manager) QueryStructuredLogs(ctx context.Context, q LogQuery) (LogResult, error) {
	if _, err := exec.LookPath("journalctl"); err != nil {
		return LogResult{}, fmt.Errorf("journalctl is not installed")
	}
	if q.Page < 1 {
		q.Page = 1
	}
	switch q.PerPage {
	case 25, 50, 100, 200:
	default:
		q.PerPage = 100
	}
	switch q.Kind {
	case "", "all", "access", "error":
	default:
		q.Kind = "all"
	}

	args := []string{"--no-pager", "-r", "-o", "json", "-u", "caddy.service"}
	if strings.TrimSpace(q.Since) != "" {
		args = append(args, "--since", q.Since)
	}
	if strings.TrimSpace(q.Until) != "" {
		args = append(args, "--until", q.Until)
	}
	if domain := strings.TrimSpace(q.Domain); domain != "" {
		args = append(args, "--grep", domain, "--case-sensitive=false")
	} else if search := strings.TrimSpace(q.Search); search != "" {
		args = append(args, "--grep", search, "--case-sensitive=false")
	}

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

	skip := (q.Page - 1) * q.PerPage
	needed := q.PerPage + 1
	matched := 0
	entries := make([]LogEntry, 0, needed)
	search := strings.ToLower(strings.TrimSpace(q.Search))
	domain := strings.ToLower(strings.TrimSpace(q.Domain))

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	enough := false
	for scanner.Scan() {
		var envelope journalEnvelope
		if err := json.Unmarshal(scanner.Bytes(), &envelope); err != nil || strings.TrimSpace(envelope.Message) == "" {
			continue
		}
		entry, ok := parseCaddyLog(envelope.Message)
		if !ok {
			continue
		}
		if domain != "" && strings.ToLower(entry.Domain) != domain {
			continue
		}
		if q.Kind == "access" && entry.Kind != "access" {
			continue
		}
		if q.Kind == "error" && entry.Kind != "error" {
			continue
		}
		if search != "" && !caddyLogMatches(entry, search) {
			continue
		}
		if matched < skip {
			matched++
			continue
		}
		entries = append(entries, entry)
		matched++
		if len(entries) >= needed {
			enough = true
			cancel()
			break
		}
	}
	scanErr := scanner.Err()
	waitErr := cmd.Wait()
	if scanErr != nil && ctx.Err() == nil {
		return LogResult{}, scanErr
	}
	if waitErr != nil && !enough && ctx.Err() == nil {
		return LogResult{}, fmt.Errorf("journalctl: %w: %s", waitErr, strings.TrimSpace(stderr.String()))
	}

	hasNext := len(entries) > q.PerPage
	if hasNext {
		entries = entries[:q.PerPage]
	}
	return LogResult{Entries: entries, Page: q.Page, PerPage: q.PerPage, HasNext: hasNext}, nil
}

func parseCaddyLog(raw string) (LogEntry, bool) {
	start := strings.IndexByte(raw, '{')
	if start < 0 {
		return LogEntry{}, false
	}
	rawJSON := strings.TrimSpace(raw[start:])
	var event caddyLogMessage
	if err := json.Unmarshal([]byte(rawJSON), &event); err != nil {
		return LogEntry{}, false
	}
	if !strings.HasPrefix(event.Logger, "http.log.") {
		return LogEntry{}, false
	}

	kind := "access"
	if strings.Contains(event.Logger, ".error") {
		kind = "error"
	}
	clientIP := event.Request.ClientIP
	if clientIP == "" {
		clientIP = event.Request.RemoteIP
	}

	var eventTime time.Time
	if event.Ts > 0 {
		seconds := int64(event.Ts)
		nanos := int64((event.Ts - float64(seconds)) * float64(time.Second))
		eventTime = time.Unix(seconds, nanos).UTC()
	}

	message := event.Msg
	if kind == "access" && message == "handled request" {
		message = ""
	}

	return LogEntry{
		Time:       eventTime,
		Level:      event.Level,
		Kind:       kind,
		Domain:     event.Request.Host,
		Method:     event.Request.Method,
		URI:        event.Request.URI,
		Status:     event.Status,
		ClientIP:   clientIP,
		Protocol:   event.Request.Proto,
		DurationMS: event.Duration * 1000,
		Size:       event.Size,
		Message:    message,
		Raw:        rawJSON,
	}, true
}

func caddyLogMatches(entry LogEntry, search string) bool {
	fields := []string{
		entry.Domain,
		entry.Method,
		entry.URI,
		entry.ClientIP,
		entry.Protocol,
		entry.Level,
		entry.Kind,
		entry.Message,
		strconv.Itoa(entry.Status),
	}
	for _, field := range fields {
		if strings.Contains(strings.ToLower(field), search) {
			return true
		}
	}
	return false
}
