package server

import (
	"strings"
	"testing"

	panelcaddy "github.com/bau59/open-go-panel/internal/caddy"
)

func TestCaddyPageStandaloneDomainControls(t *testing.T) {
	standalone := panelcaddy.Site{AppID: -1, Domain: "unused.example.com", Kind: "parked"}
	page := caddyPage("active", []panelcaddy.Site{standalone}, "", "", panelcaddy.GlobalSettings{HTTPS: true}, -1, "")
	for _, required := range []string{
		`action="/caddy/domain"`,
		`action="/caddy/site/-1/rename"`,
		`action="/caddy/site/-1/redirect"`,
		`action="/caddy/site/-1/attach"`,
		`action="/caddy/site/-1/remove"`,
		`href="/caddy/site/-1/diagnostics"`,
		"Unassigned",
	} {
		if !strings.Contains(page, required) {
			t.Errorf("standalone domain page is missing %q", required)
		}
	}
}

func TestCaddyPageAppDomainKeepsExistingControls(t *testing.T) {
	app := panelcaddy.Site{AppID: 1, Domain: "app.example.com", Kind: "proxy", Port: 8080}
	page := caddyPage("active", []panelcaddy.Site{app}, "", "", panelcaddy.GlobalSettings{HTTPS: true}, 1, "")
	for _, required := range []string{
		`action="/caddy/site/1/rename"`,
		`action="/caddy/site/1/template"`,
		`href="/caddy/site/1/diagnostics"`,
	} {
		if !strings.Contains(page, required) {
			t.Errorf("app domain page is missing %q", required)
		}
	}
	if strings.Contains(page, `action="/caddy/site/1/remove"`) {
		t.Error("app domain unexpectedly exposes standalone deletion")
	}
}

func TestCaddyCanonicalAndMirrorUI(t *testing.T) {
	site := panelcaddy.Site{
		AppID: 3, Domain: "www.example.com", Kind: "proxy", Port: 8080,
		WWWRedirect: true, Aliases: []string{"longer-example-domain.com", "mirror.example.org"},
	}
	page := caddyPage("active", []panelcaddy.Site{site}, "", "", panelcaddy.GlobalSettings{HTTPS: true}, 3, "")
	for _, required := range []string{
		`action="/caddy/site/3/aliases"`,
		`name="www_redirect" value="1" checked`,
		`name="mirrors"`,
		`longer-example-domain.com`,
		`mirror.example.org`,
		`Primary domain &amp; mirrors`,
		`Change primary domain`,
		`3 redirect alias(es)`,
	} {
		if !strings.Contains(page, required) {
			t.Errorf("canonical domain UI missing %q", required)
		}
	}
}

func TestCaddyParkedDomainHasNoAliasForm(t *testing.T) {
	site := panelcaddy.Site{AppID: -1, Domain: "parked.example.com", Kind: "parked"}
	page := caddyPage("active", []panelcaddy.Site{site}, "", "", panelcaddy.GlobalSettings{HTTPS: true}, -1, "")
	if strings.Contains(page, `action="/caddy/site/-1/aliases"`) {
		t.Fatal("parked domain must not have mirror redirect controls")
	}
}
