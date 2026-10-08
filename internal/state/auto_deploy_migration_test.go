package state

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMigrationAddsAutoDeployIntervalToLegacyDatabase(t *testing.T) {
	path:=filepath.Join(t.TempDir(),"legacy.db")
	db,err:=sql.Open("sqlite",path)
	if err!=nil{t.Fatal(err)}
	if _,err:=db.Exec(`CREATE TABLE deployments(
		app_id INTEGER PRIMARY KEY,
		repository TEXT NOT NULL DEFAULT '',
		branch TEXT NOT NULL DEFAULT 'main',
		current_commit TEXT NOT NULL DEFAULT '',
		previous_commit TEXT NOT NULL DEFAULT '',
		deployed_at TEXT NOT NULL DEFAULT '',
		auto_deploy INTEGER NOT NULL DEFAULT 0
	);
	INSERT INTO deployments(app_id,repository,branch,auto_deploy)
	VALUES(2,'git@github.com:test/legacy.git','main',1);`);err!=nil{
		_ = db.Close()
		t.Fatal(err)
	}
	if err:=db.Close();err!=nil{t.Fatal(err)}
	store,err:=Open(path)
	if err!=nil{t.Fatal(err)}
	defer store.Close()
	var interval int
	var checked string
	var enabled int
	if err:=store.DB().QueryRow(`SELECT auto_deploy_interval_sec,last_checked_at,auto_deploy
		FROM deployments WHERE app_id=2`).Scan(&interval,&checked,&enabled);err!=nil{t.Fatal(err)}
	if interval!=300||checked!=""||enabled!=1{
		t.Fatalf("old app migrated with interval %d, checked %q, auto deploy %d",interval,checked,enabled)
	}
	// Restarting the panel must not reset an existing custom interval.
	if _,err:=store.DB().Exec(`UPDATE deployments SET auto_deploy_interval_sec=30 WHERE app_id=2`);err!=nil{t.Fatal(err)}
	if err:=store.Close();err!=nil{t.Fatal(err)}
	store,err=Open(path)
	if err!=nil{t.Fatal(err)}
	defer store.Close()
	if err:=store.DB().QueryRow(`SELECT auto_deploy_interval_sec FROM deployments WHERE app_id=2`).Scan(&interval);err!=nil{t.Fatal(err)}
	if interval!=30{t.Fatalf("custom interval lost during migration: %d",interval)}
}
