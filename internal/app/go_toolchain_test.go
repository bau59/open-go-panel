package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFirstExecutableGoFindsManagedBinary(t *testing.T) {
	dir := t.TempDir()
	nonExecutable := filepath.Join(dir, "go-not-executable")
	executable := filepath.Join(dir, "go")
	if err := os.WriteFile(nonExecutable, []byte("#!/bin/sh\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	got := firstExecutableGo([]string{filepath.Join(dir, "missing"), nonExecutable, executable})
	if got != executable {
		t.Fatalf("found Go binary %q, want %q", got, executable)
	}
}

func TestFirstExecutableGoRejectsDirectoriesAndMissingTools(t *testing.T) {
	dir := t.TempDir()
	if got := firstExecutableGo([]string{filepath.Join(dir, "missing"), dir}); got != "" {
		t.Fatalf("invalid tool detected: %q", got)
	}
}

func TestFirstExecutableGoAcceptsToolchainSymlink(t *testing.T) {
	dir := t.TempDir()
	realGo := filepath.Join(dir, "real-go")
	link := filepath.Join(dir, "go")
	if err := os.WriteFile(realGo, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realGo, link); err != nil {
		t.Fatal(err)
	}
	if got := firstExecutableGo([]string{link}); got != link {
		t.Fatalf("Go symlink resolved to %q, want %q", got, link)
	}
}
