package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// PromotionTask describes one asynchronous dev-to-production binary release.
// No credentials or environment variable values are included in this state.
type PromotionTask struct {
	Running    bool
	SourceID   int64
	TargetID   int64
	Step       string
	Error      string
	StartedAt  time.Time
	FinishedAt time.Time
}

func (m *Manager) PromotionStatus() PromotionTask {
	m.promotionMu.Lock()
	defer m.promotionMu.Unlock()
	return m.promotionTask
}

func (m *Manager) PromotionTargets(sourceID int64) ([]App, error) {
	apps, err := m.List()
	if err != nil {
		return nil, err
	}
	var targets []App
	for _, app := range apps {
		if app.ID != sourceID && app.Type == "go" &&
			app.Service.Mode != "raw" && app.Service.RunMode == "go-binary" {
			targets = append(targets, app)
		}
	}
	return targets, nil
}

func validatePromotion(source, target App, pkg string) error {
	if source.ID == target.ID || source.Root == target.Root {
		return errors.New("development and production must be different applications")
	}
	if source.Type != "go" || source.Service.RunMode != "go-air" ||
		source.Service.Mode == "raw" {
		return errors.New("source must be a Go application using Air live reload")
	}
	if target.Type != "go" || target.Service.RunMode != "go-binary" ||
		target.Service.Mode == "raw" {
		return errors.New("production must be a separate Go application with the prebuilt binary run mode")
	}
	if target.Service.WorkingDirectory != "" &&
		filepath.Clean(target.Service.WorkingDirectory) != filepath.Clean(target.Root) {
		return errors.New("production working directory must be the application root")
	}
	if !filepath.IsAbs(source.Root) || !filepath.IsAbs(target.Root) {
		return errors.New("application roots must be absolute")
	}
	if _, err := normalizeGoPackage(pkg); err != nil {
		return err
	}
	return nil
}

func normalizeGoPackage(pkg string) (string, error) {
	pkg = strings.TrimSpace(pkg)
	if pkg == "" || pkg == "." {
		return ".", nil
	}
	if !strings.HasPrefix(pkg, "./") || strings.ContainsAny(pkg, "\r\n\x00*?[]") {
		return "", errors.New("Go build package must be . or a relative directory such as ./cmd/server")
	}
	clean := filepath.Clean(pkg)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", errors.New("Go build package must stay within development source")
	}
	for _, segment := range strings.Split(strings.TrimPrefix(pkg, "./"), "/") {
		if segment == "." || segment == ".." || segment == "" {
			return "", errors.New("invalid Go package directory")
		}
	}
	return "./" + strings.TrimPrefix(clean, "./"), nil
}

func (m *Manager) StartPromotion(sourceID, targetID int64, pkg string) error {
	source, err := m.Get(sourceID)
	if err != nil { return err }
	target, err := m.Get(targetID)
	if err != nil { return err }
	if err := validatePromotion(source, target, pkg); err != nil { return err }
	pkg, err = normalizeGoPackage(pkg)
	if err != nil { return err }
	m.promotionMu.Lock()
	defer m.promotionMu.Unlock()
	if m.promotionTask.Running {
		return errors.New("another Dev → Production build is already in progress")
	}
	m.promotionTask = PromotionTask{Running:true,SourceID:sourceID,TargetID:targetID,
		Step:"Preparing development snapshot",StartedAt:time.Now().UTC()}
	go m.promoteAsync(sourceID, targetID, pkg)
	return nil
}

func (m *Manager) setPromotionStep(step string) {
	m.promotionMu.Lock()
	m.promotionTask.Step = step
	m.promotionMu.Unlock()
}

func (m *Manager) promoteAsync(sourceID, targetID int64, pkg string) {
	ctx,cancel:=context.WithTimeout(context.Background(),30*time.Minute)
	defer cancel()
	// Serialize with Git deployments/rollbacks because both exchange the
	// production app directory. Do not lock the dev service or its files.
	m.deployMu.Lock()
	err:=m.promoteGo(ctx,sourceID,targetID,pkg)
	m.deployMu.Unlock()
	m.promotionMu.Lock()
	defer m.promotionMu.Unlock()
	m.promotionTask.Running=false
	m.promotionTask.FinishedAt=time.Now().UTC()
	if err != nil {
		m.promotionTask.Step="Failed"
		m.promotionTask.Error=err.Error()
	} else {
		m.promotionTask.Step="Production updated"
	}
}

