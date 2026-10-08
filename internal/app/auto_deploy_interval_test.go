package app

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/bau59/open-go-panel/internal/state"
)

func TestAutoDeployIntervalDue(t *testing.T) {
	now := time.Date(2026, 10, 8, 15, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		interval int
		elapsed time.Duration
		want bool
	}{
		{"first check", 30, 0, true},
		{"30 seconds not yet due", 30, 29*time.Second, false},
		{"30 seconds due", 30, 30*time.Second, true},
		{"1 minute not yet due", 60, 59*time.Second, false},
		{"1 minute due", 60, time.Minute, true},
		{"legacy default 5 minutes", 300, 299*time.Second, false},
		{"legacy default due", 300, 300*time.Second, true},
	} {
		t.Run(tc.name,func(t *testing.T) {
			cfg:=DeployConfig{Repository:"git@github.com:test/repo.git",AutoDeploy:true,AutoDeployIntervalSeconds:tc.interval}
			if tc.name!="first check"{cfg.LastCheckedAt=now.Add(-tc.elapsed)}
			if got:=autoDeployCheckDue(cfg,now);got!=tc.want{t.Fatalf("due=%v, want %v",got,tc.want)}
		})
	}
	if autoDeployCheckDue(DeployConfig{Repository:"git@github.com:test/repo.git",AutoDeploy:false},now) {
		t.Fatal("disabled app was scheduled")
	}
}

func TestAutoDeployIntervalSavedAndSurvivesReopen(t *testing.T) {
	path:=filepath.Join(t.TempDir(),"panel.db")
	store,err:=state.Open(path)
	if err!=nil{t.Fatal(err)}
	if _,err:=store.DB().Exec(`INSERT INTO apps(id,owner,name,type,root,created_at)
		VALUES(11,'tester','testapp','go','/home/tester/apps/testapp','2026-01-01T00:00:00Z')`);err!=nil{t.Fatal(err)}
	manager:=New(store,filepath.Join(t.TempDir(),"apps.json"),nil)
	initial,err:=manager.DeployConfig(11)
	if err!=nil{t.Fatal(err)}
	if initial.AutoDeployIntervalSeconds!=300 {t.Fatalf("new app default %d",initial.AutoDeployIntervalSeconds)}
	if err:=manager.SetDeployConfigInterval(11,"git@github.com:org/repo.git","main",true,60);err!=nil{t.Fatal(err)}
	now:=time.Date(2026,10,8,10,0,0,0,time.UTC)
	cfg,err:=manager.DeployConfig(11)
	if err!=nil{t.Fatal(err)}
	if !autoDeployCheckDue(cfg,now){t.Fatal("first auto deploy check not due")}
	ok,err:=manager.claimAutoDeployCheck(11,cfg,now)
	if err!=nil||!ok{t.Fatalf("cannot claim first check: %v %v",ok,err)}
	if ok,err:=manager.claimAutoDeployCheck(11,cfg,now);err!=nil||ok{
		t.Fatalf("stale claim accepted: %v %v",ok,err)
	}
	cfg,err=manager.DeployConfig(11)
	if err!=nil{t.Fatal(err)}
	if autoDeployCheckDue(cfg,now.Add(59*time.Second)) {t.Fatal("check ran before 60 seconds")}
	if !autoDeployCheckDue(cfg,now.Add(60*time.Second)){t.Fatal("check not due at 60 seconds")}
	if err:=store.Close();err!=nil{t.Fatal(err)}
	store,err=state.Open(path)
	if err!=nil{t.Fatal(err)}
	defer store.Close()
	manager=New(store,filepath.Join(t.TempDir(),"apps.json"),nil)
	cfg,err=manager.DeployConfig(11)
	if err!=nil{t.Fatal(err)}
	if cfg.AutoDeployIntervalSeconds!=60||!cfg.LastCheckedAt.Equal(now){
		t.Fatalf("interval and last check did not persist: %#v",cfg)
	}
	if err:=manager.SetDeployConfigInterval(11,"git@github.com:org/repo.git","main",true,30);err!=nil{t.Fatal(err)}
	cfg,err=manager.DeployConfig(11)
	if err!=nil{t.Fatal(err)}
	if !cfg.LastCheckedAt.IsZero(){t.Fatal("interval change did not trigger an immediate check")}
	if cfg.AutoDeployIntervalSeconds!=30{t.Fatalf("expected 30 second interval, got %d",cfg.AutoDeployIntervalSeconds)}
	// Saving unchanged settings must not discard the scheduling timestamp.
	ok,err=manager.claimAutoDeployCheck(11,cfg,now)
	if err!=nil||!ok{t.Fatal(err)}
	if err:=manager.SetDeployConfigInterval(11,"git@github.com:org/repo.git","main",true,30);err!=nil{t.Fatal(err)}
	cfg,err=manager.DeployConfig(11)
	if err!=nil{t.Fatal(err)}
	if cfg.LastCheckedAt.IsZero(){t.Fatal("unchanged settings reset check schedule")}
	if err:=manager.SetDeployConfigInterval(11,"git@github.com:org/repo.git","main",false,30);err!=nil{t.Fatal(err)}
	cfg,err=manager.DeployConfig(11)
	if err!=nil{t.Fatal(err)}
	if autoDeployCheckDue(cfg,now.Add(time.Minute)){t.Fatal("disabled auto-deploy is due")}
	if !cfg.LastCheckedAt.IsZero(){t.Fatal("disabling auto deploy should clear stale scheduling timestamp")}
}

func TestAutoDeployIntervalRejectsUnsupportedValues(t *testing.T){
	for _,v:=range []int{-1,0,1,29,86401}{
		manager:=&Manager{}
		if err:=manager.SetDeployConfigInterval(1,"git@github.com:org/repo.git","main",true,v);err==nil{
			t.Fatalf("invalid interval %d accepted",v)
		}
	}
	before:=DeployConfig{AutoDeploy:true,Repository:"repo",Branch:"main",AutoDeployIntervalSeconds:30}
	after:=before
	after.AutoDeployIntervalSeconds=60
	if shouldContinueAutoDeploy(before,after,"commit"){
		t.Fatal("remote check should not deploy using obsolete interval configuration")
	}
}
