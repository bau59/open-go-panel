package mcpaccess

import (
    "context"
    "errors"
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
    "time"
)

// /workspace is the file helper's virtual mount, not the host terminal's
// directory. Accept it as an alias for older clients and automation.
func resolveTerminalWorkdir(root, requested string)(string,error){
    if !filepath.IsAbs(root)||strings.ContainsAny(root,"\x00\r\n")||strings.ContainsAny(requested,"\x00\r\n"){
        return "",errors.New("invalid terminal working directory")
    }
    switch {
    case requested==""||requested=="/workspace":
        return filepath.Clean(root),nil
    case strings.HasPrefix(requested,"/workspace/"):
        return filepath.Join(root,strings.TrimPrefix(requested,"/workspace/")),nil
    case filepath.IsAbs(requested):
        return filepath.Clean(requested),nil
    default:
        return filepath.Join(root,requested),nil
    }
}

// Check with the actual Linux account, including supplementary groups and ACLs.
// root's stat alone cannot tell whether the target user can traverse parents.
// The shell script is constant; the directory is an argument, never shell code.
func checkTerminalWorkdir(ctx context.Context,username,dir string)error{
    info,err:=os.Stat(dir)
    if err!=nil{return fmt.Errorf("CHDIR preflight: workdir=%q: %w",dir,err)}
    if !info.IsDir(){return fmt.Errorf("CHDIR preflight: workdir=%q is not a directory",dir)}
    ctx,cancel:=context.WithTimeout(ctx,5*time.Second);defer cancel()
    cmd:=exec.CommandContext(ctx,"/usr/sbin/runuser","-u",username,"--","/bin/sh","-c","cd \"$1\"","ogp-workdir",dir)
    cmd.Env=[]string{"PATH=/usr/bin:/bin","LC_ALL=C"}
    output:=&terminalDiagnostics{}
    cmd.Stdout=output;cmd.Stderr=output
    if err:=cmd.Run();err!=nil{
        if ctx.Err()!=nil{return fmt.Errorf("CHDIR preflight: workdir=%q: %w",dir,ctx.Err())}
        detail:=strings.TrimSpace(output.String())
        if detail==""{detail=err.Error()}
        if len(detail)>1024{detail=detail[:1024]}
        return fmt.Errorf("CHDIR preflight: workdir=%q inaccessible to user %q: %s",dir,username,detail)
    }
    return nil
}

// A directory can disappear between preflight and systemd's actual chdir.
// Inspect only the unique failed launcher's own journal, and never return
// unrelated log messages or classify a normal shell exit 200 as CHDIR.
func terminalStartDiagnostic(id,dir,launcherError string)string{
    ctx,cancel:=context.WithTimeout(context.Background(),3*time.Second);defer cancel()
    cmd:=exec.CommandContext(ctx,"journalctl","--unit="+terminalUnit(id),"--no-pager","--lines=20","--output=cat")
    cmd.Env=[]string{"PATH=/usr/bin:/bin","LC_ALL=C"}
    out:=&terminalDiagnostics{}
    cmd.Stdout=out
    _=cmd.Run()
    return formatTerminalStartDiagnostic(dir,launcherError,out.String())
}
func formatTerminalStartDiagnostic(dir,launcherError,journal string)string{
    if strings.Contains(journal,"Failed at step CHDIR")||strings.Contains(journal,"status=200/CHDIR")||strings.Contains(launcherError,"200/CHDIR"){
        reason:="systemd could not enter the working directory"
        for _,line:=range strings.Split(journal,"\n"){
            if strings.Contains(line,"Failed at step CHDIR"){
                if i:=strings.LastIndex(line,": ");i>=0&&i+2<len(line){
                    reason=line[i+2:]
                    if len(reason)>256{reason=reason[:256]}
                }
                break
            }
        }
        return fmt.Sprintf("terminal launcher failed: 200/CHDIR; workdir=%q: %s",dir,reason)
    }
    return fmt.Sprintf("%s; workdir=%q",launcherError,dir)
}
