package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type snapshotPolicy struct {
	LocalDays int
	LocalHour int
	LocalKeep int
	CloudDays int
	CloudHour int
}

type snapshotTask struct {
	Kind string
	State string
	Message string
}

type snapshotManager struct {
	cfg Config
	ctx context.Context
	mu sync.Mutex
	task snapshotTask
}

func (m *snapshotManager) setting(key string)string{
	value,_,_:=m.cfg.State.Setting("snapshot."+key)
	return value
}

func (m *snapshotManager) policy()snapshotPolicy{
	number:=func(key string,def,min,max int)int{
		n,err:=strconv.Atoi(m.setting(key))
		if err!=nil||n<min||n>max{return def}
		return n
	}
	return snapshotPolicy{
		LocalDays:number("local_days",0,0,365),
		LocalHour:number("local_hour",3,0,23),
		LocalKeep:number("local_keep",7,1,100),
		CloudDays:number("cloud_days",0,0,365),
		CloudHour:number("cloud_hour",4,0,23),
	}
}

func (m *snapshotManager) savePolicy(p snapshotPolicy)error{
	if p.LocalDays<0||p.LocalDays>365||p.CloudDays<0||p.CloudDays>365||p.LocalHour<0||p.LocalHour>23||p.CloudHour<0||p.CloudHour>23||p.LocalKeep<1||p.LocalKeep>100{
		return errors.New("invalid snapshot schedule")
	}
	if p.CloudDays>0{
		if m.cfg.Databases==nil{return errors.New("database manager unavailable")}
		if _,_,err:=m.cfg.Databases.YandexStorage();err!=nil{return err}
	}
	tx,err:=m.cfg.State.DB().Begin()
	if err!=nil{return err}
	defer tx.Rollback()
	for key,value:=range map[string]int{"local_days":p.LocalDays,"local_hour":p.LocalHour,"local_keep":p.LocalKeep,"cloud_days":p.CloudDays,"cloud_hour":p.CloudHour}{
		if _,err:=tx.Exec("INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value","snapshot."+key,strconv.Itoa(value));err!=nil{return err}
	}
	return tx.Commit()
}

func (m *snapshotManager) status()snapshotTask{
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.task
}

func (m *snapshotManager) start(kind string,job func(context.Context)error)error{
	m.mu.Lock()
	if m.task.State=="Running"{m.mu.Unlock();return errors.New("a server snapshot operation is already running")}
	if m.ctx.Err()!=nil{m.mu.Unlock();return m.ctx.Err()}
	m.task=snapshotTask{Kind:kind,State:"Running"}
	m.mu.Unlock()
	go func(){
		err:=job(m.ctx)
		m.mu.Lock()
		defer m.mu.Unlock()
		m.task=snapshotTask{Kind:kind,State:"Completed"}
		if err!=nil{m.task.State="Failed";m.task.Message=err.Error();m.cfg.Logger.Error("server snapshot operation failed","kind",kind,"err",err)}
	}()
	return nil
}

func snapshotDue(now time.Time,last string,days,hour int)bool{
	if days<=0{return false}
	now=now.UTC()
	if now.Hour()<hour{return false}
	prev,err:=time.Parse("2006-01-02",last)
	if err!=nil{return true}
	today:=time.Date(now.Year(),now.Month(),now.Day(),0,0,0,0,time.UTC)
	return !today.Before(prev.AddDate(0,0,days))
}

func (m *snapshotManager) runSchedule(){
	ticker:=time.NewTicker(5*time.Minute)
	defer ticker.Stop()
	for {
		now:=time.Now().UTC()
		p:=m.policy()
		if snapshotDue(now,m.setting("local_last"),p.LocalDays,p.LocalHour){
			_ = m.start("Scheduled local snapshot",func(ctx context.Context)error{return m.local(ctx,true)})
		}else if snapshotDue(now,m.setting("cloud_last"),p.CloudDays,p.CloudHour){
			_ = m.start("Scheduled cloud snapshot",func(ctx context.Context)error{return m.cloud(ctx,true)})
		}
		select{case <-m.ctx.Done():return;case <-ticker.C:}
	}
}

func (m *snapshotManager) local(ctx context.Context,scheduled bool)error{
	if _,err:=createServerSnapshot(ctx,m.cfg);err!=nil{return err}
	list,err:=localSnapshots()
	if err!=nil{return err}
	keep:=m.policy().LocalKeep
	if keep<len(list){
		for _,idx:=range list[keep:]{
			if err:=os.RemoveAll(filepath.Join(snapshotRoot,idx.ID));err!=nil{return err}
		}
	}
	if scheduled{return m.cfg.State.SetSetting("snapshot.local_last",time.Now().UTC().Format("2006-01-02"))}
	return nil
}

