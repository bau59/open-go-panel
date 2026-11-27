package app

import (
    "context"
    "encoding/json"
    "errors"
    "os"
    "path/filepath"
    "reflect"
    "strings"
    "testing"
    "time"
)

func TestDependencyValidation(t *testing.T) {
    self:="open-go-panel-app-1.service"
    graph:=map[string]unitEdges{
        "mysql.service":{ID:"mysql.service",LoadState:"loaded",After:[]string{"network.target"}},
        "mysql-alias.service":{ID:"mysql.service",LoadState:"loaded",After:[]string{"network.target"}},
        "redis.service":{ID:"redis.service",LoadState:"loaded"},
        "network.target":{ID:"network.target",LoadState:"loaded"},
        "missing.service":{ID:"missing.service",LoadState:"not-found"},
        "self-alias.service":{ID:self,LoadState:"loaded"},
        "cycle.service":{ID:"cycle.service",LoadState:"loaded",After:[]string{"bridge.target"}},
        "bridge.target":{ID:"bridge.target",LoadState:"loaded",After:[]string{self}},
        "require-cycle.service":{ID:"require-cycle.service",LoadState:"loaded",Wants:[]string{self}},
    }
    read:=func(_ context.Context,name string)(unitEdges,error){u,ok:=graph[name];if !ok{return u,errors.New("unknown unit")};return u,nil}
    got,err:=normalizeDependencies(context.Background(),self,[]ServiceDependency{{"redis.service",false},{"mysql-alias.service",false},{"mysql.service",true}},read)
    want:=[]ServiceDependency{{"mysql.service",true},{"redis.service",false}}
    if err!=nil||!reflect.DeepEqual(got,want){t.Fatalf("got %+v, %v",got,err)}
    for _,name:=range []string{"missing.service","self-alias.service","cycle.service","require-cycle.service","bad.service\nAfter=x","foo@.service","foo.service;echo"}{
        if _,err:=normalizeDependencies(context.Background(),self,[]ServiceDependency{{name,true}},read);err==nil{t.Errorf("accepted %q",name)}
    }
    body:=string(dependencyContent(got))
    for _,want:=range []string{"[Unit]\n","After=mysql.service redis.service\n","Requires=mysql.service\n","Wants=redis.service\n"}{if !strings.Contains(body,want){t.Errorf("missing %q",want)}}
    if len(dependencyContent(nil))!=0{t.Fatal("empty dependencies should remove the file")}
}
func TestDependencyFileDoesNotTouchOtherDropIns(t *testing.T){
    m:=Manager{serviceDir:t.TempDir()}
    path:=m.dependencyPath(1)
    if err:=writeDependencyFile(path,[]byte("[Unit]\nAfter=mysql.service\n"));err!=nil{t.Fatal(err)}
    other:=filepath.Join(filepath.Dir(path),"90-custom.conf")
    if err:=os.WriteFile(other,[]byte("custom"),0644);err!=nil{t.Fatal(err)}
    if err:=writeDependencyFile(path,[]byte("[Unit]\nAfter=redis.service\n"));err!=nil{t.Fatal(err)}
    if err:=m.removeDependencyFile(1);err!=nil{t.Fatal(err)}
    if _,err:=os.Stat(path);!errors.Is(err,os.ErrNotExist){t.Fatal("managed file was not removed")}
    if data,err:=os.ReadFile(other);err!=nil||string(data)!="custom"{t.Fatal("unmanaged drop-in changed")}
}
func TestDependenciesAreBackwardCompatibleJSON(t *testing.T){
    var cfg ServiceConfig
    if err:=json.Unmarshal([]byte("{\"mode\":\"form\",\"run_mode\":\"go-air\"}"),&cfg);err!=nil||len(cfg.Dependencies)!=0{t.Fatal("old service state incompatible")}
    cfg.Dependencies=[]ServiceDependency{{"mysql.service",true}}
    data,err:=json.Marshal(cfg);if err!=nil{t.Fatal(err)}
    var restored ServiceConfig
    if err=json.Unmarshal(data,&restored);err!=nil||!reflect.DeepEqual(cfg,restored){t.Fatal("dependency settings lost")}
}
func TestRuntimeAvailability(t *testing.T){
    tests:=[]struct{process string;port,ready bool;age time.Duration;want string}{
        {"inactive",true,false,time.Hour,"inactive"},{"failed",true,true,time.Hour,"failed"},
        {"activating",true,false,time.Hour,"activating"},{"active",false,false,time.Hour,"active"},
        {"active",true,true,0,"active"},{"active",true,false,StartupGrace-time.Second,"waiting-port"},
        {"active",true,false,StartupGrace,"unavailable"},{"active",true,false,-1,"unavailable"},
    }
    for _,tt:=range tests{if got:=runtimeAvailability(tt.process,tt.port,tt.ready,tt.age);got!=tt.want{t.Errorf("%+v: %s",tt,got)}}
}
