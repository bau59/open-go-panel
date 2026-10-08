package server

import (
 "strings"
 "testing"
 paneldocker "github.com/bau59/open-go-panel/internal/docker"
)

func TestDockerPortDisplay(t *testing.T) {
 cases:=[]struct{raw,want string}{
  {"127.0.0.1:8000→8000/tcp","Local only → container 8000/tcp"},
  {"*:443→443/tcp","Public / all interfaces → container 443/tcp"},
  {"8080/tcp","Container-only / not published"},
  {"—","No published ports"},
 }
 for _,tc:=range cases{
  got:=dockerPortDisplay(tc.raw)
  if !strings.Contains(got,tc.want){t.Fatalf("port display %q omitted %q: %q",tc.raw,tc.want,got)}
 }
}

func TestDockerDashboardUsesModalAndHidesBackup(t *testing.T) {
 containers:=[]paneldocker.Container{
  {ID:"active123abc",Name:"deepseek",Image:"image:new",State:"running",Running:true,RestartPolicy:"unless-stopped",
   Ports:"127.0.0.1:8000→8000/tcp",Repository:"bau59/deepseek-web-api-bridge",Branch:"main",Dockerfile:"Dockerfile"},
  {ID:"backup456abc",Name:"ogp-prev-deepseek-k2c",Image:"image:old",State:"exited",RestartPolicy:"no"},
 }
 page:=dockerPage(paneldocker.Status{Installed:true,Active:true},containers,false,
  paneldocker.BuildTask{},paneldocker.GitHubKeyInfo{},"")
 if strings.Contains(page,"__CONTAINER_TABLE__"){t.Fatal("unreplaced layout template placeholder")}
 if count:=strings.Count(page,`id="docker-containers"`);count!=1{t.Fatalf("container list occurrence: %d",count)}
 if !strings.Contains(page,`id="docker-rebuild-active123abc" class="docker-rebuild-dialog"`){
  t.Fatal("rebuild should be a modal separate from the table")
 }
 if !strings.Contains(page,`document.getElementById('docker-rebuild-active123abc').showModal()`){
  t.Fatal("rebuild button should open modal")
 }
 if !strings.Contains(page,"Previous versions (1)") {t.Fatal("missing collapsed backup section")}
 if strings.Contains(page,`id="docker-rebuild-backup456abc"`){t.Fatal("backup containers must not have rebuild actions")}
 if !strings.Contains(page,"Local only") || !strings.Contains(page,"127.0.0.1:8000"){
  t.Fatal("loopback port not displayed clearly")
 }
 if strings.Contains(page,`<div class="metric"><span>Containers</span><strong>2`){
  t.Fatal("backup containers must not be included in operational count")
 }
 if !strings.Contains(page,`<form method="post" action="/docker/github/key" class="docker-key-form"`){
  t.Fatal("deploy key form must use its own layout")
 }
}
