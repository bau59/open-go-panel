package server

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

func appSnapshotRoot(id int64) string {
	return filepath.Join(snapshotRoot,"apps",strconv.FormatInt(id,10))
}

func createAppSnapshot(ctx context.Context,cfg Config,id int64)(snapshotIndex,error){
	idx:=snapshotIndex{Version:1,CreatedAt:time.Now().UTC(),AppID:id}
	idx.ID=idx.CreatedAt.Format(snapshotStamp)
	a,err:=cfg.Apps.Get(id)
	if err!=nil{return idx,err}
	idx.Owner=a.User;idx.Application=a.Name
	if !backupUsername.MatchString(a.User)||!snapshotSegment.MatchString(a.Name)||filepath.Clean(a.Root)!=filepath.Join("/home",a.User,"apps",a.Name){
		return idx,errors.New("unsupported application root")
	}
	root:=appSnapshotRoot(id)
	if err:=os.MkdirAll(root,0700);err!=nil{return idx,err}
	dir,err:=os.MkdirTemp(root,".snapshot-*")
	if err!=nil{return idx,err}
	defer os.RemoveAll(dir)
	deploy,err:=cfg.Apps.DeployConfig(id);if err!=nil{return idx,err}
	cron,err:=cfg.Apps.CronJobs(ctx,id);if err!=nil{return idx,err}
	config:=map[string]any{"app":a,"deploy":deploy,"cron":cron}
	if cfg.Caddy!=nil{
		sites,err:=cfg.Caddy.Sites();if err!=nil{return idx,err}
		for _,site:=range sites{if site.AppID==id{config["caddy"]=site;break}}
	}
	var names []string
	if cfg.Databases!=nil{
		bindings,err:=cfg.Databases.AttachmentsForApp(id);if err!=nil{return idx,err}
		config["database_bindings"]=bindings
		seen:=map[int64]bool{}
		var dbs []any
		for _,binding:=range bindings{
			if seen[binding.DatabaseID]{continue};seen[binding.DatabaseID]=true
			db,err:=cfg.Databases.Get(binding.DatabaseID);if err!=nil{return idx,err}
			dbs=append(dbs,db)
			name:="databases/"+db.Engine+"/"+db.Name+".sql.gz"
			if !validSnapshotPath(name){return idx,errors.New("invalid database path")}
			dump,err:=cfg.Databases.BackupTo(ctx,db.ID,filepath.Join(dir,"databases",db.Engine))
			if err!=nil{return idx,err}
			if err:=os.Rename(dump.Path,filepath.Join(dir,name));err!=nil{return idx,err}
			names=append(names,name)
		}
		config["databases"]=dbs
	}
	data,err:=json.Marshal(config);if err!=nil{return idx,err}
	if err:=writeAppSnapshotConfig(filepath.Join(dir,"config.tar.gz"),data);err!=nil{return idx,err}
	names=append(names,"config.tar.gz")
	if err:=os.MkdirAll(filepath.Join(dir,"files"),0700);err!=nil{return idx,err}
	name:="files/"+a.User+".tar.gz"
	f,err:=os.OpenFile(filepath.Join(dir,name),os.O_CREATE|os.O_EXCL|os.O_WRONLY,0600)
	if err!=nil{return idx,err}
	cmd:=exec.CommandContext(ctx,"runuser","-u",a.User,"--","/usr/bin/tar","-czf","-","-C",a.Root,".")
	cmd.Stdout=f;cmd.Stderr=io.Discard
	runErr:=cmd.Run();closeErr:=f.Close()
	if runErr!=nil{return idx,fmt.Errorf("archive application %s: %w",a.Name,runErr)}
	if closeErr!=nil{return idx,closeErr}
	names=append(names,name)
	return publishSnapshot(ctx,idx,dir,root,names)
}

func writeAppSnapshotConfig(path string,data []byte)(err error){
	f,err:=os.OpenFile(path,os.O_CREATE|os.O_EXCL|os.O_WRONLY,0600)
	if err!=nil{return err}
	defer func(){if e:=f.Close();err==nil{err=e}}()
	gz:=gzip.NewWriter(f)
	defer func(){if e:=gz.Close();err==nil{err=e}}()
	tw:=tar.NewWriter(gz)
	defer func(){if e:=tw.Close();err==nil{err=e}}()
	return writeTarBytes(tw,"app.json",data)
}

func (m *snapshotManager) appPolicyKey(id int64)string{
	return "app."+strconv.FormatInt(id,10)+"."
}

func (m *snapshotManager) appLocal(ctx context.Context,id int64,scheduled bool)error{
	if _,err:=createAppSnapshot(ctx,m.cfg,id);err!=nil{return err}
	key:=m.appPolicyKey(id)
	if err:=pruneLocalSnapshots(appSnapshotRoot(id),m.policyFor(key).LocalKeep,m.setting(key+"cloud_pending"));err!=nil{return err}
	if scheduled{return m.cfg.State.SetSetting("snapshot."+key+"local_last",time.Now().UTC().Format("2006-01-02"))}
	return nil
}

func (m *snapshotManager) appCloud(ctx context.Context,id int64,scheduled bool)error{
	key:=m.appPolicyKey(id)
	if m.cfg.Databases==nil{return errors.New("database manager unavailable")}
	_,remote,err:=m.cfg.Databases.YandexStorage();if err!=nil{return err}
	a,err:=m.cfg.Apps.Get(id);if err!=nil{return err}
	if !backupUsername.MatchString(a.User)||!snapshotSegment.MatchString(a.Name){return errors.New("invalid application path")}
	remote+="/projects/"+a.User+"/"+a.Name
	root:=appSnapshotRoot(id)
	idx,err:=m.pendingSnapshot(root,key,func()(snapshotIndex,error){return createAppSnapshot(ctx,m.cfg,id)})
	if err!=nil{return err}
	if idx.AppID!=id||idx.Owner!=a.User||idx.Application!=a.Name{return errors.New("pending application snapshot no longer matches")}
	if err:=m.uploadAt(ctx,idx,root,remote);err!=nil{return err}
	if err:=m.pruneCloud(ctx,remote,m.policyFor(key).CloudKeep);err!=nil{return err}
	if err:=m.finishPending(root,key,idx.ID);err!=nil{return err}
	if scheduled{return m.cfg.State.SetSetting("snapshot."+key+"cloud_last",time.Now().UTC().Format("2006-01-02"))}
	return nil
}
