package caddy

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bau59/open-go-panel/internal/state"
)

func TestCanonicalHostRedirects(t *testing.T) {
	tests := []struct {
		name string
		site Site
		https bool
		want []string
	}{
		{
			"bare primary uses www alias",
			Site{AppID: 1, Domain: "example.com", WWWRedirect: true, Aliases: []string{"a-longer-domain.example"}},
			true,
			[]string{
				"www.example.com {",
				"redir https://example.com{uri} 301",
				"a-longer-domain.example {",
			},
		},
		{
			"www primary uses bare alias",
			Site{AppID: 1, Domain: "www.example.com", WWWRedirect: true},
			true,
			[]string{"example.com {", "redir https://www.example.com{uri} 301"},
		},
		{
			"HTTP only redirects to HTTP",
			Site{AppID: 1, Domain: "example.com", WWWRedirect: true},
			false,
			[]string{"http://www.example.com {", "redir http://example.com{uri} 301"},
		},
		{
			"unconfigured alias emits no additional blocks",
			Site{AppID: 1, Domain: "example.com"},
			true, nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := renderSiteAliases(tc.site, tc.https)
			for _, expected := range tc.want {
				if !strings.Contains(got, expected) {
					t.Errorf("missing %q in redirect:\n%s", expected, got)
				}
			}
			if tc.want == nil && got != "" {
				t.Fatalf("unexpected redirect: %s", got)
			}
		})
	}
}

func TestMirrorDomainValidation(t *testing.T) {
	good, err := parseMirrorDomains("LONG-DOMAIN.example, mirror.example.org\nnext.example.net")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(good, ",") != "long-domain.example,mirror.example.org,next.example.net" {
		t.Fatalf("unintended normalization: %v", good)
	}
	for _, input := range []string{
		"https://bad.example.org", "mirror.example.org/path", "bad.example.org:8080",
		"*.example.org", "bad.example.org\nrespond {unsafe}", "same.example.org\nsame.example.org",
	} {
		if _, err := parseMirrorDomains(input); err == nil {
			t.Errorf("accepted invalid mirror %q", input)
		}
	}
}

func TestAliasHostnameConflicts(t *testing.T) {
	for _, tc := range []struct {
		name string
		sites []Site
		fail bool
	}{
		{"valid mirrors", []Site{
			{AppID: 1, Domain: "example.com", WWWRedirect: true, Aliases: []string{"long.example.net"}},
			{AppID: -1, Domain: "parked.example.org", Kind: "parked"},
		}, false},
		{"www clashes with standalone", []Site{
			{AppID: 1, Domain: "example.com", WWWRedirect: true},
			{AppID: -1, Domain: "www.example.com", Kind: "parked"},
		}, true},
		{"mirror clashes with different canonical", []Site{
			{AppID: 1, Domain: "example.com", Aliases: []string{"second.example.com"}},
			{AppID: 2, Domain: "second.example.com"},
		}, true},
		{"www manually duplicated", []Site{
			{AppID: 1, Domain: "www.example.com", WWWRedirect: true, Aliases: []string{"example.com"}},
		}, true},
		{"primary cannot be mirror", []Site{
			{AppID: 1, Domain: "example.com", Aliases: []string{"example.com"}},
		}, true},
		{"parked cannot own redirects", []Site{
			{AppID: -1, Domain: "unused.example.com", Kind: "parked", WWWRedirect: true},
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateSiteHosts(tc.sites); (err != nil) != tc.fail {
				t.Fatalf("validateSiteHosts(%+v): error %v want fail %v", tc.sites, err, tc.fail)
			}
		})
	}
}

func TestCaddyAliasPersistenceAndCleanup(t *testing.T) {
	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.DB().Exec(
		"INSERT INTO apps(id, owner, name, type, root, port, created_at) VALUES(1, 'test', 'web', 'go', '/srv/web', 8080, '2026-01-01')",
	); err != nil {
		t.Fatal(err)
	}
	mgr := New(st, filepath.Join(t.TempDir(), "legacy.json"))
	site := Site{AppID: 1, Domain: "example.com", Port: 8080, Kind: "proxy",
		WWWRedirect: true, Aliases: []string{"mirror-long.example.org"}}
	if err := mgr.save([]Site{site}); err != nil {
		t.Fatal(err)
	}
	loaded, err := mgr.Sites()
	if err != nil || len(loaded) != 1 || !loaded[0].WWWRedirect ||
		strings.Join(loaded[0].Aliases, ",") != "mirror-long.example.org" {
		t.Fatalf("aliases not persisted: %+v, %v", loaded, err)
	}
	site.WWWRedirect = false
	site.Aliases = nil
	if err := mgr.save([]Site{site}); err != nil {
		t.Fatal(err)
	}
	loaded, err = mgr.Sites()
	if err != nil || loaded[0].WWWRedirect || len(loaded[0].Aliases) > 0 {
		t.Fatalf("aliases not cleared: %+v, %v", loaded, err)
	}
	if err := mgr.save(nil); err != nil {
		t.Fatal(err)
	}
	raw, ok, err := st.Setting(siteAliasesSettingKey)
	if err != nil || !ok || raw != "{}" {
		t.Fatalf("stale aliases after deleting site: %q, %v, %v", raw, ok, err)
	}
}

func TestSetSiteAliasesRejectsCollisionBeforeCaddy(t *testing.T) {
	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.DB().Exec("INSERT INTO apps(id, owner, name, type, root, port, created_at) VALUES(1, 'test', 'web', 'go', '/srv/web', 8080, '2026-01-01')"); err != nil {
		t.Fatal(err)
	}
	mgr := New(st, filepath.Join(t.TempDir(), "legacy.json"))
	if err := mgr.save([]Site{
		{AppID: 1, Domain: "example.com", Port: 8080, Kind: "proxy"},
		{AppID: -1, Domain: "www.example.com", Kind: "parked"},
	}); err != nil {
		t.Fatal(err)
	}
	err = mgr.SetSiteAliases(context.Background(), 1, true, "")
	if err == nil || !strings.Contains(err.Error(), "already configured") {
		t.Fatalf("didn't reject collision with parked domain: %v", err)
	}
}
