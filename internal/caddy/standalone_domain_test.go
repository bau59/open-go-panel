package caddy

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bau59/open-go-panel/internal/state"
)

func TestStandaloneDomainStoragePreservesAppDomains(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if _, err := store.DB().Exec(
		"INSERT INTO apps(id, owner, name, type, root, port, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		1, "test", "web", "go", "/srv/web", 8080, "2026-01-01",
	); err != nil {
		t.Fatal(err)
	}

	manager := New(store, filepath.Join(t.TempDir(), "legacy.json"))
	sites := []Site{
		{AppID: 1, Domain: "app.example.com", Port: 8080, Kind: "proxy"},
		{AppID: -1, Domain: "unused.example.com", Kind: "parked"},
	}
	if err := manager.save(sites); err != nil {
		t.Fatalf("save mixed sites: %v", err)
	}
	got, err := manager.Sites()
	if err != nil {
		t.Fatalf("load mixed sites: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 domains, got %d: %+v", len(got), got)
	}
	app, found, err := manager.SiteForApp(1)
	if err != nil || !found || app.Domain != "app.example.com" {
		t.Fatalf("app domain changed: site=%+v found=%v err=%v", app, found, err)
	}

	if err := manager.save(sites[:1]); err != nil {
		t.Fatalf("remove standalone: %v", err)
	}
	got, err = manager.Sites()
	if err != nil || len(got) != 1 || got[0].Domain != "app.example.com" {
		t.Fatalf("standalone removal damaged app site: sites=%+v err=%v", got, err)
	}
}

func TestAttachStandaloneRejectsCustomOverride(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	manager := New(store, filepath.Join(t.TempDir(), "legacy.json"))
	site := Site{AppID: -1, Domain: "custom.example.com", Kind: "parked", Template: "{domain} { respond 200 }"}
	if err := manager.save([]Site{site}); err != nil {
		t.Fatal(err)
	}
	err = manager.AttachStandaloneDomain(context.Background(), -1, 1, 8080, "", "proxy")
	if err == nil || !strings.Contains(err.Error(), "custom Caddy override") {
		t.Fatalf("expected override protection, got %v", err)
	}
	got, err := manager.Sites()
	if err != nil || len(got) != 1 || got[0].Template != site.Template {
		t.Fatalf("override changed after rejected attach: sites=%+v err=%v", got, err)
	}
}

func TestStandaloneRedirectRejectsInvalidURLs(t *testing.T) {
	manager := &Manager{}
	for _, dest := range []string{
		"javascript:alert(1)",
		"https://example.com/\nrespond 200",
		"https://user:pass@example.com/",
		"https://example.com/{unsafe}",
	} {
		if err := manager.SetStandaloneRedirect(context.Background(), -1, dest); err == nil {
			t.Errorf("accepted invalid redirect target: %q", dest)
		}
	}
}
