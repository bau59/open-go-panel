package docker

import (
	"os"
	"strings"
	"testing"
)

func TestDockerStatsLines(t *testing.T) {
	in := []byte(`{"ID":"abc123def456","Name":"bridge","CPUPerc":"3.25%","MemUsage":"84.0MiB / 512MiB","MemPerc":"16.41%"}`+"\n"+
		`{"ID":"fed987654321","Name":"worker","CPUPerc":"0.00%","MemUsage":"12MiB / 1GiB","MemPerc":"1.17%"}`+"\n")
	items,err:=parseDockerStats(in)
	if err!=nil {t.Fatal(err)}
	if len(items)!=2||items["abc123def456"].CPU!="3.25%"||
		items["abc123def456"].Memory!="84.0MiB / 512MiB"||
		items["fed987654321"].MemPct!="1.17%" {
		t.Fatalf("unexpected parsed Docker stats: %#v",items)
	}
	if _,err:=parseDockerStats([]byte("{invalid"));err==nil {t.Fatal("malformed Docker stats accepted")}
}

func TestRebuildPreservesExistingPortsEnvironmentAndVolumes(t *testing.T) {
	spec:=replacementSpec{
		ID:"123456789abc",Name:"deepseek-bridge",Image:"ogp/deepseek-bridge:build-old",
		Running:true,Restart:"unless-stopped",Init:true,ShmSize:512*1024*1024,
		Env:[]string{"BRIDGE_API_KEYS=sk-example-secret","STATE_ENCRYPTION_KEY=original-secret","OTHER=value=with=equals"},
	}
	spec.Ports=map[string][]struct {
		HostIP string `json:"HostIp"`
		HostPort string `json:"HostPort"`
	}{"8000/tcp":{{HostIP:"127.0.0.1",HostPort:"8000"}}}
	spec.Mounts=append(spec.Mounts,struct {
		Type string `json:"Type"`
		Source string `json:"Source"`
		Name string `json:"Name"`
		Destination string `json:"Destination"`
		RW bool `json:"RW"`
		Driver string `json:"Driver"`
		Propagation string `json:"Propagation"`
	}{"bind","/opt/deepseek-bridge/data","","/app/data",true,"","rprivate"})
	args,cleanup,err:=spec.launchArgs("ogp/deepseek-bridge:build-new")
	if err!=nil {t.Fatal(err)}
	defer cleanup()
	command:=strings.Join(args," ")
	for _,part:=range []string{
		"run -d --name deepseek-bridge","--restart unless-stopped","--init","--shm-size 536870912",
		"-p 127.0.0.1:8000:8000/tcp","--mount type=bind,src=/opt/deepseek-bridge/data,dst=/app/data",
		"ogp/deepseek-bridge:build-new"}{
		if !strings.Contains(command,part){t.Errorf("missing %q in %s",part,command)}
	}
	if strings.Contains(command,"original-secret")||strings.Contains(command,"sk-example-secret"){
		t.Fatal("secret exposed in Docker command")
	}
	i:=-1
	for n,v:=range args{if v=="--env-file"{i=n;break}}
	if i<0||i+1>=len(args){t.Fatal("missing protected Docker env file")}
	file:=args[i+1]
	info,err:=os.Stat(file)
	if err!=nil {t.Fatal(err)}
	if info.Mode().Perm()!=0600{t.Errorf("env file mode %o",info.Mode().Perm())}
	data,err:=os.ReadFile(file)
	if err!=nil {t.Fatal(err)}
	if !strings.Contains(string(data),"STATE_ENCRYPTION_KEY=original-secret\n") {t.Fatal("original encryption key not retained")}
	cleanup()
	if _,err:=os.Stat(file);!os.IsNotExist(err){t.Fatalf("env file left behind: %v",err)}
}

func TestRebuildRejectsUnsupportedMounts(t *testing.T) {
	p:=replacementSpec{Name:"example"}
	p.Mounts=append(p.Mounts,struct {
		Type string `json:"Type"`
		Source string `json:"Source"`
		Name string `json:"Name"`
		Destination string `json:"Destination"`
		RW bool `json:"RW"`
		Driver string `json:"Driver"`
		Propagation string `json:"Propagation"`
	}{"tmpfs","","","/run",true,"",""})
	if _,_,err:=p.launchArgs("ogp/image:v1");err==nil{t.Fatal("unsupported tmpfs mount accepted")}
}

func TestRebuildStoppedContainerStaysStopped(t *testing.T) {
	p:=replacementSpec{Name:"stopped-container",Running:false}
	args,cleanup,err:=p.launchArgs("local:image")
	if err!=nil{t.Fatal(err)}
	cleanup()
	if len(args)==0||args[0]!="create"{t.Fatalf("stopped container would be started: %#v",args)}
}
