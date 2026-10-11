package security

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveProfilesFileChecksValidationAndRollsBack(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0755); err != nil { t.Fatal(err) }
	file := filepath.Join(dir, "profiles.yaml")
	old := "name: base\nfilters:\n - Alert.Remediation == true\ndecisions:\n - type: ban\n   duration: 4h\n"
	if err := os.WriteFile(file, []byte(old), 0640); err != nil { t.Fatal(err) }
	crowdsec := "#!/bin/sh\ngrep -q INVALID \"$TEST_PROFILES\" && { echo invalid-profile >&2; exit 1; }\nexit 0\n"
	systemctl := "#!/bin/sh\nif [ \"$1\" = restart ] && [ \"$FAIL_RESTART\" = 1 ]; then exit 1; fi\nexit 0\n"
	for name, script := range map[string]string{"crowdsec": crowdsec, "systemctl": systemctl} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0755); err != nil { t.Fatal(err) }
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TEST_PROFILES", file)
	m := &Manager{profilesDir: dir}
	initial, err := m.ReadProfilesFile("main")
	if err != nil { t.Fatal(err) }
	if !initial.Exists || initial.Version == "" { t.Fatalf("unexpected initial file: %+v", initial) }
	if err := m.SaveProfilesFile(context.Background(), "main", initial.Version, "INVALID"); err == nil || !strings.Contains(err.Error(), "invalid-profile") {
		t.Fatalf("expected validation error: %v", err)
	}
	check := func(want string) {
		t.Helper()
		b, err := os.ReadFile(file)
		if err != nil { t.Fatal(err) }
		if string(b) != want { t.Fatalf("file differs: got %q, want %q", string(b), want) }
	}
	check(old)
	t.Setenv("FAIL_RESTART", "1")
	if err := m.SaveProfilesFile(context.Background(), "main", initial.Version, "name: different\n"); err == nil || !strings.Contains(err.Error(), "restart failed") {
		t.Fatalf("expected restart error: %v", err)
	}
	check(old)
	t.Setenv("FAIL_RESTART", "")
	updated := "name: different\nfilters: []\n"
	if err := m.SaveProfilesFile(context.Background(), "main", initial.Version, updated); err != nil { t.Fatal(err) }
	check(updated)
	stat, err := os.Stat(file)
	if err != nil || stat.Mode().Perm() != 0640 { t.Fatalf("profile mode not retained: %v, %v", stat, err) }
	if err := m.SaveProfilesFile(context.Background(), "main", initial.Version, old); err == nil || !strings.Contains(err.Error(), "changed since") {
		t.Fatalf("stale edit unexpectedly accepted: %v", err)
	}
	check(updated)
}

func TestProfilesLocalCreationAndRejection(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0755); err != nil { t.Fatal(err) }
	for name, script := range map[string]string{
		"crowdsec": "#!/bin/sh\nexit 0\n",
		"systemctl": "#!/bin/sh\nexit 0\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0755); err != nil { t.Fatal(err) }
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	m := &Manager{profilesDir: dir}
	local, err := m.ReadProfilesFile("local")
	if err != nil || local.Exists || local.Version != "missing" { t.Fatalf("unexpected local: %+v, %v", local, err) }
	if err := m.SaveProfilesFile(context.Background(), "local", "wrong", "name: custom\n"); err == nil { t.Fatal("accepted stale version") }
	if err := m.SaveProfilesFile(context.Background(), "local", "missing", "name: custom\n"); err != nil { t.Fatal(err) }
	local, err = m.ReadProfilesFile("local")
	if err != nil || !local.Exists || local.Content != "name: custom\n" { t.Fatalf("local not created: %+v, %v", local, err) }
	if _, err := m.ReadProfilesFile("../../etc/passwd"); err == nil { t.Fatal("accepted path traversal") }
	if err := m.SaveProfilesFile(context.Background(), "main", "missing", ""); err == nil { t.Fatal("accepted empty YAML") }
}