func (m *snapshotManager) cloud(ctx context.Context,scheduled bool)error{
	if m.cfg.Databases==nil{return errors.New("database manager unavailable")}
	_,_,err:=m.cfg.Databases.YandexStorage()
	if err!=nil{return err}
	// Independent cloud scheduling uses its own fresh snapshot rather than
	// changing the local schedule or relying on an old SQL backup.
	idx,err:=createServerSnapshot(ctx,m.cfg)
	if err!=nil{return err}
	if err:=m.upload(ctx,idx);err!=nil{return err}
	// Cloud-only creation must not modify the separate local history.
	if err:=os.RemoveAll(filepath.Join(snapshotRoot,idx.ID));err!=nil{return err}
	if scheduled{return m.cfg.State.SetSetting("snapshot.cloud_last",time.Now().UTC().Format("2006-01-02"))}
	return nil
}

func (m *snapshotManager) upload(ctx context.Context,idx snapshotIndex)error{
	if err:=validateSnapshotIndex(idx,idx.ID);err!=nil{return err}
	if m.cfg.Databases==nil{return errors.New("database manager unavailable")}
	client,root,err:=m.cfg.Databases.YandexStorage()
	if err!=nil{return err}
	root+="/server/"+idx.ID
	for _,item:=range idx.Files{
		if err:=ctx.Err();err!=nil{return err}
		local:=filepath.Join(snapshotRoot,idx.ID,filepath.FromSlash(item.Path))
		actual,err:=snapshotFileInfo(filepath.Join(snapshotRoot,idx.ID),item.Path)
		if err!=nil{return err}
		if actual!=item{return fmt.Errorf("local snapshot file changed: %s",item.Path)}
		// Each file has its own multipart manifest. The root index is the
		// completion marker for the whole server snapshot.
		if _,err:=client.UploadArchive(ctx,root+"/"+item.Path,local);err!=nil{return fmt.Errorf("upload %s: %w",item.Path,err)}
	}
	return client.PutJSON(ctx,root+"/manifest.json",idx)
}

func (m *snapshotManager) cloudList(ctx context.Context)([]snapshotIndex,error){
	if m.cfg.Databases==nil{return nil,errors.New("database manager unavailable")}
	client,root,err:=m.cfg.Databases.YandexStorage()
	if err!=nil{return nil,err}
	root+="/server"
	if err:=client.Check(ctx,root);err!=nil{return nil,err}
	names,err:=client.ListDirectories(ctx,root)
	if err!=nil{return nil,err}
	var result []snapshotIndex
	for _,id:=range names{
		if !snapshotIDPattern.MatchString(id){continue}
		var idx snapshotIndex
		if err:=client.GetJSON(ctx,root+"/"+id+"/manifest.json",&idx);err!=nil{continue}
		if validateSnapshotIndex(idx,id)==nil{result=append(result,idx)}
	}
	return result,nil
}

func (m *snapshotManager) download(ctx context.Context,id string)error{
	if !snapshotIDPattern.MatchString(id){return errors.New("invalid snapshot ID")}
	if m.cfg.Databases==nil{return errors.New("database manager unavailable")}
	client,root,err:=m.cfg.Databases.YandexStorage()
	if err!=nil{return err}
	root+="/server/"+id
	var idx snapshotIndex
	if err:=client.GetJSON(ctx,root+"/manifest.json",&idx);err!=nil{return err}
	if err:=validateSnapshotIndex(idx,id);err!=nil{return err}
	if err:=os.MkdirAll(snapshotRoot,0700);err!=nil{return err}
	final:=filepath.Join(snapshotRoot,id)
	if _,err:=os.Lstat(final);err==nil{return errors.New("snapshot already exists locally")}else if !errors.Is(err,os.ErrNotExist){return err}
	dir,err:=os.MkdirTemp(snapshotRoot,".download-*")
	if err!=nil{return err}
	defer os.RemoveAll(dir)
	for _,item:=range idx.Files{
		dst:=filepath.Join(dir,filepath.FromSlash(item.Path))
		if err:=os.MkdirAll(filepath.Dir(dst),0700);err!=nil{return err}
		if err:=client.DownloadArchive(ctx,root+"/"+item.Path,dst);err!=nil{return fmt.Errorf("download %s: %w",item.Path,err)}
		actual,err:=snapshotFileInfo(dir,item.Path)
		if err!=nil{return err}
		if actual!=item{return fmt.Errorf("snapshot checksum mismatch: %s",item.Path)}
	}
	if err:=writeSnapshotIndex(dir,idx);err!=nil{return err}
	return os.Rename(dir,final)
}

func validSnapshotID(id string)bool{return snapshotIDPattern.MatchString(id)&&!strings.Contains(id,"/")}
