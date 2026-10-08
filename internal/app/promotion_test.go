package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func developmentAndProduction() (App, App) {
	dev := App{
		ID: 1, Type: "go", Name: "api-dev", Root: "/home/demo/apps/api-dev",
		Service: ServiceConfig{Mode: "form", RunMode: "go-air"},
	}
	prod := App{
		ID: 2, Type: "go", Name: "api-prod", Root: "/home/demo/apps/api-prod",
		Service: ServiceConfig{Mode: "form", RunMode: "go-binary"},
	}
	return dev, prod
}

func TestValidateDevToProductionPair(t *testing.T) {
	dev, prod := developmentAndProduction()
	if err := validatePromotion(dev, prod, "./cmd/server"); err != nil {
		t.Fatalf("valid dev-to-prod pair rejected: %v", err)
	}
	same := dev
	same.ID = prod.ID
	if err := validatePromotion(same, prod, "."); err == nil {
		t.Fatal("promotion to the same application accepted")
	}
	noAir := dev
	noAir.Service.RunMode = "go-build"
	if err := validatePromotion(noAir, prod, "."); err == nil {
		t.Fatal("production promotion without Air development rejected incorrectly")
	}
	noBinary := prod
	noBinary.Service.RunMode = "go-build"
	if err := validatePromotion(dev, noBinary, "."); err == nil {
		t.Fatal("a target that rebuilds on every restart was accepted")
	}
	otherDir := prod
	otherDir.Service.WorkingDirectory = "/tmp/another-directory"
	if err := validatePromotion(dev, otherDir, "."); err == nil {
		t.Fatal("a target with incompatible working directory was accepted")
	}
	other := prod
	other.Root = dev.Root
	if err := validatePromotion(dev, other, "."); err == nil {
		t.Fatal("overlapping source and production root accepted")
	}
	nested := prod
	nested.Root = filepath.Join(dev.Root, "prod")
	if err := validatePromotion(dev, nested, "."); err == nil {
		t.Fatal("nested production directory accepted")
	}
}

func TestNormalizePromotionGoPackage(t *testing.T) {
	for _, tc := range []struct {
		input, want string
	}{
		{"", "."}, {".", "."}, {"./cmd/server", "./cmd/server"},
		{"./app", "./app"},
	} {
		got, err := normalizeGoPackage(tc.input)
		if err != nil || got != tc.want {
			t.Fatalf("normalize %q = %q, %v; want %q", tc.input, got, err, tc.want)
		}
	}
	for _, pkg := range []string{"/etc", "../secrets", "./../secrets", "./cmd/../prod",
		"./cmd//app", "-C /tmp", "./*", "./cmd\nserver"} {
		if _, err := normalizeGoPackage(pkg); err == nil {
			t.Errorf("invalid package %q accepted", pkg)
		}
	}
}

func TestInstallProductionBinaryKeepsOtherProductionFiles(t *testing.T) {
	dir := t.TempDir()
	dev := filepath.Join(dir, "dev")
	prod := filepath.Join(dir, "prod")
	if err := os.Mkdir(dev, 0750); err != nil {t.Fatal(err)}
	if err := os.Mkdir(prod, 0750); err != nil {t.Fatal(err)}
	if err := os.WriteFile(filepath.Join(dev, "production-bin"), []byte("new Go executable"), 0750); err != nil {t.Fatal(err)}
	if err := os.WriteFile(filepath.Join(prod, ".ogp-app"), []byte("previous binary"), 0750); err != nil {t.Fatal(err)}
	if err := os.WriteFile(filepath.Join(prod, ".env"), []byte("PROD_SECRET=unchanged"), 0600); err != nil {t.Fatal(err)}
	if err := os.Mkdir(filepath.Join(prod,"data"),0700); err != nil {t.Fatal(err)}
	if err := os.WriteFile(filepath.Join(prod,"data","session"),[]byte("production state"),0600); err != nil {t.Fatal(err)}
	prepared, err := stageProductionBinary(filepath.Join(dev, "production-bin"), prod)
	if err != nil {t.Fatal(err)}
	if err := activateProductionBinary(prod, prepared, func() error {return nil}, nil); err != nil {t.Fatal(err)}
	data, err := os.ReadFile(filepath.Join(prod, ".ogp-app"))
	if err != nil || string(data)!="new Go executable" {t.Fatalf("unexpected prod binary %q: %v",data,err)}
	config, err := os.ReadFile(filepath.Join(prod, ".env"))
	if err != nil || string(config)!="PROD_SECRET=unchanged" {t.Fatal("production environment was modified")}
	session, err := os.ReadFile(filepath.Join(prod, "data","session"))
	if err != nil || string(session)!="production state" {t.Fatal("production data was modified")}
	info, err := os.Stat(filepath.Join(prod, ".ogp-app"))
	if err != nil || info.Mode().Perm()!=0750 {t.Fatalf("binary permission is wrong: %v %v",info,err)}
}

