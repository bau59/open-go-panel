package server

import (
    "context"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
    "testing"
)

func TestSnapshotArchiveRetriesAndDiscardsPartial(t *testing.T){
    ctx:=context.Background()
    dest:=filepath.Join(t.TempDir(),"archive.tar.gz")
    attempts:=0
    err:=archiveSnapshotWithRetry(ctx,dest,0,func()*exec.Cmd{
        attempts++
        if attempts==1{return exec.CommandContext(ctx,"sh","-c","printf partial; printf 'file changed as we read it' >&2; exit 1")}
        return exec.CommandContext(ctx,"sh","-c","printf complete")
    })
    if err!=nil||attempts!=2{t.Fatalf("retry failed: %d %v",attempts,err)}
    data,err:=os.ReadFile(dest);if err!=nil||string(data)!="complete"{t.Fatalf("partial archive preserved: %q %v",data,err)}
}

func TestSnapshotArchiveNeverPublishesFailedAttempt(t *testing.T){
    for _,tc:=range []struct{exit string;attempts int}{{"1",3},{"2",1}}{
        t.Run(tc.exit,func(t *testing.T){
            ctx:=context.Background()
            dest:=filepath.Join(t.TempDir(),"archive.tar.gz")
            attempts:=0
            err:=archiveSnapshotWithRetry(ctx,dest,0,func()*exec.Cmd{
                attempts++
                return exec.CommandContext(ctx,"sh","-c","printf partial; printf 'demo.txt: Permission denied' >&2; exit "+tc.exit)
            })
            if err==nil||!strings.Contains(err.Error(),"demo.txt: Permission denied")||attempts!=tc.attempts{t.Fatalf("unexpected diagnostics: %d %v",attempts,err)}
            if _,err:=os.Stat(dest);!os.IsNotExist(err){t.Fatal("failed archive not removed")}
        })
    }
}

func TestSnapshotArchiveDiagnosticsBounded(t *testing.T){
    d:=&snapshotArchiveDiagnostics{}
    n,err:=d.Write([]byte(strings.Repeat("x",snapshotArchiveDiagnosticLimit*2)))
    if err!=nil||n!=snapshotArchiveDiagnosticLimit*2||d.data.Len()!=snapshotArchiveDiagnosticLimit||!d.truncated{t.Fatal("diagnostics were not bounded")}
}

func TestSnapshotArchiveNeverOverwritesExisting(t *testing.T){
    ctx:=context.Background()
    dest:=filepath.Join(t.TempDir(),"archive.tar.gz")
    if err:=os.WriteFile(dest,[]byte("original"),0600);err!=nil{t.Fatal(err)}
    err:=archiveSnapshotWithRetry(ctx,dest,0,func()*exec.Cmd{t.Fatal("existing archive must not start a command");return nil})
    if err==nil{t.Fatal("expected existing archive error")}
    data,err:=os.ReadFile(dest);if err!=nil||string(data)!="original"{t.Fatal("existing archive changed")}
}
