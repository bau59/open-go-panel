package server

import (
    "bytes"
    "context"
    "errors"
    "fmt"
    "os"
    "os/exec"
    "strings"
    "time"
)

const snapshotArchiveAttempts = 3
const snapshotArchiveDiagnosticLimit = 8192

// Store bounded diagnostics, never the archive contents.
type snapshotArchiveDiagnostics struct {
    data bytes.Buffer
    truncated bool
}

func (d *snapshotArchiveDiagnostics) Write(p []byte)(int,error){
    n:=len(p)
    available:=snapshotArchiveDiagnosticLimit-d.data.Len()
    if n>available{d.truncated=true;p=p[:available]}
    _,_=d.data.Write(p)
    return n,nil
}

func (d *snapshotArchiveDiagnostics) String()string{
    message:=strings.TrimSpace(d.data.String())
    if d.truncated{message+=" [diagnostics truncated]"}
    return message
}

// Use the account's own permissions and preserve ACLs, xattrs and symlinks.
// A changing live tree is retried, never silently accepted as a good archive.
func archiveSnapshotFiles(ctx context.Context,owner,root,dest string)error{
    return archiveSnapshotWithRetry(ctx,dest,time.Second,func()*exec.Cmd{
        return exec.CommandContext(ctx,"runuser","-u",owner,"--","/usr/bin/tar","--acls","--xattrs","-czf","-","-C",root,".")
    })
}

func archiveSnapshotWithRetry(ctx context.Context,dest string,delay time.Duration,command func()*exec.Cmd)error{
    for attempt:=1;attempt<=snapshotArchiveAttempts;attempt++{
        if err:=ctx.Err();err!=nil{return err}
        f,err:=os.OpenFile(dest,os.O_CREATE|os.O_EXCL|os.O_WRONLY,0600)
        if err!=nil{return err}
        diagnostics:=&snapshotArchiveDiagnostics{}
        cmd:=command()
        cmd.Stdout=f
        cmd.Stderr=diagnostics
        runErr:=cmd.Run()
        closeErr:=f.Close()
        if runErr==nil&&closeErr==nil{return nil}
        // Only remove the file created by this attempt.
        if err:=os.Remove(dest);err!=nil{return fmt.Errorf("remove failed archive: %w",err)}
        if err:=ctx.Err();err!=nil{return err}
        if closeErr!=nil{return fmt.Errorf("close archive: %w",closeErr)}
        var exitErr *exec.ExitError
        if errors.As(runErr,&exitErr)&&exitErr.ExitCode()==1&&attempt<snapshotArchiveAttempts{
            timer:=time.NewTimer(delay)
            select{
            case <-ctx.Done():timer.Stop();return ctx.Err()
            case <-timer.C:
            }
            continue
        }
        if detail:=diagnostics.String();detail!=""{
            return fmt.Errorf("attempt %d/%d: %w: %s",attempt,snapshotArchiveAttempts,runErr,detail)
        }
        return fmt.Errorf("attempt %d/%d: %w (no archive diagnostics)",attempt,snapshotArchiveAttempts,runErr)
    }
    return errors.New("archive attempts exhausted")
}