func TestSnapshotPromotionDoesNotModifyAirSource(t *testing.T) {
	dir:=t.TempDir()
	root:=filepath.Join(dir,"go-dev")
	if err:=os.Mkdir(root,0750);err!=nil{t.Fatal(err)}
	if err:=os.WriteFile(filepath.Join(root,"main.go"),[]byte("package main\n"),0640);err!=nil{t.Fatal(err)}
	stage,err:=stageApplication(context.Background(),App{Name:"go-dev",Root:root})
	if err!=nil{t.Fatal(err)}
	defer os.RemoveAll(stage)
	if err:=os.WriteFile(filepath.Join(stage,"main.go"),[]byte("changed snapshot"),0640);err!=nil{t.Fatal(err)}
	data,err:=os.ReadFile(filepath.Join(root,"main.go"))
	if err!=nil||strings.TrimSpace(string(data))!="package main"{t.Fatalf("Air working tree mutated: %q (%v)",data,err)}
}

func TestGoProductionActivationFailureRestoresOriginalBinary(t *testing.T) {
	root:=t.TempDir()
	old:=filepath.Join(root,".ogp-app")
	if err:=os.WriteFile(old,[]byte("old working executable"),0750);err!=nil{t.Fatal(err)}
	newPath:=filepath.Join(root,".ogp-next")
	if err:=os.WriteFile(newPath,[]byte("new broken executable"),0750);err!=nil{t.Fatal(err)}
	restarts:=0
	err:=activateProductionBinary(root,newPath,func()error{return errors.New("service readiness failed")},func()error{restarts++;return nil})
	if err==nil||!strings.Contains(err.Error(),"previous executable restored"){t.Fatalf("missing recovery: %v",err)}
	if restarts!=1{t.Fatalf("old service restart count=%d, expected 1",restarts)}
	b,err:=os.ReadFile(old)
	if err!=nil||string(b)!="old working executable"{t.Fatalf("previous binary not restored: %q %v",b,err)}
}

func TestGoProductionActivationLeavesPersistentFileChangesIntact(t *testing.T) {
	root:=t.TempDir()
	dataFile:=filepath.Join(root,"sessions.json")
	if err:=os.WriteFile(dataFile,[]byte("first"),0600);err!=nil{t.Fatal(err)}
	old:=filepath.Join(root,".ogp-app")
	if err:=os.WriteFile(old,[]byte("old executable"),0750);err!=nil{t.Fatal(err)}
	prepared:=filepath.Join(root,".ogp-next")
	if err:=os.WriteFile(prepared,[]byte("new executable"),0750);err!=nil{t.Fatal(err)}
	if err:=activateProductionBinary(root,prepared,func()error{
		// Production writes to this directory while the new executable is
		// activating; swapping the entire directory would lose this write.
		return os.WriteFile(dataFile,[]byte("new session state"),0600)
	},nil);err!=nil{t.Fatal(err)}
	data,err:=os.ReadFile(dataFile)
	if err!=nil||string(data)!="new session state"{t.Fatalf("persistent data lost: %q %v",data,err)}
	oldBinary,err:=os.ReadFile(filepath.Join(root,".ogp-app.previous"))
	if err!=nil||string(oldBinary)!="old executable"{t.Fatalf("old binary backup missing: %q %v",oldBinary,err)}
}

func TestFirstProductionActivationFailureDoesNotInventRollback(t *testing.T) {
	root:=t.TempDir()
	prepared:=filepath.Join(root,".ogp-new-binary")
	if err:=os.WriteFile(prepared,[]byte("first build"),0750);err!=nil{t.Fatal(err)}
	recovered:=false
	err:=activateProductionBinary(root,prepared,func()error{return errors.New("app failed to listen")},func()error{recovered=true;return nil})
	if err==nil||!strings.Contains(err.Error(),"no previous binary exists"){t.Fatalf("incorrect first-deploy failure: %v",err)}
	if recovered{t.Fatal("attempted to restart a previous binary that does not exist")}
	if _,err:=os.Lstat(filepath.Join(root,".ogp-app"));!os.IsNotExist(err){t.Fatalf("failed first build still active: %v",err)}
}

func TestProductionBinaryManualRollbackSwap(t *testing.T) {
	root:=t.TempDir()
	active:=filepath.Join(root,".ogp-app")
	previous:=filepath.Join(root,".ogp-app.previous")
	for path,value:=range map[string]string{active:"new binary",previous:"old binary"}{
		if err:=os.WriteFile(path,[]byte(value),0750);err!=nil{t.Fatal(err)}
	}
	restartCalled:=0
	if err:=activateStagedRelease(active,previous,func()error{restartCalled++;return nil},nil);err!=nil{
		t.Fatal(err)
	}
	nowActive,_:=os.ReadFile(active)
	nowPrevious,_:=os.ReadFile(previous)
	if string(nowActive)!="old binary"||string(nowPrevious)!="new binary"||restartCalled!=1{
		t.Fatalf("rollback exchange failed: active=%q previous=%q restarts=%d",nowActive,nowPrevious,restartCalled)
	}
}
