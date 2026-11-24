package caddy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareProxyPublicRootWithoutDirectory(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	// Proxy applications do not have to expose static files.
	if err := prepareProxyPublicRoot(ctx, root); err != nil {
		t.Fatalf("missing public directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "public"), []byte("regular file"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := prepareProxyPublicRoot(ctx, root); err != nil {
		t.Fatalf("regular file named public: %v", err)
	}
}

func TestPrepareProxyPublicRootRejectsSymlinks(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	outside := t.TempDir()

	if err := os.Symlink(outside, filepath.Join(root, "public")); err != nil {
		t.Fatal(err)
	}
	if err := prepareProxyPublicRoot(ctx, root); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink rejection, got %v", err)
	}

	realRoot := filepath.Join(root, "real")
	if err := os.MkdirAll(filepath.Join(realRoot, "public"), 0750); err != nil {
		t.Fatal(err)
	}
	linkedRoot := filepath.Join(root, "linked")
	if err := os.Symlink(realRoot, linkedRoot); err != nil {
		t.Fatal(err)
	}
	if err := prepareProxyPublicRoot(ctx, linkedRoot); err == nil || !strings.Contains(err.Error(), "symlinks") {
		t.Fatalf("expected symlinked ancestor rejection, got %v", err)
	}
}

func TestPrepareProxyPublicRootRejectsInvalidAppRoot(t *testing.T) {
	for _, root := range []string{"", ".", "/", "relative/app", "/tmp/app\ninjected"} {
		if err := prepareProxyPublicRoot(context.Background(), root); err == nil {
			t.Errorf("accepted invalid app root %q", root)
		}
	}
}
