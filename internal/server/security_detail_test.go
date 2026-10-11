package server

import (
	"strings"
	"testing"
	"time"

	"github.com/bau59/open-go-panel/internal/caddy"
	"github.com/bau59/open-go-panel/internal/security"
)

func TestSecurityIPPageEscapesAccessLogContent(t *testing.T) {
	page := securityIPPage("203.0.113.1", "24h", caddy.LogResult{Entries: []caddy.LogEntry{{
		Time: time.Date(2026, 10, 11, 7, 0, 0, 0, time.UTC),
		Domain: "<img src=x onerror=alert(1)>",
		URI: "/?q=<script>alert(1)</script>",
		Method: "GET", Status: 404, DurationMS: 3,
	}}}, "")
	if strings.Contains(page, "<script>alert(1)</script>") || strings.Contains(page, "<img src=x") {
		t.Fatal("unescaped Caddy log entry")
	}
	for _, expected := range []string{"&lt;script&gt;", "203.0.113.1", "Domains in this sample", "Search logs"} {
		if !strings.Contains(page, expected) { t.Errorf("missing %q", expected) }
	}
}

func TestSecurityProfilesPageEscapesYAML(t *testing.T) {
	page := securityProfilesPage(security.ProfilesFile{
		Kind: "main", Path: "/etc/crowdsec/profiles.yaml", Content: "</textarea><script>alert(1)</script>",
		Version: "abcdef", Exists: true,
	}, "")
	if strings.Contains(page, "</textarea><script>") { t.Fatal("profile content escapes the editor") }
	if !strings.Contains(page, "&lt;/textarea&gt;") || !strings.Contains(page, "name=\"version\" value=\"abcdef\"") {
		t.Fatal("profile content or concurrency token missing")
	}
}
