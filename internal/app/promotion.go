package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"os/exec"
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
	for _, pair := range [][2]string{{source.Root, target.Root}, {target.Root, source.Root}} {
		relative, err := filepath.Rel(pair[0], pair[1])
		if err == nil && (relative == "." || relative == ".." ||
			!strings.HasPrefix(relative, ".."+string(os.PathSeparator)) && !filepath.IsAbs(relative)) {
			return errors.New("development and production directories cannot overlap")
		}
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

// PromotionGoPackage returns the saved build package for a development app.
func (m *Manager) PromotionGoPackage(sourceID int64) (string, error) {
	if _, err := m.Get(sourceID); err != nil {
		return "", err
	}
	var pkg string
	err := m.store.DB().QueryRow("SELECT value FROM settings WHERE key = ?", fmt.Sprintf("promotion.go_package.%d", sourceID)).Scan(&pkg)
	if errors.Is(err, sql.ErrNoRows) {
		return ".", nil
	}
	if err != nil {
		return "", fmt.Errorf("read Go build package: %w", err)
	}
	return normalizeGoPackage(pkg)
}

func (m *Manager) savePromotionGoPackage(sourceID int64, pkg string) error {
	_, err := m.store.DB().Exec(
		"INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value",
		fmt.Sprintf("promotion.go_package.%d", sourceID), pkg,
	)
	if err != nil {
		return fmt.Errorf("save Go build package: %w", err)
	}
	return nil
}

// PromotionDirectories stores relative runtime directories to copy with a compiled release.
func (m *Manager) PromotionDirectories(sourceID int64) (string, error) {
	if _, err := m.Get(sourceID); err != nil { return "", err }
	var value string
	err := m.store.DB().QueryRow("SELECT value FROM settings WHERE key = ?", fmt.Sprintf("promotion.directories.%d", sourceID)).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) { return "", nil }
	return value, err
}

func normalizePromotionDirectories(raw string) ([]string, error) {
	var dirs []string
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		dir := strings.TrimSpace(line)
		if dir == "" { continue }
		if filepath.IsAbs(dir) || dir == "." || strings.ContainsAny(dir, "\\:*?[]\x00") {
			return nil, fmt.Errorf("invalid copy directory %q", dir)
		}
		clean := filepath.Clean(dir)
		if clean == ".." || strings.HasPrefix(clean, "../") || clean != dir || strings.HasPrefix(dir, ".") {
			return nil, fmt.Errorf("unsafe copy directory %q", dir)
		}
		for _, part := range strings.Split(dir, "/") {
			if part == "." || part == ".." || part == "" { return nil, fmt.Errorf("invalid copy directory %q", dir) }
		}
		if dir == "data" || dir == "uploads" || dir == "storage" || dir == "vendor" || dir == "node_modules" || strings.HasPrefix(dir, ".ogp") {
			return nil, fmt.Errorf("runtime data directory %q is not a release asset", dir)
		}
		for _, existing := range dirs {
			if dir == existing { return nil, fmt.Errorf("duplicate copy directory %q", dir) }
			if strings.HasPrefix(dir, existing+"/") || strings.HasPrefix(existing, dir+"/") {
				return nil, fmt.Errorf("overlapping copy directories %q and %q", dir, existing)
			}
		}
		dirs = append(dirs, dir)
	}
	if len(dirs) > 16 { return nil, errors.New("too many copy directories") }
	return dirs, nil
}

func (m *Manager) savePromotionDirectories(sourceID int64, dirs []string) error {
	_, err := m.store.DB().Exec("INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value",
		fmt.Sprintf("promotion.directories.%d", sourceID), strings.Join(dirs, "\n"))
	return err
}

func (m *Manager) StartPromotion(sourceID, targetID int64, pkg string, directories ...string) error {
	source, err := m.Get(sourceID)
	if err != nil { return err }
	target, err := m.Get(targetID)
	if err != nil { return err }
	if err := validatePromotion(source, target, pkg); err != nil { return err }
	pkg, err = normalizeGoPackage(pkg)
	if err != nil { return err }
	raw := ""
	if len(directories) > 0 { raw = directories[0] }
	dirs, err := normalizePromotionDirectories(raw)
	if err != nil { return err }
	m.promotionMu.Lock()
	defer m.promotionMu.Unlock()
	if m.promotionTask.Running {
		return errors.New("another Dev → Production build is already in progress")
	}
	if err := m.savePromotionGoPackage(sourceID, pkg); err != nil { return err }
	if err := m.savePromotionDirectories(sourceID, dirs); err != nil { return err }
	m.promotionTask = PromotionTask{Running:true,SourceID:sourceID,TargetID:targetID,
		Step:"Preparing development snapshot",StartedAt:time.Now().UTC()}
	go m.promoteAsync(sourceID, targetID, pkg, dirs)
	return nil
}

