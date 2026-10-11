package server

import (
	"strings"
	"testing"

	"github.com/bau59/open-go-panel/internal/security"
)

func TestSecurityPageWarnsAndRepairsDuplicateCaddySources(t *testing.T) {
	state := security.Status{
		Installed: true, EngineActive: true, WebProtection: true,
		WebSources: security.WebSources{
			Panel: true,
			External: []string{"/etc/crowdsec/acquis.d/setup.caddy.yaml"},
			SyslogAcquired: true,
			NeedsRepair: true,
		},
	}
	html := securityPage(securityPageData{Status: state})
	for _, fragment := range []string{
		"Repair duplicate logs",
		"/security/web/repair",
		"Caddy HTTP events may be acquired more than once",
		"managed by CrowdSec",
	} {
		if !strings.Contains(html, fragment) {
			t.Errorf("missing %q", fragment)
		}
	}
	if strings.Contains(html, `action="/security/web/disable"`) {
		t.Fatal("must not offer to remove external CrowdSec acquisition")
	}
}
