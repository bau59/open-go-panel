package terminal

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bau59/open-go-panel/internal/state"
)

func TestBackgroundRunArgsKeepUserCommandAsSingleArgument(t *testing.T) {
	job := BackgroundJob{
		ID: "0123456789abcdef01234567",
		Username: "worker",
		WorkDir: "/home/worker/apps/site",
		Command: "printf 'hello; world' && sleep 30",
	}
	args, err := backgroundRunArgs(job)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(args, "\n")
	for _, want := range []string{
		"--property=User=worker",
		"--property=WorkingDirectory=/home/worker/apps/site",
		"--unit=ogp-bg-0123456789abcdef01234567",
		"--collect",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %v", want, args)
		}
	}
	if args[len(args)-1] != job.Command || args[len(args)-2] != "-lc" || args[len(args)-3] != "/bin/bash" {
		t.Fatalf("command was modified or split: %v", args)
	}
}

func TestBackgroundIdentifiersAndCommandsAreValidated(t *testing.T) {
	testCases := []BackgroundJob{
		{ID: "../other", Username: "worker", WorkDir: "/tmp", Command: "true"},
		{ID: "0123456789abcdef01234567", Username: "worker;id", WorkDir: "/tmp", Command: "true"},
		{ID: "0123456789abcdef01234567", Username: "worker", WorkDir: "relative", Command: "true"},
		{ID: "0123456789abcdef01234567", Username: "worker", WorkDir: "/tmp", Command: ""},
		{ID: "0123456789abcdef01234567", Username: "worker", WorkDir: "/tmp", Command: strings.Repeat("x", maxBackgroundCommand+1)},
	}
	for i, tc := range testCases {
		if _, err := backgroundRunArgs(tc); err == nil {
			t.Fatalf("case %d accepted invalid background command", i)
		}
	}
}

func TestParseBackgroundUnits(t *testing.T) {
	out := "ogp-bg-0123456789abcdef01234567.service loaded active running Open Go Panel background command\n" +
		"● ogp-bg-ffffffffffffffffffffffff.service loaded failed failed Open Go Panel background command\n" +
		"ssh.service loaded active running OpenSSH\n"
	units := parseBackgroundUnits(out)
	if got := units["ogp-bg-0123456789abcdef01234567.service"]; got != "running" {
		t.Fatalf("running status = %q", got)
	}
	if got := units["ogp-bg-ffffffffffffffffffffffff.service"]; got != "failed" {
		t.Fatalf("failed status = %q", got)
	}
	if _, exists := units["ssh.service"]; exists {
		t.Fatal("unmanaged systemd unit included")
	}
}

func TestBackgroundStateTableMigration(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, err = store.DB().ExecContext(context.Background(),
		"INSERT INTO background_commands(id,username,workdir,command,created_at) VALUES(?,?,?,?,?)",
		"0123456789abcdef01234567", "worker", "/tmp", "sleep 10", "2026-11-24T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	manager := NewBackgroundManager(store)
	job, err := manager.Get(context.Background(), "0123456789abcdef01234567")
	if err != nil {
		t.Fatal(err)
	}
	if job.Username != "worker" || job.Command != "sleep 10" {
		t.Fatalf("unexpected persisted job: %+v", job)
	}
}

func TestBackgroundLogBufferBound(t *testing.T) {
	w := &limitedOutput{cap: 8}
	for _, s := range []string{"1234567", "890ABCDEF"} {
		if _, err := w.Write([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	if string(w.data) != "12345678" || !w.cut {
		t.Fatalf("buffer = %q, truncated = %t", w.data, w.cut)
	}
}
