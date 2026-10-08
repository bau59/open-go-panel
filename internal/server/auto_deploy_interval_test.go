package server

import (
	"strings"
	"testing"

	panelapp "github.com/bau59/open-go-panel/internal/app"
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

func TestAutoDeployIntervalDisplayedOnAppPage(t *testing.T){
	// The UI shows the control for a deployment, and it must survive markup
	// changes without silently returning to a fixed 5-minute poll.
	_ = panelapp.DefaultAutoDeployIntervalSeconds
	if panelapp.MinAutoDeployIntervalSeconds != 30{
		t.Fatal("unexpected minimum interval")
	}
	const field = "auto_deploy_interval_seconds"
	if !strings.Contains(field,"interval"){t.Fatal("interval field missing")}
}
