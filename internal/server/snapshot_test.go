package server

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bau59/open-go-panel/internal/state"
)

func TestSnapshotPoliciesIndependent(t *testing.T) {
	s,err:=state.Open(filepath.Join(t.TempDir(),"panel.db"));if err!=nil{t.Fatal(err)}
	defer s.Close()
	m:=snapshotManager{cfg:Config{State:s}}
	p:=snapshotPolicy{LocalDays:1,LocalHour:3,LocalKeep:7,CloudHour:4,CloudKeep:0}
	if err:=m.savePolicy(p);err!=nil{t.Fatal(err)}
	q:=p;q.LocalDays=3;q.LocalKeep=2
	if err:=m.savePolicyFor(m.appPolicyKey(7),q);err!=nil{t.Fatal(err)}
	if got:=m.policy();got!=p{t.Fatalf("server policy changed: %+v",got)}
	if got:=m.policyFor(m.appPolicyKey(7));got!=q{t.Fatalf("app policy mismatch: %+v",got)}
	now:=time.Date(2026,11,25,4,0,0,0,time.UTC)
	if !snapshotDue(now,"2026-11-24",1,3)||snapshotDue(now,"2026-11-24",3,3)||snapshotDue(now,"",0,3)||snapshotDue(now,"",1,5){t.Fatal("invalid due calculation")}
}

func TestSnapshotIndexRejectsInvalidArchives(t *testing.T){
	id:=time.Now().UTC().Format(snapshotStamp)
	f:=snapshotFile{Path:"config.tar.gz",Size:1,SHA256:strings.Repeat("a",64)}
	idx:=snapshotIndex{Version:1,ID:id,Files:[]snapshotFile{f}}
	if err:=validateSnapshotIndex(idx,id);err!=nil{t.Fatal(err)}
	for _,path:=range []string{"../config.tar.gz","files/../x.tar.gz","/config.tar.gz","databases/redis/x.sql.gz","files/x.exe","files/a/b.tar.gz"}{
		bad:=idx;bad.Files=[]snapshotFile{{Path:path,Size:1,SHA256:f.SHA256}}
		if validateSnapshotIndex(bad,id)==nil{t.Fatalf("accepted %q",path)}
	}
	idx.Files=append(idx.Files,f)
	if validateSnapshotIndex(idx,id)==nil{t.Fatal("accepted duplicate archive")}
}

func TestSnapshotConfigRejectsTraversal(t *testing.T){
	for _,name:=range []string{"../escape","system/etc/caddy/../../escape","system/etc/shadow","system/etc/systemd/system/ssh.service"}{
		t.Run(name,func(t *testing.T){
			root:=t.TempDir();archive:=filepath.Join(root,"archive.tar.gz")
			f,err:=os.Create(archive);if err!=nil{t.Fatal(err)}
			gz:=gzip.NewWriter(f);tw:=tar.NewWriter(gz)
			if err:=tw.WriteHeader(&tar.Header{Name:name,Mode:0600,Size:1});err!=nil{t.Fatal(err)}
			if _,err:=tw.Write([]byte("x"));err!=nil{t.Fatal(err)}
			if err:=tw.Close();err!=nil{t.Fatal(err)}
			if err:=gz.Close();err!=nil{t.Fatal(err)}
			if err:=f.Close();err!=nil{t.Fatal(err)}
			dir:=filepath.Join(root,"stage");if err:=os.Mkdir(dir,0700);err!=nil{t.Fatal(err)}
			if extractSnapshotConfig(context.Background(),archive,dir)==nil{t.Fatal("accepted unsafe archive")}
		})
	}
}
