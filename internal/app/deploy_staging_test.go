package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestActivateStagedRelease(t *testing.T) {
	for _, tc := range []struct {
		name     string
		fail     bool
		restarts int
	}{
		{name: "success", fail: false, restarts: 0},
		{name: "failed activation restores original", fail: true, restarts: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := t.TempDir()
			root := filepath.Join(parent, "app")
			stage := filepath.Join(parent, ".stage")
			if err := os.Mkdir(root, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(stage, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "version"), []byte("old"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(stage, "version"), []byte("new"), 0600); err != nil {
				t.Fatal(err)
			}
			restarts := 0
			err := activateStagedRelease(root, stage, func() error {
				got, err := os.ReadFile(filepath.Join(root, "version"))
				if err != nil || string(got) != "new" {
					t.Fatalf("activation did not see staged tree: %q (%v)", got, err)
				}
				if tc.fail {
					return errors.New("service failed to start")
				}
				return nil
			}, func() error {
				restarts++
				return nil
			})
			if (err != nil) != tc.fail {
				t.Fatalf("activate error=%v, want failure=%v", err, tc.fail)
			}
			wantRoot, wantStage := "new", "old"
			if tc.fail {
				wantRoot, wantStage = "old", "new"
			}
			gotRoot, _ := os.ReadFile(filepath.Join(root, "version"))
			gotStage, _ := os.ReadFile(filepath.Join(stage, "version"))
			if string(gotRoot) != wantRoot || string(gotStage) != wantStage {
				t.Fatalf("root=%q stage=%q, want %q and %q", gotRoot, gotStage, wantRoot, wantStage)
			}
			if restarts != tc.restarts {
				t.Fatalf("old version restarts = %d, want %d", restarts, tc.restarts)
			}
		})
	}
}

func TestStageApplicationDoesNotTouchRunningTree(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "demo")
	if err := os.Mkdir(root, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "application.txt"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	stage, err := stageApplication(context.Background(), App{Name: "demo", Root: root})
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(stage)
	if filepath.Dir(stage) != parent {
		t.Fatalf("stage %s must share parent %s", stage, parent)
	}
	if err := os.WriteFile(filepath.Join(stage, "application.txt"), []byte("updated"), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "application.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original" {
		t.Fatalf("staging touched active files: %q", data)
	}
}

func TestExchangeRejectsDifferentParents(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	stage := filepath.Join(t.TempDir(), "stage")
	if err := exchangeApplicationVersion(root, stage); err == nil {
		t.Fatal("expected different-parent exchange to be rejected")
	}
}
