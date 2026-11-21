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
		"php_admin_flag[log_errors] = on", "php_admin_flag[display_errors] = off",
		"php_admin_value[error_log] = /var/log/open-go-panel/php/app-42.log",
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


func TestVersionedPHPPoolsUseDistinctSockets(t *testing.T) {
    old := Socket(17)
    legacy := SocketForVersion(17, "7.4")
    modern := SocketForVersion(17, "8.3")
    if old != "/run/php/ogp-app-17.sock" {
        t.Fatalf("historical socket changed: %s", old)
    }
    if legacy == old || modern == old || legacy == modern {
        t.Fatalf("PHP versions share socket: %q, %q, %q", old, legacy, modern)
    }
    config := poolConfigForVersion(17, "siteuser", "sitegroup", "7.4")
    for _, want := range []string{
        "[ogp-app-17-php74]", "listen = "+legacy,
        "user = siteuser", "group = sitegroup",
        "listen.owner = caddy", "php_admin_flag[display_errors] = off",
    } {
        if !strings.Contains(config, want) { t.Errorf("missing %q in PHP 7.4 pool", want) }
    }
}

func TestDetectVersionRejectsInjection(t *testing.T) {
    for _, version := range []string{"../etc", "7.4;rm", "7.4\nfoo", "7.4/../../etc"} {
        if _, err := DetectVersion(version); err == nil {
            t.Fatalf("unsafe PHP version accepted: %q", version)
        }
    }
}
