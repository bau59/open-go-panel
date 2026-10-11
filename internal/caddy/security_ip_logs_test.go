package caddy

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestQueryIPAccessLogsExactMatchAndNoRawHeaders(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0755); err != nil { t.Fatal(err) }
	fixture := filepath.Join(dir, "events.jsonl")
	argsPath := filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CAPTURE_ARGS\"\ncat \"$LOG_FIXTURE\"\n"
	if err := os.WriteFile(filepath.Join(bin, "journalctl"), []byte(script), 0755); err != nil { t.Fatal(err) }
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("LOG_FIXTURE", fixture)
	t.Setenv("CAPTURE_ARGS", argsPath)
	var lines []string
	for _, tc := range []struct{ ip, method, uri string; status int }{
		{"203.0.113.10", "GET", "/not-the-same-ip", 404},
		{"203.0.113.1", "GET", "/.env?name=1", 404},
		{"203.0.113.1", "POST", "/wp-login.php", 403},
		{"203.0.113.1", "GET", "/normal", 200},
	} {
		data := map[string]any{
			"logger": "http.log.access", "msg": "handled request", "status": tc.status,
			"ts": 1791700000.0,
			"request": map[string]any{"client_ip": tc.ip, "remote_ip": tc.ip,
				"host": "site.example", "method": tc.method, "uri": tc.uri,
				"headers": map[string]any{"Authorization": []string{"SECRET"}}},
		}
		event, _ := json.Marshal(data)
		wrapper, _ := json.Marshal(map[string]any{"MESSAGE": string(event)})
		lines = append(lines, string(wrapper))
	}
	if err := os.WriteFile(fixture, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil { t.Fatal(err) }
	m := &Manager{}
	got, err := m.QueryIPAccessLogs(context.Background(), "203.0.113.1", "24h")
	if err != nil { t.Fatal(err) }
	if len(got.Entries) != 3 { t.Fatalf("want 3 matching requests, got %d: %+v", len(got.Entries), got.Entries) }
	if got.Entries[0].URI != "/.env?name=1" || got.Entries[1].Status != 403 {
		t.Fatalf("incorrect request parsing: %+v", got.Entries)
	}
	for _, entry := range got.Entries {
		if entry.ClientIP != "203.0.113.1" || entry.Raw != "" || entry.Message != "" {
			t.Fatalf("wrong IP or raw data leaked: %+v", entry)
		}
	}
	args, err := os.ReadFile(argsPath)
	if err != nil { t.Fatal(err) }
	if !strings.Contains(string(args), "--grep\n203\\.0\\.113\\.1") || !strings.Contains(string(args), "24 hours ago") {
		t.Fatalf("expected escaped IP journal filter and bounded window, got: %q", args)
	}
	for _, tc := range []struct{ip, window string}{
		{"bogus; rm -rf /", "24h"}, {"203.0.113.1", "500d"},
	} {
		if _, err := m.QueryIPAccessLogs(context.Background(), tc.ip, tc.window); err == nil {
			t.Fatalf("accepted invalid query: %+v", tc)
		}
	}
}
