package caddy

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/bau59/open-go-panel/internal/state"
)

func TestCaddyGeneratedDefaultsRemainCompatible(t *testing.T) {
	settings := defaultGlobalSettings()
	got := managedProxyTemplate(settings)
	if got != recommendedSiteTemplate {
		t.Fatalf("default managed proxy template changed:\n%s", got)
	}
	for _, unexpected := range []string{"header {", "transport http {", "response_header_timeout", "dial_timeout"} {
		if strings.Contains(got, unexpected) {
			t.Errorf("unexpected new directive %q in unchanged defaults", unexpected)
		}
	}
}

func TestCaddySecurityAndTimeoutTemplates(t *testing.T) {
	settings := defaultGlobalSettings()
	settings.SecurityHeaders = true
	settings.FrameProtection = true
	settings.HSTS = true
	settings.ProxyDialTimeoutSeconds = 5
	settings.ProxyHeaderTimeoutSeconds = 30

	if err := validateGlobalSettings(settings); err != nil {
		t.Fatalf("valid settings rejected: %v", err)
	}
	got := managedProxyTemplate(settings)
	for _, expected := range []string{
		"X-Content-Type-Options nosniff",
		"Referrer-Policy strict-origin-when-cross-origin",
		"X-Frame-Options SAMEORIGIN",
		"Strict-Transport-Security",
		"dial_timeout 5s",
		"response_header_timeout 30s",
	} {
		if !strings.Contains(got, expected) {
			t.Errorf("proxy template missing %q:\n%s", expected, got)
		}
	}
	site := Site{AppID: -1, Domain: "unused.example.com", Kind: "parked"}
	static := managedStaticTemplate(settings)
	parked := ManagedSiteTemplate(site, settings)
	if strings.Contains(static, "reverse_proxy") || !strings.Contains(static, "X-Frame-Options SAMEORIGIN") {
		t.Errorf("static site security headers incorrect:\n%s", static)
	}
	if !strings.Contains(parked, "Strict-Transport-Security") || !strings.Contains(parked, "respond") {
		t.Errorf("parked domain headers incorrect:\n%s", parked)
	}
}

func TestCaddyHTTPSAndHSTSRestrictions(t *testing.T) {
	settings := defaultGlobalSettings()
	settings.HTTPS = false
	settings.HSTS = true
	if err := validateGlobalSettings(settings); err == nil {
		t.Fatal("expected HSTS without HTTPS to be rejected")
	}
	settings.HSTS = false
	settings.ProxyDialTimeoutSeconds = -1
	if err := validateGlobalSettings(settings); err == nil {
		t.Fatal("expected invalid proxy dial timeout to be rejected")
	}
	settings.ProxyDialTimeoutSeconds = 0
	settings.ProxyHeaderTimeoutSeconds = 999
	if err := validateGlobalSettings(settings); err == nil {
		t.Fatal("expected invalid response header timeout to be rejected")
	}
	settings.ProxyHeaderTimeoutSeconds = 0
	if err := validateGlobalSettings(settings); err != nil {
		t.Fatalf("HTTP-only defaults unexpectedly rejected: %v", err)
	}
	site := Site{Kind: "redirect", Root: "https://other.example"}
	rendered := ManagedSiteTemplate(site, settings)
	if !strings.HasPrefix(rendered, "http://{domain} {") || strings.Contains(rendered, "Strict-Transport-Security") {
		t.Fatalf("HTTP-only redirect config incorrect:\n%s", rendered)
	}
}

func TestCaddyGlobalSettingsPersistence(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	settings := defaultGlobalSettings()
	for key, value := range map[string]string{
		"caddy.security_headers": "1",
		"caddy.frame_protection": "1",
		"caddy.hsts": "1",
		"caddy.proxy_dial_timeout": "10",
		"caddy.proxy_header_timeout": "60",
	} {
		if err := store.SetSetting(key, value); err != nil {
			t.Fatal(err)
		}
	}
	manager := New(store, filepath.Join(t.TempDir(), "legacy.json"))
	loaded, err := manager.GlobalSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.HTTPS || !loaded.SecurityHeaders || !loaded.FrameProtection || !loaded.HSTS ||
		loaded.ProxyDialTimeoutSeconds != 10 || loaded.ProxyHeaderTimeoutSeconds != 60 {
		t.Fatalf("unexpected settings loaded from SQLite: %+v", loaded)
	}
}
