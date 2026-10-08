package docker

import (
    "os"
    "path/filepath"
    "strings"
    "testing"
)

func TestParseEnvironment(t *testing.T) {
    got, err := parseEnvironment(" # note\r\nBRIDGE_API_KEYS=sk-test-value\r\nSTATE_ENCRYPTION_KEY=abc=def\r\n\n")
    if err != nil {
        t.Fatal(err)
    }
    if got != "BRIDGE_API_KEYS=sk-test-value\nSTATE_ENCRYPTION_KEY=abc=def" {
        t.Fatalf("unexpected environment: %q", got)
    }
    for _, tc := range []string{"NO_EQUALS", "3INVALID=hi", "GOOD=ok\nGOOD=again", "A=ok\nBAD-KEY=no", strings.Repeat("A",maxEnvironmentBytes+1)} {
        if _,err := parseEnvironment(tc); err == nil {
            t.Fatalf("accepted invalid configuration: %q", tc[:min(len(tc),80)])
        }
    }
}

func TestParseVolumes(t *testing.T) {
    mounts,err := parseVolumes("/opt/deepseek-bridge/data:/app/data\nbridge_cache:/app/cache")
    if err != nil {
        t.Fatal(err)
    }
    if len(mounts)!=2 || mounts[0].Named || !mounts[1].Named ||
       mounts[0].Source!="/opt/deepseek-bridge/data" || mounts[0].Target!="/app/data"{
        t.Fatalf("wrong mounts: %#v", mounts)
    }
    for _, tc := range []string{"/var/lib/data:app/data", "relative/file:/app/data", "/opt/data:/app/data:/extra", "/:/app/data",
        "/opt/../etc:/app/data", "/opt/foo:/", "cache:/app/data\nother:/app/data", "bad,name:/app/data", "data:/app/data,ro"} {
        if _,err := parseVolumes(tc); err == nil {
            t.Fatalf("accepted invalid mount %q",tc)
        }
    }
}

func TestRuntimeArgsSecretEnvironmentAndDataMount(t *testing.T) {
    dir := filepath.Join(t.TempDir(),"deepseek","data")
    cfg := RuntimeConfig{Environment:"BRIDGE_API_KEYS=sk-private-value\nSTATE_ENCRYPTION_KEY=fernet-secret",
        Volumes:dir+":/app/data\nbridge_cache:/app/cache",Init:true,ShmSize:"512m"}
    env,mounts,err := cfg.validate()
    if err != nil { t.Fatal(err) }
    args,cleanup,err := runtimeArgs(cfg,env,mounts)
    if err != nil { t.Fatal(err) }
    defer cleanup()
    command := strings.Join(args," ")
    if strings.Contains(command,"sk-private-value")||strings.Contains(command,"fernet-secret") {
        t.Fatal("secrets exposed in process arguments")
    }
    for _, required := range []string{"--init","--shm-size 512m","type=bind,src="+dir+",dst=/app/data",
        "type=volume,src=bridge_cache,dst=/app/cache","--env-file"} {
        if !strings.Contains(command,required) {t.Fatalf("missing %q in %q",required,command)}
    }
    idx:=-1
    for i,arg:=range args {if arg=="--env-file" { idx=i; break }}
    if idx<0 || idx+1>=len(args) {t.Fatal("missing env-file argument")}
    envFile:=args[idx+1]
    info,err:=os.Stat(envFile)
    if err!=nil {t.Fatal(err)}
    if info.Mode().Perm()!=0600 {t.Fatalf("env file is %o, want 0600",info.Mode().Perm())}
    fileContent,err:=os.ReadFile(envFile)
    if err!=nil {t.Fatal(err)}
    if string(fileContent)!=env+"\n" {t.Fatal("environment contents changed")}
    dataDir,err:=os.Stat(dir)
    if err!=nil {t.Fatal(err)}
    if dataDir.Mode().Perm()!=0700 {t.Fatalf("host directory mode %o, want 0700",dataDir.Mode().Perm())}
    cleanup()
    if _,err:=os.Stat(envFile);!os.IsNotExist(err){t.Fatalf("temporary secret file still exists: %v",err)}
}

func TestRuntimeConfigValidation(t *testing.T) {
    for _,shm:=range []string{"0m","not-a-size","512","-1g","1000000m"} {
        if _,_,err:= (RuntimeConfig{ShmSize:shm}).validate();err==nil {
            t.Fatalf("accepted invalid shm size %q",shm)
        }
    }
    if _,_,err := (RuntimeConfig{ShmSize:"1g",Volumes:"named_data:/app/data"}).validate();err!=nil {
        t.Fatal(err)
    }
}

func TestExistingEnvironmentFile(t *testing.T) {
    path := filepath.Join(t.TempDir(), ".env")
    const secrets = "BRIDGE_API_KEYS=sk-original\nSTATE_ENCRYPTION_KEY=original-fernet-key\n"
    if err := os.WriteFile(path, []byte(secrets), 0600); err != nil { t.Fatal(err) }
    cfg := RuntimeConfig{EnvironmentFile: path, Volumes: "bridge_data:/app/data"}
    env, mounts, err := cfg.validate()
    if err != nil { t.Fatal(err) }
    if env != "" { t.Fatal("server env file should not be copied into runtime strings") }
    args, cleanup, err := runtimeArgs(cfg, env, mounts)
    if err != nil { t.Fatal(err) }
    cleanup()
    if !strings.Contains(strings.Join(args, " "), "--env-file "+path) {
        t.Fatalf("existing env file path absent from flags: %#v", args)
    }
    content, err := os.ReadFile(path)
    if err != nil { t.Fatal(err) }
    if string(content) != secrets {
        t.Fatal("existing secret file was modified or removed")
    }
    cfg.Environment = "OTHER=one"
    if _,_,err:=cfg.validate(); err==nil {t.Fatal("accepted both inline and file environment")}
    cfg.Environment = ""
    if err:=os.Chmod(path,0644);err!=nil{t.Fatal(err)}
    if _,_,err:=cfg.validate();err==nil{t.Fatal("accepted world-readable secrets")}
    cfg.EnvironmentFile = "relative/.env"
    if _,_,err:=cfg.validate();err==nil{t.Fatal("accepted relative env file")}
}
