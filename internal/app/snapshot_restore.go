package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/bau59/open-go-panel/internal/phpfpm"
)

// RestoreSnapshotFiles prepares the verified archive under the application
// account. The original tree remains available if activation fails.
func (m *Manager) RestoreSnapshotFiles(ctx context.Context,id int64,archive string)error{
	m.deployMu.Lock()
	defer m.deployMu.Unlock()
	a,err:=m.Get(id);if err!=nil{return err}
	if a.Type=="php"&&a.Service.PHPVersion==phpfpm.LegacyVersion{return errors.New("restore PHP 7.4 files through a fresh server snapshot; its container bind mount requires recreation")}
	info,err:=os.Lstat(a.Root);if err!=nil{return err}
	if !info.IsDir(){return errors.New("application root must be a real directory")}
	stat,ok:=info.Sys().(*syscall.Stat_t);if !ok{return errors.New("cannot determine application directory ownership")}
	stage,err:=os.MkdirTemp(filepath.Dir(a.Root),".ogp-restore-");if err!=nil{return err}
	preserve:=false
	defer func(){if !preserve{_ = os.RemoveAll(stage)}}()
	if err:=os.Chown(stage,int(stat.Uid),int(stat.Gid));err!=nil{return err}
	if err:=os.Chmod(stage,info.Mode().Perm());err!=nil{return err}
	f,err:=os.Open(archive);if err!=nil{return err};defer f.Close()
	cmd:=exec.CommandContext(ctx,"runuser","-u",a.User,"--","/usr/bin/tar","--acls","--xattrs","--no-same-owner","--same-permissions","-xzf","-","-C",stage)
	cmd.Stdin=f
	if err:=cmd.Run();err!=nil{return fmt.Errorf("extract application snapshot: %w",err)}
	if err:=ctx.Err();err!=nil{return err}
	running:=a.Type!="static"&&a.Type!="php"&&m.Status(ctx,id)=="active"
	if running{if err:=m.Stop(ctx,id);err!=nil{return err}}
	// Retain the stage after an attempted switch. On success it holds the
	// old tree; on a failed rollback it may be the only remaining original.
	preserve=true
	restart:=func()error{
		if !running{return nil}
		recovery,cancel:=context.WithTimeout(context.Background(),time.Minute);defer cancel()
		return m.Start(recovery,id)
	}
	if err:=activateStagedRelease(a.Root,stage,func()error{
		if !running{return nil}
		if err:=m.Start(ctx,id);err!=nil{return err}
		return m.waitForRelease(ctx,a)
	},restart);err!=nil{
		if e:=restart();e!=nil{err=errors.Join(err,e)}
		return fmt.Errorf("file recovery failed; retained tree %s: %w",stage,err)
	}
	// The caller already created a verified safety archive. The swapped old
	// tree can be removed only after successful activation.
	preserve=false
	return nil
}
