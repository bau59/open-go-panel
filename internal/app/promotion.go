package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
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

	m.setPromotionStep("Preparing production binary")
	prepared,err:=stageProductionBinary(outputBinary,target.Root)
	if err!=nil{return fmt.Errorf("prepare production binary: %w",err)}
	defer os.Remove(prepared)
	if err:=ctx.Err();err!=nil{return err}
	m.setPromotionStep("Switching production binary")
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
	return activateProductionBinary(target.Root,prepared,activate,recoverOriginal)
}

// stageProductionBinary creates a complete executable next to production's
// current binary on the same filesystem. No other production file is copied,
// so runtime writes, uploads and persistent data cannot be lost on release.
func stageProductionBinary(sourceBinary,root string) (string,error) {
	src,err:=os.Open(sourceBinary)
	if err!=nil{return "",err}
	defer src.Close()
	info,err:=src.Stat()
	if err!=nil{return "",err}
	if !info.Mode().IsRegular()||info.Size()==0 {
		return "",errors.New("Go build did not produce a valid binary")
	}
	rootInfo,err:=os.Lstat(root)
	if err!=nil{return "",err}
	if !rootInfo.IsDir(){return "",errors.New("production root must be a real directory")}
	owner,ok:=rootInfo.Sys().(*syscall.Stat_t)
	if !ok{return "",errors.New("production root ownership is unavailable")}
	dst,err:=os.CreateTemp(root,".ogp-new-binary-")
	if err!=nil{return "",err}
	defer func(){_ = dst.Close()}()
	fail:=func(err error)(string,error){_ = os.Remove(dst.Name());return "",err}
	if _,err:=io.Copy(dst,src);err!=nil{return fail(err)}
	if err:=dst.Chmod(0750);err!=nil{return fail(err)}
	if err:=os.Chown(dst.Name(),int(owner.Uid),int(owner.Gid));err!=nil{
		return fail(fmt.Errorf("set production binary ownership: %w",err))
	}
	if err:=dst.Sync();err!=nil{return fail(err)}
	if err:=dst.Close();err!=nil{return fail(err)}
	return dst.Name(),nil
}

// activateProductionBinary atomically replaces only .ogp-app while keeping all
// production directories, secrets and data in place. The prior executable is
// retained at .ogp-app.previous for a manual rollback.
func activateProductionBinary(root,prepared string,activate func()error,recoverOriginal func()error) error {
	if filepath.Dir(prepared)!=root {
		return errors.New("staged Go executable must be in the production root")
	}
	current:=filepath.Join(root,".ogp-app")
	previous:=filepath.Join(root,".ogp-app.previous")
	existing,err:=os.Lstat(current)
	hadPrevious:=err==nil
	if err!=nil && !errors.Is(err,os.ErrNotExist){return err}
	if hadPrevious {
		if !existing.Mode().IsRegular(){return errors.New("existing production executable must be a regular file")}
		backup,err:=os.CreateTemp(root,".ogp-backup-")
		if err!=nil{return err}
		backupName:=backup.Name()
		_ = backup.Close()
		_ = os.Remove(backupName)
		if err:=os.Link(current,backupName);err!=nil{return fmt.Errorf("save previous executable: %w",err)}
		defer os.Remove(backupName)
		if err:=os.Rename(backupName,previous);err!=nil{return fmt.Errorf("persist previous executable: %w",err)}
	}
	if err:=os.Rename(prepared,current);err!=nil{
		return fmt.Errorf("activate compiled executable: %w",err)
	}
	if err:=activate();err!=nil{
		var restoreErr error
		if hadPrevious {
			restoreErr=os.Rename(previous,current)
		} else {
			restoreErr=os.Remove(current)
		}
		if restoreErr!=nil {
			return errors.Join(err,fmt.Errorf("critical: cannot restore previous production binary: %w",restoreErr))
		}
		if recoverOriginal!=nil {
			if restartErr:=recoverOriginal();restartErr!=nil {
				return errors.Join(err,fmt.Errorf("previous executable restored but service restart failed: %w",restartErr))
			}
		}
		return fmt.Errorf("production activation failed; previous executable restored: %w",err)
	}
	return nil
}

// RollbackProductionBinary swaps the most recent promoted executable back
// atomically, with the same service restart and health checks as promotion.
func (m *Manager) RollbackProductionBinary(ctx context.Context,id int64)error{
	m.deployMu.Lock()
	defer m.deployMu.Unlock()
	app,err:=m.Get(id)
	if err!=nil{return err}
	if app.Type!="go"||app.Service.RunMode!="go-binary"||app.Service.Mode=="raw" {
		return errors.New("application is not in Go production binary mode")
	}
	current:=filepath.Join(app.Root,".ogp-app")
	previous:=filepath.Join(app.Root,".ogp-app.previous")
	for _,path:=range []string{current,previous}{
		info,err:=os.Lstat(path)
		if err!=nil{return fmt.Errorf("production rollback: %w",err)}
		if !info.Mode().IsRegular(){return errors.New("production rollback requires regular executable files")}
	}
	activate:=func()error{
		if err:=m.Restart(ctx,id);err!=nil{return err}
		return m.waitForRelease(ctx,app)
	}
	recoverOriginal:=func()error{
		recoveryCtx,cancel:=context.WithTimeout(context.Background(),time.Minute)
		defer cancel()
		return m.Restart(recoveryCtx,id)
	}
	return activateStagedRelease(current,previous,activate,recoverOriginal)
}
