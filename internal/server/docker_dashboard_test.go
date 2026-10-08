package server

import (
	"strings"
	"testing"

	paneldocker "github.com/bau59/open-go-panel/internal/docker"
)

func TestDockerDashboardShowsContainersBeforeCollapsedForms(t *testing.T) {
	containers:=[]paneldocker.Container{{ID:"abc123",Name:"deepseek-bridge",Image:"ogp/deepseek-bridge:build-old",
		State:"running",Running:true,RestartPolicy:"unless-stopped",Repository:"bau59/deepseek-web-api-bridge",
		Branch:"main",Dockerfile:"Dockerfile",Commit:"abcdef123456"}}
	page:=dockerPage(paneldocker.Status{Installed:true,Active:true,Version:"27.5"},containers,
		false,paneldocker.BuildTask{},paneldocker.GitHubKeyInfo{},"")
	listIndex:=strings.Index(page,`id="docker-containers"`)
	formIndex:=strings.Index(page,`id="docker-add-image"`)
	if listIndex<0||formIndex<0||listIndex>formIndex{
		t.Fatalf("containers should appear before creation forms, list=%d form=%d",listIndex,formIndex)
	}
	for _,fragment:=range []string{
		`<details class="panel panel-pad docker-create-card docker-fold" id="docker-add-image"`,
		`<details class="panel panel-pad docker-create-card docker-fold" id="docker-add-github"`,
		`/docker/abc123/rebuild`,
		`value="bau59/deepseek-web-api-bridge"`,
		`Git abcdef1`,
		`data-field="cpu"`,`data-field="memory"`,`/docker/stats`,
		`docker-total-memory`,`docker-total-cpu`,
	}{
		if !strings.Contains(page,fragment){t.Errorf("missing %s",fragment)}
	}
	if strings.Count(page,`name="init"`)!=2 {
		t.Errorf("expected init control in both creation forms")
	}
}

func TestDockerStatusSourceMissingIsEditable(t *testing.T) {
	containers:=[]paneldocker.Container{{ID:"old000123",Name:"custom-bridge",Image:"old",State:"running",Running:true}}
	page:=dockerPage(paneldocker.Status{Installed:true,Active:true},containers,
		false,paneldocker.BuildTask{},paneldocker.GitHubKeyInfo{},"")
	if !strings.Contains(page,`/docker/old000123/rebuild`) ||
		!strings.Contains(page,`name="repository" required placeholder="owner/repository"`){
		t.Fatal("older GitHub builds must support manual source input")
	}
}
