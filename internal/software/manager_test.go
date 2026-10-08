package software

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectGoVersionPrefersManagedToolchain(t *testing.T) {
	dir := t.TempDir()
	managed := filepath.Join(dir, "managed-go")
	fallback := filepath.Join(dir, "fallback-go")
	if err := os.WriteFile(managed, []byte("#!/bin/sh\necho 'go version go1.25.4 linux/amd64'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fallback, []byte("#!/bin/sh\necho 'go version go1.22.0 linux/amd64'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name       string
		candidates []string
		want       string
	}{
		{"managed path takes precedence", []string{managed, fallback}, "go version go1.25.4 linux/amd64"},
		{"fallback PATH candidate", []string{filepath.Join(dir, "missing"), fallback}, "go version go1.22.0 linux/amd64"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := detectGoVersion(context.Background(), tc.candidates...)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("detected %q; expected %q", got, tc.want)
			}
		})
	}
}

func TestDetectGoVersionReportsMissingToolchain(t *testing.T) {
	dir := t.TempDir()
	version, err := detectGoVersion(context.Background(), filepath.Join(dir, "go"))
	if err == nil || strings.TrimSpace(version) != "" {
		t.Fatalf("missing Go version = %q, err = %v", version, err)
	}
}