func (m *Manager) promoteGo(ctx context.Context,sourceID,targetID int64,pkg string) error {
	source,err:=m.Get(sourceID)
	if err!=nil{return err}
	target,err:=m.Get(targetID)
	if err!=nil{return err}
	if err:=validatePromotion(source,target,pkg);err!=nil{return err}
	goTool,err:=resolveDeployGo()
	if err!=nil{return err}

	// Snapshot rather than building inside Air's watched development tree:
	// Go compilation never touches the working dev directory.
	sourceStage,err:=stageApplication(ctx,source)
	if err!=nil{return fmt.Errorf("snapshot development source: %w",err)}
	defer os.RemoveAll(sourceStage)

	if _,err:=os.Stat(filepath.Join(sourceStage,"go.mod"));err!=nil{
		return fmt.Errorf("development source requires go.mod: %w",err)
	}
	packageDir,err:=normalizeGoPackage(pkg)
	if err!=nil{return err}
	if packageDir!="." {
		path:=filepath.Join(sourceStage,strings.TrimPrefix(packageDir,"./"))
		resolved,err:=filepath.EvalSymlinks(path)
		if err!=nil{return fmt.Errorf("resolve Go build package: %w",err)}
		rel,err:=filepath.Rel(sourceStage,resolved)
		if err!=nil||rel==".."||strings.HasPrefix(rel,".."+string(os.PathSeparator)){
			return errors.New("Go package directory escapes source snapshot")
		}
		if stat,err:=os.Stat(resolved);err!=nil||!stat.IsDir(){
			return errors.New("Go package must be a directory")
		}
	}

	account,err:=user.Lookup(source.User)
	if err!=nil{return err}
	uid,err:=strconv.Atoi(account.Uid)
	if err!=nil{return err}
	gid,err:=strconv.Atoi(account.Gid)
	if err!=nil{return err}
	outputDir,err:=os.MkdirTemp(sourceStage,".ogp-build-")
	if err!=nil{return err}
	if err:=os.Chown(outputDir,uid,gid);err!=nil{return err}
	outputBinary:=filepath.Join(outputDir,"production-bin")
	m.setPromotionStep("Compiling Go binary from dev snapshot")
	if _,err:=runAsUser(ctx,source.User,sourceStage,goTool,"build","-trimpath","-o",outputBinary,packageDir);err!=nil{
		return fmt.Errorf("build production binary: %w",err)
	}

	m.setPromotionStep("Preparing production release")
	targetStage,err:=stageApplication(ctx,target)
	if err!=nil{return fmt.Errorf("snapshot production release: %w",err)}
	defer os.RemoveAll(targetStage)
	if err:=installProductionBinary(outputBinary,targetStage);err!=nil{
		return fmt.Errorf("prepare production binary: %w",err)
	}
	if err:=ctx.Err();err!=nil{return err}
	m.setPromotionStep("Switching production release")
	activate:=func()error{
		if err:=m.Restart(ctx,target.ID);err!=nil{
			return fmt.Errorf("restart production: %w",err)
		}
		return m.waitForRelease(ctx,target)
	}
	recoverOriginal:=func()error{
		recoveryCtx,cancel:=context.WithTimeout(context.Background(),time.Minute)
		defer cancel()
		return m.Restart(recoveryCtx,target.ID)
	}
	return activateStagedRelease(target.Root,targetStage,activate,recoverOriginal)
}

func installProductionBinary(sourceBinary,stage string) error {
	src,err:=os.Open(sourceBinary)
	if err!=nil{return err}
	defer src.Close()
	info,err:=src.Stat()
	if err!=nil{return err}
	if !info.Mode().IsRegular()||info.Size()==0 {
		return errors.New("Go build did not produce a valid binary")
	}
	stageInfo,err:=os.Stat(stage)
	if err!=nil{return err}
	stat,ok:=stageInfo.Sys().(interface{Uid() uint32; Gid() uint32})
	_ = stat
	_ = ok
	// The release root belongs to its production user. Set ownership of the
	// replacement binary to that same user before activation.
	dst,err:=os.CreateTemp(stage,".ogp-new-binary-")
	if err!=nil{return err}
	defer os.Remove(dst.Name())
	if _,err:=io.Copy(dst,src);err!=nil{_ = dst.Close();return err}
	if err:=dst.Chmod(0750);err!=nil{_ = dst.Close();return err}
	if err:=dst.Sync();err!=nil{_ = dst.Close();return err}
	if err:=dst.Close();err!=nil{return err}
	if err:=exec.Command("chown","--reference="+stage,dst.Name()).Run();err!=nil{
		return fmt.Errorf("set production binary ownership: %w",err)
	}
	return os.Rename(dst.Name(),filepath.Join(stage,".ogp-app"))
}
