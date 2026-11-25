package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bau59/open-go-panel/internal/yandexdisk"
)

type snapshotPolicy struct {
	LocalDays int
	LocalHour int
	LocalKeep int
	CloudDays int
	CloudHour int
	CloudKeep int
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

func (m *snapshotManager) policy()snapshotPolicy{return m.policyFor("")}

func (m *snapshotManager) policyFor(prefix string)snapshotPolicy{
	number:=func(key string,def,min,max int)int{
		n,err:=strconv.Atoi(m.setting(prefix+key))
		if err!=nil||n<min||n>max{return def}
		return n
	}
	return snapshotPolicy{
		LocalDays:number("local_days",0,0,365),
		LocalHour:number("local_hour",3,0,23),
		LocalKeep:number("local_keep",7,1,100),
		CloudDays:number("cloud_days",0,0,365),
		CloudHour:number("cloud_hour",4,0,23),
		CloudKeep:number("cloud_keep",0,0,100),
	}
}

func (m *snapshotManager) savePolicy(p snapshotPolicy)error{return m.savePolicyFor("",p)}

func (m *snapshotManager) savePolicyFor(prefix string,p snapshotPolicy)error{
	if p.LocalDays<0||p.LocalDays>365||p.CloudDays<0||p.CloudDays>365||p.LocalHour<0||p.LocalHour>23||p.CloudHour<0||p.CloudHour>23||p.LocalKeep<1||p.LocalKeep>100||p.CloudKeep<0||p.CloudKeep>100{
		return errors.New("invalid snapshot schedule")
	}
	if p.CloudDays>0{
		if m.cfg.Databases==nil{return errors.New("database manager unavailable")}
		if _,_,err:=m.cfg.Databases.YandexStorage();err!=nil{return err}
	}
	tx,err:=m.cfg.State.DB().Begin()
	if err!=nil{return err}
	defer tx.Rollback()
	for key,value:=range map[string]int{"local_days":p.LocalDays,"local_hour":p.LocalHour,"local_keep":p.LocalKeep,"cloud_days":p.CloudDays,"cloud_hour":p.CloudHour,"cloud_keep":p.CloudKeep}{
		if _,err:=tx.Exec("INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value","snapshot."+prefix+key,strconv.Itoa(value));err!=nil{return err}
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
	// Rotate among due jobs so one failed local job cannot starve cloud
	// transfers or other applications.
	next:=0
	for {
		if m.status().State!="Running"{
			type job struct{kind string;run func(context.Context)error}
			var due []job
			now:=time.Now().UTC()
			p:=m.policy()
			if snapshotDue(now,m.setting("local_last"),p.LocalDays,p.LocalHour){
				due=append(due,job{"Scheduled local server snapshot",func(ctx context.Context)error{return m.local(ctx,true)}})
			}
			if snapshotDue(now,m.setting("cloud_last"),p.CloudDays,p.CloudHour){
				due=append(due,job{"Scheduled cloud server snapshot",func(ctx context.Context)error{return m.cloud(ctx,true)}})
			}
			if m.cfg.Apps!=nil{
				apps,err:=m.cfg.Apps.List()
				if err!=nil{m.cfg.Logger.Error("list application backup schedules","err",err)}
				for _,a:=range apps{
					id:=a.ID
					key:=m.appPolicyKey(id)
					p:=m.policyFor(key)
					if snapshotDue(now,m.setting(key+"local_last"),p.LocalDays,p.LocalHour){due=append(due,job{"Local app snapshot: "+a.Name,func(ctx context.Context)error{return m.appLocal(ctx,id,true)}})}
					if snapshotDue(now,m.setting(key+"cloud_last"),p.CloudDays,p.CloudHour){due=append(due,job{"Cloud app snapshot: "+a.Name,func(ctx context.Context)error{return m.appCloud(ctx,id,true)}})}
				}
			}
			if len(due)>0{
				j:=due[next%len(due)]
				next++
				_=m.start(j.kind,j.run)
			}
		}
		select{case <-m.ctx.Done():return;case <-ticker.C:}
	}
}

func pruneLocalSnapshots(root string,keep int,pending string)error{
	list,err:=snapshotsAt(root)
	if err!=nil{return err}
	count:=0
	for _,idx:=range list{
		if idx.ID==pending{continue}
		count++
		if count>keep{
			if err:=os.RemoveAll(filepath.Join(root,idx.ID));err!=nil{return err}
		}
	}
	return nil
}

func (m *snapshotManager) local(ctx context.Context,scheduled bool)error{
	if _,err:=createServerSnapshot(ctx,m.cfg);err!=nil{return err}
	if err:=pruneLocalSnapshots(snapshotRoot,m.policy().LocalKeep,m.setting("cloud_pending"));err!=nil{return err}
	if scheduled{return m.cfg.State.SetSetting("snapshot.local_last",time.Now().UTC().Format("2006-01-02"))}
	return nil
}

func (m *snapshotManager) pendingSnapshot(root,key string,create func()(snapshotIndex,error))(snapshotIndex,error){
	id:=m.setting(key+"cloud_pending")
	if validSnapshotID(id){
		idx,err:=readSnapshotIndex(filepath.Join(root,id),id)
		if err==nil{return idx,nil}
		if !errors.Is(err,os.ErrNotExist){return idx,err}
	}
	idx,err:=create()
	if err!=nil{return idx,err}
	if err:=m.cfg.State.SetSetting("snapshot."+key+"cloud_pending",idx.ID);err!=nil{return idx,err}
	return idx,nil
}

func (m *snapshotManager) finishPending(root,key,id string)error{
	if err:=m.cfg.State.SetSetting("snapshot."+key+"cloud_pending","");err!=nil{return err}
	return os.RemoveAll(filepath.Join(root,id))
}

func (m *snapshotManager) cloud(ctx context.Context,scheduled bool)error{
	if m.cfg.Databases==nil{return errors.New("database manager unavailable")}
	_,remote,err:=m.cfg.Databases.YandexStorage()
	if err!=nil{return err}
	remote+="/server"
	idx,err:=m.pendingSnapshot(snapshotRoot,"",func()(snapshotIndex,error){return createServerSnapshot(ctx,m.cfg)})
	if err!=nil{return err}
	if idx.AppID!=0{return errors.New("expected full server snapshot")}
	if err:=m.uploadAt(ctx,idx,snapshotRoot,remote);err!=nil{return err}
	if err:=m.pruneCloud(ctx,remote,m.policy().CloudKeep);err!=nil{return err}
	if err:=m.finishPending(snapshotRoot,"",idx.ID);err!=nil{return err}
	if scheduled{return m.cfg.State.SetSetting("snapshot.cloud_last",time.Now().UTC().Format("2006-01-02"))}
	return nil
}

func (m *snapshotManager) upload(ctx context.Context,idx snapshotIndex)error{
	if err:=validateSnapshotIndex(idx,idx.ID);err!=nil{return err}
	if m.cfg.Databases==nil{return errors.New("database manager unavailable")}
	_,root,err:=m.cfg.Databases.YandexStorage()
	if err!=nil{return err}
	return m.uploadAt(ctx,idx,snapshotRoot,root+"/server")
}

func (m *snapshotManager) uploadAt(ctx context.Context,idx snapshotIndex,localRoot,remoteRoot string)error{
	if err:=validateSnapshotIndex(idx,idx.ID);err!=nil{return err}
	client,_,err:=m.cfg.Databases.YandexStorage();if err!=nil{return err}
	root:=remoteRoot+"/"+idx.ID
	for _,item:=range idx.Files{
		if err:=ctx.Err();err!=nil{return err}
		local:=filepath.Join(localRoot,idx.ID,filepath.FromSlash(item.Path))
		actual,err:=snapshotFileInfo(filepath.Join(localRoot,idx.ID),item.Path)
		if err!=nil{return err}
		if actual!=item{return fmt.Errorf("local snapshot file changed: %s",item.Path)}
		// Each file has its own multipart manifest. The root index is the
		// completion marker for the whole server snapshot.
		if _,err:=client.UploadArchive(ctx,root+"/"+item.Path,local);err!=nil{return fmt.Errorf("upload %s: %w",item.Path,err)}
	}
	return client.PutJSON(ctx,root+"/manifest.json",idx)
}

type cloudSnapshotInventory struct {
	Snapshots []snapshotIndex
	Incomplete []string
}

func (m *snapshotManager) cloudList(ctx context.Context)(cloudSnapshotInventory,error){
	if m.cfg.Databases==nil{return cloudSnapshotInventory{},errors.New("database manager unavailable")}
	client,root,err:=m.cfg.Databases.YandexStorage()
	if err!=nil{return cloudSnapshotInventory{},err}
	return scanCloudSnapshots(ctx,client,root+"/server")
}

func (m *snapshotManager) cloudInventoryAt(ctx context.Context,root string)(cloudSnapshotInventory,error){
	if m.cfg.Databases==nil{return cloudSnapshotInventory{},errors.New("database manager unavailable")}
	client,_,err:=m.cfg.Databases.YandexStorage();if err!=nil{return cloudSnapshotInventory{},err}
	return scanCloudSnapshots(ctx,client,root)
}

// Only a valid final manifest makes a directory a completed restore point.
// Distinguish incomplete directories from errors reading completed snapshots.
func scanCloudSnapshots(ctx context.Context,client yandexdisk.Client,root string)(cloudSnapshotInventory,error){
	var result cloudSnapshotInventory
	names,err:=client.ListDirectories(ctx,root)
	if errors.Is(err,yandexdisk.ErrNotFound){return result,nil}
	if err!=nil{return result,err}
	var problems []error
	for _,id:=range names{
		if !validSnapshotID(id){continue}
		if err:=ctx.Err();err!=nil{problems=append(problems,err);break}
		var idx snapshotIndex
		err:=client.GetJSON(ctx,root+"/"+id+"/manifest.json",&idx)
		if errors.Is(err,yandexdisk.ErrNotFound){
			result.Incomplete=append(result.Incomplete,id)
			continue
		}
		if err==nil{err=validateSnapshotIndex(idx,id)}
		if err!=nil{
			problems=append(problems,fmt.Errorf("snapshot %s: %w",id,err))
			continue
		}
		result.Snapshots=append(result.Snapshots,idx)
	}
	sort.Slice(result.Snapshots,func(i,j int)bool{return result.Snapshots[i].ID>result.Snapshots[j].ID})
	sort.Sort(sort.Reverse(sort.StringSlice(result.Incomplete)))
	return result,errors.Join(problems...)
}

func (m *snapshotManager) cloudListAt(ctx context.Context,root string)([]snapshotIndex,error){
	inventory,err:=m.cloudInventoryAt(ctx,root)
	return inventory.Snapshots,err
}

func (m *snapshotManager) pruneCloud(ctx context.Context,root string,keep int)error{
	if keep==0{return nil}
	list,err:=m.cloudListAt(ctx,root);if err!=nil{return err}
	client,_,err:=m.cfg.Databases.YandexStorage();if err!=nil{return err}
	if len(list)>keep{for _,idx:=range list[keep:]{if err:=client.DeleteDirectory(ctx,root+"/"+idx.ID);err!=nil{return err}}}
	return nil
}

func (m *snapshotManager) download(ctx context.Context,id string)error{
	if !snapshotIDPattern.MatchString(id){return errors.New("invalid snapshot ID")}
	if m.cfg.Databases==nil{return errors.New("database manager unavailable")}
	_,root,err:=m.cfg.Databases.YandexStorage()
	if err!=nil{return err}
	return m.downloadAt(ctx,id,snapshotRoot,root+"/server",0)
}

func (m *snapshotManager) downloadAt(ctx context.Context,id,localRoot,remoteRoot string,appID int64)error{
	if !validSnapshotID(id){return errors.New("invalid snapshot ID")}
	client,_,err:=m.cfg.Databases.YandexStorage();if err!=nil{return err}
	root:=remoteRoot+"/"+id
	var idx snapshotIndex
	if err:=client.GetJSON(ctx,root+"/manifest.json",&idx);err!=nil{return err}
	if err:=validateSnapshotIndex(idx,id);err!=nil{return err}
	if idx.AppID!=appID{return errors.New("snapshot scope does not match")}
	if err:=os.MkdirAll(localRoot,0700);err!=nil{return err}
	final:=filepath.Join(localRoot,id)
	if _,err:=os.Lstat(final);err==nil{return errors.New("snapshot already exists locally")}else if !errors.Is(err,os.ErrNotExist){return err}
	dir,err:=os.MkdirTemp(localRoot,".download-*")
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