func (m *Manager) setPromotionStep(step string) {
	m.promotionMu.Lock()
	m.promotionTask.Step = step
	m.promotionMu.Unlock()
}

func (m *Manager) promoteAsync(sourceID, targetID int64, pkg string, dirs []string) {
	ctx,cancel:=context.WithTimeout(context.Background(),30*time.Minute)
	defer cancel()
	// Serialize with Git deployments/rollbacks because both exchange the
	// production app directory. Do not lock the dev service or its files.
	m.deployMu.Lock()
	err:=m.promoteGoWithDirectories(ctx,sourceID,targetID,pkg,dirs)
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
	return m.promoteGoWithDirectories(ctx,sourceID,targetID,pkg,nil)
}

func (m *Manager) promoteGoWithDirectories(ctx context.Context,sourceID,targetID int64,pkg string,dirs []string) error {
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

	m.setPromotionStep("Preparing production binary and runtime directories")
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
	if len(dirs) == 0 { return activateProductionBinary(target.Root,prepared,activate,recoverOriginal) }
	return activateProductionRelease(ctx,sourceStage,target.Root,prepared,dirs,activate,recoverOriginal)
}

// activateProductionRelease stages selected directories and switches them with
// the binary. Backups remain available for the next manual binary rollback.
func activateProductionRelease(ctx context.Context, sourceStage, root, prepared string, dirs []string, activate, recover func() error) error {
	type asset struct { path, staged, backup string; existed, switched bool }
	items := make([]*asset, 0, len(dirs))
	releaseID := fmt.Sprintf("%d", time.Now().UnixNano())
	cleanup := func() { for _, a := range items { if !a.switched { _ = os.RemoveAll(a.staged) } } }
	defer cleanup()
	for _, dir := range dirs {
		src := filepath.Join(sourceStage, dir)
		info, err := os.Lstat(src)
		if err != nil { return fmt.Errorf("copy %s: %w", dir, err) }
		if !info.IsDir() { return fmt.Errorf("copy %s: source must be a real directory", dir) }
		dst := filepath.Join(root, dir)
		// Deny symlinked parents and destination to keep copied assets inside root.
		for parent := filepath.Dir(dst); parent != root; parent = filepath.Dir(parent) {
			st, err := os.Lstat(parent)
			if err == nil && !st.IsDir() { return fmt.Errorf("copy %s: destination parent is not a directory", dir) }
			if err != nil && !errors.Is(err, os.ErrNotExist) { return err }
		}
		old, err := os.Lstat(dst)
		if err != nil && !errors.Is(err, os.ErrNotExist) { return err }
		if err == nil && !old.IsDir() { return fmt.Errorf("copy %s: destination is not a real directory", dir) }
		stage := filepath.Join(root, ".ogp-asset-"+releaseID+"-"+strconv.Itoa(len(items)))
		cmd := exec.CommandContext(ctx, "cp", "-a", "--", src, stage)
		if output, err := cmd.CombinedOutput(); err != nil { return fmt.Errorf("stage asset %s: %w: %s",dir,err,strings.TrimSpace(string(output))) }
		if stat,ok := oldOwner(root); ok {
			if err := exec.CommandContext(ctx,"chown","-R",fmt.Sprintf("%d:%d",stat.Uid,stat.Gid),stage).Run(); err != nil { _ = os.RemoveAll(stage);return fmt.Errorf("set asset owner: %w",err) }
		}
		items = append(items,&asset{path:dst,staged:stage,backup:filepath.Join(root,".ogp-asset-backup-"+releaseID+"-"+strconv.Itoa(len(items))),existed:err==nil})
	}
	restore := func() error {
		var failures []error
		for i:=len(items)-1;i>=0;i-- {
			a:=items[i]
			if !a.switched { continue }
			if err:=os.RemoveAll(a.path);err!=nil {failures=append(failures,err);continue}
			if a.existed {if err:=os.Rename(a.backup,a.path);err!=nil { failures=append(failures,err) }}
			a.switched=false
		}
		return errors.Join(failures...)
	}
	for _, a := range items {
		if err:=os.MkdirAll(filepath.Dir(a.path),0750);err!=nil {_=restore();return err}
		if a.existed {if err:=os.Rename(a.path,a.backup);err!=nil {_=restore();return err}}
		if err:=os.Rename(a.staged,a.path);err!=nil {
			if a.existed {_=os.Rename(a.backup,a.path)}
			_ = restore();return err
		}
		a.switched=true
	}
	// Copy backups to a stable path only after successful activation; on
	// failure the former directories must be restored before restarting.
	err := activateProductionBinary(root,prepared,activate,func()error {
		restoreErr:=restore()
		if restoreErr!=nil { return restoreErr }
		if recover!=nil { return recover() }
		return nil
	})
	if err!=nil {
		if restoreErr:=restore();restoreErr!=nil {return errors.Join(err,restoreErr)}
		return err
	}
	// Persist asset rollback metadata; preserve backups until manual rollback.
	manifest:=filepath.Join(root,".ogp-assets-previous")
	previousDir:=manifest+".old"
	_ = os.RemoveAll(previousDir)
	if _,err:=os.Lstat(manifest);err==nil { if err:=os.Rename(manifest,previousDir);err!=nil{return err} }
	if err:=os.Mkdir(manifest,0750);err!=nil{return err}
	for i,a:=range items {
		if a.existed {
			if err:=os.Rename(a.backup,filepath.Join(manifest,strconv.Itoa(i)));err!=nil{return err}
		}
	}
	lines:=[]string{}
	for _,a:=range items {lines=append(lines,strings.TrimPrefix(a.path,root+string(os.PathSeparator)))}
	if err:=os.WriteFile(filepath.Join(manifest,"paths"),[]byte(strings.Join(lines,"\n")),0600);err!=nil{return err}
	_ = os.RemoveAll(previousDir)
	return nil
}

