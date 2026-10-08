package app

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bau59/open-go-panel/internal/state"
)

func TestRecordRollbackDisablesAutomaticRedeploy(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	_, err = store.DB().Exec(`INSERT INTO apps
		(id, owner, name, type, root, port, command, service_json, created_at)
		VALUES (7, 'tester', 'demo', 'static', '/home/tester/apps/demo', 0, '', '{}', '2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.DB().Exec(`INSERT INTO deployments
		(app_id, repository, branch, current_commit, previous_commit, auto_deploy)
		VALUES (7, 'https://example.test/repo.git', 'main', 'new', 'old', 1)`)
	if err != nil {
		t.Fatal(err)
	}

	manager := New(store, filepath.Join(t.TempDir(), "apps.json"), nil)
	if err := manager.recordRollback(7, "old", "new"); err != nil {
		t.Fatal(err)
	}

	var current, previous string
	var autoDeploy int
	if err := store.DB().QueryRow(`SELECT current_commit, previous_commit, auto_deploy
		FROM deployments WHERE app_id = 7`).Scan(&current, &previous, &autoDeploy); err != nil {
		t.Fatal(err)
	}
	if current != "old" || previous != "new" || autoDeploy != 0 {
		t.Fatalf("rollback state current=%q previous=%q auto_deploy=%d", current, previous, autoDeploy)
	}
}

func TestRecoveryWithoutPreviousRevisionReportsOriginalFailure(t *testing.T) {
	cause := errors.New("build failed")
	manager := &Manager{}
	err := manager.recoverDeployment(App{}, 1, "", cause)
	if !errors.Is(err, cause) {
		t.Fatalf("original error missing: %v", err)
	}
	if !strings.Contains(err.Error(), "no prior Git revision") {
		t.Fatalf("missing first-deploy recovery limitation: %v", err)
	}
}

