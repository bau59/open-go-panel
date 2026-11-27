package server

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/bau59/open-go-panel/internal/terminal"
	"github.com/bau59/open-go-panel/internal/state"
)

const snapshotRoot = "/var/lib/open-go-panel/backups/server"
const snapshotStamp = "2006-01-02_15-04-05.000000000Z"

var snapshotIDPattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}_[0-9]{2}-[0-9]{2}-[0-9]{2}\.[0-9]{9}Z$`)
var snapshotSegment = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)

type snapshotFile struct {
	Path string `json:"path"`
	Size int64 `json:"size"`
	SHA256 string `json:"sha256"`
}

type snapshotIndex struct {
	Version int `json:"version"`
	ID string `json:"id"`
	AppID int64 `json:"app_id,omitempty"`
	Owner string `json:"owner,omitempty"`
	Application string `json:"application,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Files []snapshotFile `json:"files"`
}

type snapshotUnit struct {
	Name string `json:"name"`
	Enabled bool `json:"enabled"`
	Active bool `json:"active"`
}

type snapshotAccount struct {
	Username string `json:"username"`
	Home string `json:"home"`
	UID string `json:"uid"`
	GID string `json:"gid"`
	PrimaryGroup string `json:"primary_group"`
	Shell string `json:"shell"`
	Shadow string `json:"shadow"`
	Groups []string `json:"groups"`
}

func validSnapshotPath(name string) bool {
	p:=strings.Split(name,"/")
	for _,part:=range p {
		if !snapshotSegment.MatchString(part)||part=="."||part==".." {return false}
	}
	switch {
	case name=="config.tar.gz":return true
	case len(p)==2&&p[0]=="files"&&strings.HasSuffix(p[1],".tar.gz"):return true
	case len(p)==3&&p[0]=="databases"&&(p[1]=="mysql"||p[1]=="postgres")&&strings.HasSuffix(p[2],".sql.gz"):return true
	default:return false
	}
}

func validateSnapshotIndex(idx snapshotIndex,id string) error {
	if idx.Version!=1||idx.ID!=id||!snapshotIDPattern.MatchString(id)||len(idx.Files)==0{return errors.New("invalid server snapshot index")}
	if idx.AppID<0 || (idx.AppID>0 && (!backupUsername.MatchString(idx.Owner)||!snapshotSegment.MatchString(idx.Application))) {return errors.New("invalid snapshot application")}
	seen:=map[string]bool{}
	for _,f:=range idx.Files {
		hash,err:=hex.DecodeString(f.SHA256)
		if !validSnapshotPath(f.Path)||f.Size<=0||err!=nil||len(hash)!=sha256.Size||seen[f.Path]{return errors.New("invalid server snapshot file")}
		seen[f.Path]=true
	}
	if !seen["config.tar.gz"]{return errors.New("snapshot configuration missing")}
	return nil
}

func snapshotFileInfo(root,name string)(snapshotFile,error){
	item:=snapshotFile{Path:name}
	f,err:=os.Open(filepath.Join(root,filepath.FromSlash(name)))
	if err!=nil{return item,err}
	defer f.Close()
	h:=sha256.New()
	n,err:=io.Copy(h,f)
	if err!=nil{return item,err}
	item.Size=n
	item.SHA256=hex.EncodeToString(h.Sum(nil))
	return item,nil
}

func createServerSnapshot(ctx context.Context,cfg Config)(snapshotIndex,error){
	idx:=snapshotIndex{Version:1,CreatedAt:time.Now().UTC()}
	idx.ID=idx.CreatedAt.Format(snapshotStamp)
	if err:=os.MkdirAll(snapshotRoot,0700);err!=nil{return idx,err}
	dir,err:=os.MkdirTemp(snapshotRoot,".snapshot-*")
	if err!=nil{return idx,err}
	defer os.RemoveAll(dir)
	if err:=buildSnapshotConfig(ctx,cfg,dir);err!=nil{return idx,err}
	names:=[]string{"config.tar.gz"}
	users,err:=cfg.Users.List(ctx)
	if err!=nil{return idx,err}
	for _,u:=range users {
		if !backupUsername.MatchString(u.Username)||u.Home!="/home/"+u.Username{return idx,fmt.Errorf("unsupported home for %s",u.Username)}
		name:="files/"+u.Username+".tar.gz"
		if err:=os.MkdirAll(filepath.Join(dir,"files"),0700);err!=nil{return idx,err}
		if err:=archiveSnapshotFiles(ctx,u.Username,u.Home,filepath.Join(dir,name));err!=nil{return idx,fmt.Errorf("archive home %s: %w",u.Username,err)}
		names=append(names,name)
	}
	if cfg.Databases!=nil{
		dbs,err:=cfg.Databases.List()
		if err!=nil{return idx,err}
		for _,db:=range dbs{
			name:="databases/"+db.Engine+"/"+db.Name+".sql.gz"
			if !validSnapshotPath(name){return idx,errors.New("invalid database backup name")}
			b,err:=cfg.Databases.BackupTo(ctx,db.ID,filepath.Join(dir,"databases",db.Engine))
			if err!=nil{return idx,fmt.Errorf("backup database %s: %w",db.Name,err)}
			if err:=os.Rename(b.Path,filepath.Join(dir,name));err!=nil{return idx,err}
			names=append(names,name)
		}
	}
	return publishSnapshot(ctx,idx,dir,snapshotRoot,names)
}