func oldOwner(root string) (*syscall.Stat_t,bool) {
	info,err:=os.Lstat(root)
	if err!=nil{return nil,false}
	owner,ok:=info.Sys().(*syscall.Stat_t)
	return owner,ok
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
		if !hadPrevious {
			return fmt.Errorf("first production activation failed; new executable removed, no previous binary exists: %w",err)
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

func (m *Manager) HasProductionRollback(id int64) bool {
	app,err:=m.Get(id)
	if err!=nil||app.Type!="go"||app.Service.RunMode!="go-binary" {return false}
	info,err:=os.Lstat(filepath.Join(app.Root,".ogp-app.previous"))
	return err==nil && info.Mode().IsRegular()
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
	if _, err := os.Lstat(filepath.Join(app.Root, ".ogp-assets-previous")); errors.Is(err, os.ErrNotExist) {
		return activateStagedRelease(current,previous,activate,recoverOriginal)
	} else if err != nil { return err }
	return rollbackProductionAssets(app.Root, current, previous, activate, recoverOriginal)
}

func rollbackProductionAssets(root, current, previous string, activate, recover func()error) error {
	manifest := filepath.Join(root, ".ogp-assets-previous")
	content,err:=os.ReadFile(filepath.Join(manifest,"paths"))
	if err!=nil{return fmt.Errorf("read previous assets: %w",err)}
	dirs,err:=normalizePromotionDirectories(string(content))
	if err!=nil{return err}
	// Move current assets into a temporary holder, restore previous assets,
	// then switch the binaries. A failed activation restores both sides.
	temporary,err:=os.MkdirTemp(root,".ogp-assets-rollback-")
	if err!=nil{return err}
	defer os.RemoveAll(temporary)
	type moved struct {current, saved, previous string; hadCurrent, hadPrevious bool}
	var movedAssets []moved
	restored:=false
	restore:=func() error {
		if restored { return nil }
		restored=true
		var errs []error
		for i:=len(movedAssets)-1;i>=0;i-- {
			a:=movedAssets[i]
			if a.hadPrevious {if err:=os.Rename(a.current,a.previous);err!=nil{errs=append(errs,err)}}
			if a.hadCurrent {if err:=os.Rename(a.saved,a.current);err!=nil{errs=append(errs,err)}}
		}
		return errors.Join(errs...)
	}
	for i,dir:=range dirs {
		dst:=filepath.Join(root,dir)
		old:=filepath.Join(manifest,strconv.Itoa(i))
		stash:=filepath.Join(temporary,strconv.Itoa(i))
		_,err:=os.Lstat(dst);hasCurrent:=err==nil
		if err!=nil&&!errors.Is(err,os.ErrNotExist){_ = restore();return err}
		_,err=os.Lstat(old);hasPrevious:=err==nil
		if err!=nil&&!errors.Is(err,os.ErrNotExist){_ = restore();return err}
		if hasCurrent {if err:=os.Rename(dst,stash);err!=nil{_ = restore();return err}}
		if hasPrevious {if err:=os.MkdirAll(filepath.Dir(dst),0750);err!=nil{_ = restore();return err}
			if err:=os.Rename(old,dst);err!=nil{if hasCurrent{_ = os.Rename(stash,dst)};_ = restore();return err}}
		movedAssets=append(movedAssets,moved{dst,stash,old,hasCurrent,hasPrevious})
	}
	if err:=activateStagedRelease(current,previous,activate,func()error{
		if e:=restore();e!=nil{return e}
		if recover!=nil{return recover()}
		return nil
	});err!=nil {
		if e:=restore();e!=nil{return errors.Join(err,e)}
		return err
	}
	// The rollback itself is reversible: keep the displaced directories in
	// the same manifest for the next binary rollback.
	for i,a:=range movedAssets {
		if a.hadCurrent {
			if err:=os.Rename(a.saved,filepath.Join(manifest,strconv.Itoa(i)));err!=nil{return err}
		}
	}
	return nil
}
