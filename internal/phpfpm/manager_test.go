package phpfpm

import (
	"strings"
	"testing"
)

func TestPoolConfigUsesDedicatedIdentityAndSocket(t *testing.T) {
	value := poolConfig(42, "siteuser", "sitegroup")
	for _, expected := range []string{
		"[ogp-app-42]", "user = siteuser", "group = sitegroup",
		"listen = /run/php/ogp-app-42.sock",
		"listen.owner = caddy", "listen.mode = 0660",
		"pm = ondemand", "security.limit_extensions = .php",
	} {
		if !strings.Contains(value, expected) {
			t.Errorf("missing %q in PHP-FPM pool", expected)
		}
	}
}

func TestVersionLess(t *testing.T) {
	if !versionLess("8.3", "8.4") || !versionLess("8.4", "8.10") {
		t.Fatal("PHP version ordering must be numeric")
	}
	if versionLess("8.4", "8.3") || versionLess("8.4", "8.4") {
		t.Fatal("unexpected version comparison")
	}
}