func publishSnapshot(ctx context.Context,idx snapshotIndex,dir,root string,names []string)(snapshotIndex,error){
	for _,name:=range names{
		if err:=ctx.Err();err!=nil{return idx,err}
		item,err:=snapshotFileInfo(dir,name)
		if err!=nil{return idx,err}
		idx.Files=append(idx.Files,item)
	}
	if err:=validateSnapshotIndex(idx,idx.ID);err!=nil{return idx,err}
	if err:=writeSnapshotIndex(dir,idx);err!=nil{return idx,err}
	return idx,os.Rename(dir,filepath.Join(root,idx.ID))
}

func writeSnapshotIndex(dir string,idx snapshotIndex)error{
	data,err:=json.MarshalIndent(idx,"","  ")
	if err!=nil{return err}
	return os.WriteFile(filepath.Join(dir,"manifest.json"),data,0600)
}

func readSnapshotIndex(dir,id string)(snapshotIndex,error){
	var idx snapshotIndex
	f,err:=os.Open(filepath.Join(dir,"manifest.json"))
	if err!=nil{return idx,err}
	defer f.Close()
	if err:=json.NewDecoder(io.LimitReader(f,4<<20)).Decode(&idx);err!=nil{return idx,err}
	return idx,validateSnapshotIndex(idx,id)
}

func localSnapshots()([]snapshotIndex,error){return snapshotsAt(snapshotRoot)}

func snapshotsAt(root string)([]snapshotIndex,error){
	entries,err:=os.ReadDir(root)
	if errors.Is(err,os.ErrNotExist){return nil,nil}
	if err!=nil{return nil,err}
	var result []snapshotIndex
	for _,entry:=range entries{
		if !entry.IsDir()||!snapshotIDPattern.MatchString(entry.Name()){continue}
		idx,err:=readSnapshotIndex(filepath.Join(root,entry.Name()),entry.Name())
		if err==nil{result=append(result,idx)}
	}
	sort.Slice(result,func(i,j int)bool{return result[i].ID>result[j].ID})
	return result,nil
}

