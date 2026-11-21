package caddy

import (
	"strings"
	"testing"
)

func TestPHPManagedSiteTemplate(t *testing.T) {
	site := Site{AppID: 17, Domain: "example.com", Root: "/home/site/apps/blog/public", Kind: "php"}
	settings := defaultGlobalSettings()
	rendered := renderSite(ManagedSiteTemplate(site, settings), site)
	for _, expected := range []string{
		"example.com {", "root * /home/site/apps/blog/public",
		"php_fastcgi unix//run/php/ogp-app-17.sock",
		"file_server", "respond @ogp_hidden 404", "log",
	} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("generated PHP config missing %q:\n%s", expected, rendered)
		}
	}
	if strings.Contains(rendered, "{app_id}") || strings.Contains(rendered, "reverse_proxy") {
		t.Fatalf("PHP must use FastCGI socket, not a reverse proxy:\n%s", rendered)
	}
	if err := validateSiteTemplate(ManagedSiteTemplate(site, settings), site); err != nil {
		t.Fatal(err)
	}
}

func TestPHPRespectsGlobalSettings(t *testing.T) {
	site := Site{AppID: 2, Domain: "example.com", Root: "/srv/public", Kind: "php"}
	settings := GlobalSettings{HTTPS: false, Compression: false, AccessLog: false}
	value := renderSite(ManagedSiteTemplate(site, settings), site)
	if !strings.Contains(value, "http://example.com {") { t.Fatal(value) }
	for _, unexpected := range []string{"encode zstd gzip", "\tlog"} {
		if strings.Contains(value, unexpected) { t.Fatalf("unexpected %q in %s", unexpected, value) }
	}
}


func TestPHPVersionSelectsDedicatedFastCGISocket(t *testing.T) {
    site := Site{AppID: 17, Domain: "legacy.example.com", Root: "/home/site/apps/blog/public", Kind: "php", PHPVersion: "7.4"}
    rendered := renderSite(ManagedSiteTemplate(site, defaultGlobalSettings()), site)
    if !strings.Contains(rendered, "php_fastcgi unix//run/php/ogp-app-17-php74.sock") {
        t.Fatal("PHP 7.4 did not select its dedicated socket: " + rendered)
    }
    if strings.Contains(rendered, "{php_socket}") || strings.Contains(rendered, "ogp-app-17.sock") {
        t.Fatal("PHP 7.4 accidentally routes to default PHP-FPM: " + rendered)
    }
}
