package state

import (
	"context"
	"path/filepath"
	"testing"
)

func TestAppCronJobsCascadeOnAppDeletion(t *testing.T) {
	ctx := context.Background()
	store, err := Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB().ExecContext(ctx,
		"INSERT INTO apps(id,owner,name,type,root,port,command,service_json,created_at) VALUES(1,'webuser','site','static','/home/webuser/apps/site',0,'','{}','2026-01-01T00:00:00Z')"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx,
		"INSERT INTO app_cron_jobs(app_id,name,schedule,command,enabled,created_at) VALUES(1,'cleanup','*/5 * * * *','echo done',1,'2026-01-01T00:00:00Z')"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx, "DELETE FROM apps WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM app_cron_jobs").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("cron jobs remain after app removal: %d", count)
	}
}