func buildSnapshotConfig(ctx context.Context,cfg Config,dir string)(err error){
	if cfg.State==nil{return errors.New("panel state unavailable")}
	dbfile:=filepath.Join(dir,"panel.db")
	// SQLite VACUUM INTO produces a transactionally consistent copy even
	// while the active database is in WAL mode. Never copy panel.db directly.
	if _,err:=cfg.State.DB().ExecContext(ctx,"VACUUM INTO ?",dbfile);err!=nil{return err}
	defer os.Remove(dbfile)
	if err:=os.Chmod(dbfile,0600);err!=nil{return err}
	// Authorization is bound to the original live host. Never resurrect
	// revoked ChatGPT access by restoring an old server snapshot.
	copyState,err:=state.Open(dbfile)
	if err!=nil{return err}
	_,clearErr:=copyState.DB().ExecContext(ctx,"DELETE FROM mcp_connections")
	closeErr:=copyState.Close()
	if clearErr!=nil{return clearErr}
	if closeErr!=nil{return closeErr}
	users,err:=cfg.Users.List(ctx)
	if err!=nil{return err}
	var accounts []snapshotAccount
	for _,u:=range users{
		if !backupUsername.MatchString(u.Username)||u.Home!="/home/"+u.Username{return errors.New("invalid managed account")}
		account,err:=user.Lookup(u.Username)
		if err!=nil{return err}
		primary,err:=user.LookupGroupId(account.Gid)
		if err!=nil{return err}
		groups,err:=account.GroupIds()
		if err!=nil{return err}
		shadow,err:=exec.CommandContext(ctx,"getent","shadow",u.Username).Output()
		if err!=nil{return fmt.Errorf("read managed account credentials for %s: %w",u.Username,err)}
		groupsNames:=make([]string,0,len(groups))
		for _,id:=range groups{
			group,err:=user.LookupGroupId(id)
			if err!=nil{return err}
			groupsNames=append(groupsNames,group.Name)
		}
		accounts=append(accounts,snapshotAccount{Username:u.Username,Home:u.Home,UID:account.Uid,GID:account.Gid,PrimaryGroup:primary.Name,Shell:u.Shell,Shadow:strings.TrimSpace(string(shadow)),Groups:groupsNames})
	}
	data,err:=json.Marshal(accounts)
	if err!=nil{return err}
	file,err:=os.OpenFile(filepath.Join(dir,"config.tar.gz"),os.O_CREATE|os.O_EXCL|os.O_WRONLY,0600)
	if err!=nil{return err}
	defer func(){if e:=file.Close();err==nil{err=e}}()
	gz:=gzip.NewWriter(file)
	defer func(){if e:=gz.Close();err==nil{err=e}}()
	tw:=tar.NewWriter(gz)
	defer func(){if e:=tw.Close();err==nil{err=e}}()
	if err:=writeTarBytes(tw,"accounts.json",data);err!=nil{return err}
	jobs,err:=terminal.NewBackgroundManager(cfg.State).List(ctx)
	if err!=nil{return err}
	var running []string
	for _,job:=range jobs{if job.Status=="running"||job.Status=="starting"{running=append(running,job.ID)}}
	jobData,err:=json.Marshal(running);if err!=nil{return err}
	if err:=writeTarBytes(tw,"running-commands.json",jobData);err!=nil{return err}
	if err:=writeTarTree(ctx,tw,dbfile,"panel.db");err!=nil{return err}
	paths:=[]string{"/etc/open-go-panel","/etc/caddy","/var/lib/open-go-panel/runners","/var/lib/open-go-panel/env","/usr/local/lib/open-go-panel/ioncube","/etc/mysql/mysql.conf.d/99-open-go-panel.cnf"}
	for _,pattern:=range []string{"/etc/systemd/system/open-go-panel-*.service","/etc/systemd/system/open-go-panel-*.timer","/etc/systemd/system/open-go-panel-app-*.service.d/50-ogp-dependencies.conf","/etc/systemd/journald@open-go-panel-*.conf","/etc/php/*/fpm/pool.d/ogp-app-*.conf","/etc/php/*/mods-available/*ioncube*.ini","/etc/php/*/fpm/conf.d/*ioncube*.ini"}{
		matches,err:=filepath.Glob(pattern)
		if err!=nil{return err}
		for _,name:=range matches{if snapshotSystemPath(strings.TrimPrefix(name,"/")){paths=append(paths,name)}}
	}
	for _,name:=range paths{
		if _,err:=os.Lstat(name);errors.Is(err,os.ErrNotExist){continue}else if err!=nil{return err}
		if err:=writeTarTree(ctx,tw,name,"system/"+strings.TrimPrefix(name,"/"));err!=nil{return err}
	}
	var units []snapshotUnit
	for _,name:=range paths{
		if !strings.HasPrefix(name,"/etc/systemd/system/"){continue}
		unit:=filepath.Base(name)
		if !managedUnitRE.MatchString(unit){continue}
		units=append(units,snapshotUnit{Name:unit,Enabled:exec.CommandContext(ctx,"systemctl","is-enabled","--quiet",unit).Run()==nil,Active:exec.CommandContext(ctx,"systemctl","is-active","--quiet",unit).Run()==nil})
	}
	unitData,err:=json.Marshal(units);if err!=nil{return err}
	if err:=writeTarBytes(tw,"units.json",unitData);err!=nil{return err}
	packages,err:=exec.CommandContext(ctx,"apt-mark","showmanual").Output()
	if err!=nil{return err}
	if err:=writeTarBytes(tw,"packages.txt",packages);err!=nil{return err}
	// Docker configuration is useful for reconstruction; external bind mounts,
	// named volume contents and unmanaged system files are not included.
	if _,err:=exec.LookPath("docker");err==nil{
		ids,err:=exec.CommandContext(ctx,"docker","ps","-aq").Output()
		if err!=nil{return err}
		docker:=[]byte("[]")
		if len(strings.Fields(string(ids)))>0{
			docker,err=exec.CommandContext(ctx,"docker",append([]string{"inspect"},strings.Fields(string(ids))...)...).Output()
			if err!=nil{return err}
		}
		if err:=writeTarBytes(tw,"docker.json",docker);err!=nil{return err}
	}
	return nil
}
