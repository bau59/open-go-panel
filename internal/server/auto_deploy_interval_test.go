package server

import (
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	panelapp "github.com/bau59/open-go-panel/internal/app"
	"github.com/bau59/open-go-panel/internal/state"
)

func TestParseAutoDeployIntervalSeconds(t *testing.T){
	cases:=[]struct{input string;want int;valid bool}{
		{"",300,true},
		{"30",30,true},
		{"60",60,true},
		{"300",300,true},
		{"1800",1800,true},
		{"86400",86400,true},
		{"29",0,false},
		{"-10",0,false},
		{"0",0,false},
		{"86401",0,false},
		{"1.5",0,false},
		{"invalid",0,false},
	}
	for _,tc:=range cases{
		got,err:=parseAutoDeployIntervalSeconds(tc.input)
		if (err==nil)!=tc.valid || (tc.valid&&got!=tc.want){
			t.Errorf("parse(%q) = %d, %v; want %d, valid=%v",tc.input,got,err,tc.want,tc.valid)
		}
	}
}

func TestAutoDeployIntervalShownOnAppPage(t *testing.T){
	current,err:=user.Current()
	if err!=nil{t.Fatal(err)}
	store,err:=state.Open(filepath.Join(t.TempDir(),"panel.db"))
	if err!=nil{t.Fatal(err)}
	defer store.Close()
	root:=filepath.Join(t.TempDir(),"app")
	if _,err:=store.DB().Exec(`INSERT INTO apps(id,owner,name,type,root,created_at)
		VALUES(55,?,?, 'go', ?, '2026-01-01T00:00:00Z')`,current.Username,"demo",root);err!=nil{
		t.Fatal(err)
	}
	manager:=panelapp.New(store,filepath.Join(t.TempDir(),"apps.json"),nil)
	if err:=manager.SetDeployConfigInterval(55,"git@github.com:org/repo.git","main",true,30);err!=nil{t.Fatal(err)}
	markup:=deployBlock(Config{Apps:manager},panelapp.App{ID:55,Type:"go"})
	for _,expected:=range []string{
		`name="auto_deploy_interval_seconds" min="30" max="86400"`,
		`value="30"`,
		`name="auto_deploy" value="1" checked`,
		`30 = every 30 seconds`,
	}{
		if !strings.Contains(markup,expected){t.Errorf("missing auto-deploy UI fragment %q",expected)}
	}
}
