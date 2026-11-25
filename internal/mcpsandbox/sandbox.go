// Package mcpsandbox runs each MCP operation in a new Linux mount, PID,
// network and IPC namespace. Only the selected app and read-only system
// runtime directories are mounted. There is no unsandboxed fallback.
package mcpsandbox

import (
    "bytes"
    "context"
    "errors"
    "fmt"
    "io"
    "os"
    "os/exec"
    "os/user"
    "path/filepath"
    "strconv"
    "strings"
    "syscall"
    "time"

    "golang.org/x/sys/unix"
)

const stagingRoot="/run/open-go-panel-mcp"

type Result struct {
    Output string `json:"output"`
    ExitCode int `json:"exit_code"`
    TimedOut bool `json:"timed_out"`
    Truncated bool `json:"truncated"`
}

type cappedOutput struct {
    buffer bytes.Buffer
    limit int
    truncated bool
}
func (b *cappedOutput) Write(p []byte)(int,error){
    count:=len(p)
    remaining:=max(0,b.limit-b.buffer.Len())
    if count>remaining{b.truncated=true;p=p[:remaining]}
    _,_=b.buffer.Write(p)
    return count,nil
}

func validWorkDir(value string)bool{
    if value==""||value=="."{return true}
    if len(value)>4096||filepath.IsAbs(value)||strings.ContainsAny(value,"\\\x00\r\n"){return false}
    for _,part:=range strings.Split(value,"/"){if part==""||part=="."||part==".."{return false}}
    return true
}

func Run(ctx context.Context,owner,root,workdir,operation,command string,input io.Reader,access NetworkAccess)(Result,error){
    result:=Result{ExitCode:-1}
    if os.Geteuid()!=0{return result,errors.New("MCP sandbox requires the privileged panel service")}
    if !validWorkDir(workdir){return result,errors.New("working directory must be relative to the application")}
    account,err:=user.Lookup(owner);if err!=nil{return result,errors.New("application user unavailable")}
    uid,err:=strconv.Atoi(account.Uid);if err!=nil||uid<1000{return result,errors.New("invalid application UID")}
    gid,err:=strconv.Atoi(account.Gid);if err!=nil||gid<=0{return result,errors.New("invalid application GID")}
    if root!=filepath.Clean(root)||!filepath.IsAbs(root)||filepath.Dir(root)!=filepath.Join(account.HomeDir,"apps"){return result,errors.New("invalid managed application root")}
    groups,err:=account.GroupIds();if err!=nil{return result,errors.New("application groups unavailable")}
    for _,group:=range groups{n,err:=strconv.Atoi(group);if err!=nil||n<=0{return result,errors.New("invalid application group")}}
    base,err:=unix.Open("/",unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC,0);if err!=nil{return result,err}
    defer unix.Close(base)
    fd,err:=unix.Openat2(base,strings.TrimPrefix(root,"/"),&unix.OpenHow{Flags:uint64(unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC),Resolve:unix.RESOLVE_BENEATH|unix.RESOLVE_NO_SYMLINKS})
    if err!=nil{return result,fmt.Errorf("open application directory: %w",err)}
    project:=os.NewFile(uintptr(fd),root);defer project.Close()
    binary,err:=os.Open("/proc/self/exe");if err!=nil{return result,err};defer binary.Close()
    if err:=os.MkdirAll(stagingRoot,0700);err!=nil{return result,err}
    info,err:=os.Lstat(stagingRoot)
    if err!=nil||!info.IsDir()||info.Mode().Perm()&0077!=0{return result,errors.New("unsafe sandbox staging directory")}
    if stat,ok:=info.Sys().(*syscall.Stat_t);!ok||stat.Uid!=0{return result,errors.New("unsafe sandbox staging ownership")}
    stage,err:=os.MkdirTemp(stagingRoot,"run-");if err!=nil{return result,err};defer os.RemoveAll(stage)
    deadline:=90*time.Second
    outputLimit:=4<<20
    if operation=="run_command"{deadline=120*time.Second;outputLimit=256<<10}
    childCtx,cancel:=context.WithTimeout(ctx,deadline);defer cancel()
    var ports []int
    if operation=="run_command"{ports=access.ports()}
    networkFlag:="off"
    if len(ports)>0{networkFlag="database"}
    cmd:=exec.CommandContext(childCtx,"/proc/self/exe","--mcp-sandbox-helper",stage,root,owner,strconv.Itoa(uid),strconv.Itoa(gid),strings.Join(groups,","),workdir,operation,command,networkFlag)
    cmd.Env=[]string{"PATH=/usr/bin:/bin","LANG=C.UTF-8"}
    cmd.Dir="/"
    cmd.ExtraFiles=[]*os.File{project,binary}
    cmd.SysProcAttr=&syscall.SysProcAttr{Cloneflags:unix.CLONE_NEWNS|unix.CLONE_NEWPID|unix.CLONE_NEWNET|unix.CLONE_NEWIPC|unix.CLONE_NEWUTS,Pdeathsig:syscall.SIGKILL}
    cmd.Stdin=input
    output:=&cappedOutput{limit:outputLimit}
    cmd.Stdout=output;cmd.Stderr=output
    cmd.WaitDelay=3*time.Second
    if len(ports)==0{
        err=cmd.Run()
    }else{
        ready,signal,e:=os.Pipe()
        if e!=nil{return result,e}
        defer ready.Close();defer signal.Close()
        cmd.ExtraFiles=append(cmd.ExtraFiles,ready)
        if e=cmd.Start();e!=nil{return result,e}
        ready.Close()
        stop,e:=startDatabaseRelay(childCtx,cmd.Process.Pid,ports)
        if e!=nil{signal.Close();_ = cmd.Process.Kill();_ = cmd.Wait();return result,e}
        defer stop()
        if _,e=signal.Write([]byte{1});e!=nil{signal.Close();_ = cmd.Process.Kill();_ = cmd.Wait();return result,e}
        signal.Close()
        err=cmd.Wait()
    }
    result.Output=output.buffer.String();result.Truncated=output.truncated
    result.TimedOut=errors.Is(childCtx.Err(),context.DeadlineExceeded)
    if cmd.ProcessState!=nil{result.ExitCode=cmd.ProcessState.ExitCode()}
    if err==nil{return result,nil}
    var exit *exec.ExitError
    if errors.As(err,&exit)||childCtx.Err()!=nil{return result,nil}
    return result,fmt.Errorf("start isolated command: %w",err)
}
