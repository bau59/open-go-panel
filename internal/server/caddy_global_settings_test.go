package server

import (
	"strings"
	"testing"

	panelcaddy "github.com/bau59/open-go-panel/internal/caddy"
)

func TestCaddyPageRendersAdditionalGlobalSettings(t *testing.T) {
	settings := panelcaddy.GlobalSettings{
		HTTPS: true, Compression: true, AccessLog: true,
		SecurityHeaders: true, FrameProtection: true, HSTS: true,
		ProxyDialTimeoutSeconds: 5, ProxyHeaderTimeoutSeconds: 30,
	}
	page := caddyPage("active", nil, "", "", settings, 0, "")
	for _, field := range []string{
		`name="security_headers"`,
		`name="frame_protection"`,
		`name="hsts"`,
		`name="proxy_dial_timeout"`,
		`name="proxy_header_timeout"`,
		`<option value="5" selected>5 seconds</option>`,
		`<option value="30" selected>30 seconds</option>`,
		"X-Frame-Options: SAMEORIGIN",
	} {
		if !strings.Contains(page, field) {
			t.Errorf("missing Caddy setting %q", field)
		}
	}
}

func TestCaddyPageRendersNewSettingsDisabledByDefault(t *testing.T) {
	page := caddyPage("active", nil, "", "", panelcaddy.GlobalSettings{
		HTTPS: true, Compression: true, AccessLog: true,
	}, 0, "")
	for _, field := range []string{"security_headers", "frame_protection", "hsts"} {
		if !strings.Contains(page, `name="`+field+`"`) {
			t.Errorf("missing switch %q", field)
		}
	}
	if !strings.Contains(page, "Caddy default (3 seconds)") {
		t.Fatal("connection timeout default is not labeled")
	}
}

func TestCaddyPagePrioritizesDomainList(t *testing.T) {
	page := caddyPage("active", nil, "", "", panelcaddy.GlobalSettings{HTTPS: true}, 0, "")
	domains := strings.Index(page, "<h2>Domains</h2>")
	global := strings.Index(page, "<h2>Global defaults</h2>")
	if domains < 0 || global < 0 || domains > global {
		t.Fatalf("domains should precede global defaults: domains=%d global=%d", domains, global)
	}
}

func TestCaddyPageWarnsBeforeReplacingCustomGlobalTemplate(t *testing.T) {
	settings := panelcaddy.GlobalSettings{HTTPS: true, Compression: true, AccessLog: true}
	page := caddyPage("active", nil, "", "{domain} {\n respond \"custom\" 200\n}", settings, 0, "")
	for _, required := range []string{
		"Custom global template detected.",
		"Replace the custom global Caddy template with generated defaults?",
	} {
		if !strings.Contains(page, required) {
			t.Errorf("custom template safety warning missing %q", required)
		}
	}
}
