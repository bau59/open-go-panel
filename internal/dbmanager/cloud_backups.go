package dbmanager

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "os"
    "os/exec"
    "path/filepath"
    "regexp"
    "sort"
    "strings"
    "time"

    "github.com/bau59/open-go-panel/internal/yandexdisk"
)

var cloudBackupName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,200}\.sql\.gz$`)

type CloudBackup struct {
    Name string
    Size int64
    CreatedAt time.Time
}

func cloudBackupDate(name string) time.Time {
    stamp:=strings.TrimSuffix(name,".sql.gz")
    t,_:=time.Parse("20060102T150405.000000000Z",stamp)
    return t
}

func (m *Manager) cloudArgs(args ...string) []string {
    if m.OffsiteConnection().Provider!="existing" {
        return append([]string{"--config",m.offsiteConfigFile},args...)
    }
    return args
}

func (m *Manager) CloudBackups(ctx context.Context,id int64)([]CloudBackup,error){
    if !m.operationMu.TryLock(){return nil,errOperationInProgress}
    defer m.operationMu.Unlock()
    db,err:=m.Get(id);if err!=nil{return nil,err}
    remote:=m.BackupRemote()
    if remote==""{return nil,errors.New("configure cloud storage first")}
    if err:=validateBackupRemote(remote);err!=nil{return nil,err}
    var out []CloudBackup
    if m.OffsiteConnection().Provider=="yandex"{
        client,root,err:=m.YandexStorage();if err!=nil{return nil,err}
        root+="/"+db.Engine+"/"+db.Name
        names,err:=client.ListDirectories(ctx,root)
        if errors.Is(err,yandexdisk.ErrNotFound){return nil,nil}
        if err!=nil{return nil,err}
        for _,name:=range names{
            if !cloudBackupName.MatchString(name){continue}
            manifest,err:=client.ArchiveManifest(ctx,root+"/"+name)
            if errors.Is(err,yandexdisk.ErrNotFound){continue}
            if err!=nil{return nil,err}
            out=append(out,CloudBackup{Name:name,Size:manifest.Size,CreatedAt:cloudBackupDate(name)})
        }
    }else{
        cmd:=exec.CommandContext(ctx,"rclone",m.cloudArgs("lsjson",remote+"/"+db.Engine+"/"+db.Name,"--files-only","--timeout","20s","--contimeout","10s")...)
        stdout,err:=cmd.StdoutPipe();if err!=nil{return nil,err}
        if err:=cmd.Start();err!=nil{return nil,err}
        const limit=8<<20
        data,readErr:=io.ReadAll(io.LimitReader(stdout,limit+1))
        if readErr!=nil||len(data)>limit{
            _=cmd.Process.Kill();_=cmd.Wait()
            return nil,errors.New("cloud listing exceeds limit or could not be read")
        }
        if err:=cmd.Wait();err!=nil{
            var exit *exec.ExitError
            if errors.As(err,&exit)&&exit.ExitCode()==3{return nil,nil}
            return nil,fmt.Errorf("list cloud SQL backups: %w",err)
        }
        var items []struct{Name string; Size int64; ModTime time.Time}
        if err:=json.Unmarshal(data,&items);err!=nil{return nil,err}
        for _,item:=range items{
            if !cloudBackupName.MatchString(item.Name)||item.Size<=0{continue}
            date:=cloudBackupDate(item.Name)
            if date.IsZero(){date=item.ModTime}
            out=append(out,CloudBackup{Name:item.Name,Size:item.Size,CreatedAt:date})
        }
    }
    sort.Slice(out,func(i,j int)bool{return out[i].Name>out[j].Name})
    return out,nil
}

// StartCloudBackupDownload stages a completed remote archive into local history.
// It never overwrites a local copy or changes the current database.
func (m *Manager) StartCloudBackupDownload(id int64,name string)error{
    if !cloudBackupName.MatchString(name){return errors.New("invalid cloud backup filename")}
    db,err:=m.Get(id);if err!=nil{return err}
    if m.BackupRemote()==""{return errors.New("configure cloud storage first")}
    return m.startBackupTask("cloud-download","Download cloud copy: "+db.Name,func(ctx context.Context)error{
        if !m.operationMu.TryLock(){return errOperationInProgress}
        defer m.operationMu.Unlock()
        db,err:=m.Get(id);if err!=nil{return err}
        root:=filepath.Join("/var/lib/open-go-panel/backups/databases",db.Engine,db.Name)
        if err:=os.MkdirAll(root,0700);err!=nil{return err}
        dst:=filepath.Join(root,name)
        if _,err:=os.Lstat(dst);err==nil{return errors.New("this backup already exists locally")}else if !errors.Is(err,os.ErrNotExist){return err}
        dir,err:=os.MkdirTemp(root,".cloud-download-*");if err!=nil{return err}
        defer os.RemoveAll(dir)
        staged:=filepath.Join(dir,name)
        remote:=m.BackupRemote()
        if remote==""{return errors.New("cloud storage is not configured")}
        if err:=validateBackupRemote(remote);err!=nil{return err}
        if m.OffsiteConnection().Provider=="yandex"{
            client,remoteRoot,err:=m.YandexStorage();if err!=nil{return err}
            if err:=client.DownloadArchive(ctx,remoteRoot+"/"+db.Engine+"/"+db.Name+"/"+name,staged);err!=nil{return err}
        }else{
            cmd:=exec.CommandContext(ctx,"rclone",m.cloudArgs("copyto",remote+"/"+db.Engine+"/"+db.Name+"/"+name,staged,"--timeout","30m","--contimeout","1m","--retries","6")...)
            if err:=cmd.Run();err!=nil{return fmt.Errorf("download cloud SQL backup: %w",err)}
        }
        if err:=os.Chmod(staged,0600);err!=nil{return err}
        if err:=validateCompressedBackup(staged);err!=nil{return fmt.Errorf("invalid downloaded SQL archive: %w",err)}
        if date:=cloudBackupDate(name);!date.IsZero(){if err:=os.Chtimes(staged,date,date);err!=nil{return err}}
        return os.Link(staged,dst)
    })
}
