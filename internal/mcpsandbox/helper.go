package mcpsandbox

import (
    "errors"
    "fmt"
    "os"
    "path/filepath"
    "runtime"
    "strconv"
    "strings"
    "syscall"
    "unsafe"

    "golang.org/x/sys/unix"
)

func bind(source,target string,dir,readonly,device bool)error{
    if dir{if err:=os.MkdirAll(target,0755);err!=nil{return err}}else{
        if err:=os.MkdirAll(filepath.Dir(target),0755);err!=nil{return err}
        file,err:=os.OpenFile(target,os.O_CREATE|os.O_EXCL,0644);if err!=nil{return err};if err:=file.Close();err!=nil{return err}
    }
    // Nonrecursive bind deliberately excludes any subordinate host mounts.
    if err:=unix.Mount(source,target,"",unix.MS_BIND,"");err!=nil{return err}
    flags:=uintptr(unix.MS_BIND|unix.MS_REMOUNT|unix.MS_NOSUID)
    if !device{flags|=unix.MS_NODEV}
    if readonly{flags|=unix.MS_RDONLY}
    if device{flags|=unix.MS_NOEXEC}
    return unix.Mount("",target,"",flags,"")
}

// RunHelper enters the root created only inside the new mount namespace.
// PID 1 is the command process: its exit kills all remaining descendants.
func RunHelper(args []string)error{
    runtime.LockOSThread()
    if len(args)!=8||os.Geteuid()!=0||os.Getpid()!=1{return errors.New("sandbox helper requires a private PID namespace and root setup")}
    stage,owner,uidText,gidText,groupText,workdir,operation,command:=args[0],args[1],args[2],args[3],args[4],args[5],args[6],args[7]
    if filepath.Dir(stage)!=stagingRoot||!strings.HasPrefix(filepath.Base(stage),"run-")||!validWorkDir(workdir)||strings.ContainsAny(owner,":\r\n"){return errors.New("invalid sandbox parameters")}
    uid,err:=strconv.Atoi(uidText);if err!=nil||uid<1000{return errors.New("invalid UID")}
    gid,err:=strconv.Atoi(gidText);if err!=nil||gid<=0{return errors.New("invalid GID")}
    var groups []int
    for _,value:=range strings.Split(groupText,","){n,err:=strconv.Atoi(value);if err!=nil||n<=0{return errors.New("invalid group")};groups=append(groups,n)}
    if err:=unix.Mount("","/","",unix.MS_REC|unix.MS_PRIVATE,"");err!=nil{return fmt.Errorf("private mount namespace: %w",err)}
    if err:=unix.Mount("tmpfs",stage,"tmpfs",unix.MS_NOSUID|unix.MS_NODEV,"size=384m,mode=0755");err!=nil{return fmt.Errorf("sandbox root: %w",err)}
    // These kernel magic links refer to the pinned FDs opened by the parent,
    // not to a path supplied by the model or a replaceable project symlink.
    if err:=bind("/proc/self/fd/3",stage+"/workspace",true,false,false);err!=nil{return fmt.Errorf("bind project: %w",err)}
    if err:=bind("/proc/self/fd/4",stage+"/ogp-helper",false,true,false);err!=nil{return fmt.Errorf("bind helper: %w",err)}
    if err:=unix.Close(3);err!=nil{return err}
    if err:=unix.Close(4);err!=nil{return err}
    if err:=bind("/usr",stage+"/usr",true,true,false);err!=nil{return err}
    for _,name:=range []string{"bin","sbin","lib","lib64"}{
        source:="/"+name
        info,err:=os.Lstat(source)
        if errors.Is(err,os.ErrNotExist){continue}
        if err!=nil{return err}
        if info.Mode()&os.ModeSymlink!=0{
            // Ubuntu's merged-/usr aliases are recreated inside the sandbox.
            if err:=os.Symlink("usr/"+name,stage+source);err!=nil{return err}
        }else if info.IsDir(){if err:=bind(source,stage+source,true,true,false);err!=nil{return err}}else{return errors.New("invalid system runtime path")}
    }
    for _,dir:=range []string{"/etc","/dev","/tmp","/proc"}{if err:=os.MkdirAll(stage+dir,0755);err!=nil{return err}}
    for _,name:=range []string{"/etc/ld.so.cache","/etc/ld.so.conf"}{
        if _,err:=os.Stat(name);errors.Is(err,os.ErrNotExist){continue}else if err!=nil{return err}
        if err:=bind(name,stage+name,false,true,false);err!=nil{return err}
    }
    for _,name:=range []string{"null","zero","random","urandom"}{
        if err:=bind("/dev/"+name,stage+"/dev/"+name,false,false,true);err!=nil{return err}
    }
    if err:=unix.Mount("proc",stage+"/proc","proc",unix.MS_RDONLY|unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC,"");err!=nil{return err}
    if err:=unix.Mount("tmpfs",stage+"/tmp","tmpfs",unix.MS_NOSUID|unix.MS_NODEV,"size=256m,mode=1777");err!=nil{return err}
    if err:=os.Mkdir(stage+"/tmp/home",0700);err!=nil{return err}
    if err:=os.Chown(stage+"/tmp/home",uid,gid);err!=nil{return err}
    if err:=os.WriteFile(stage+"/etc/passwd",[]byte(fmt.Sprintf("%s:x:%d:%d:App owner:/tmp/home:/bin/sh\n",owner,uid,gid)),0644);err!=nil{return err}
    if err:=os.WriteFile(stage+"/etc/group",[]byte(fmt.Sprintf("%s:x:%d:\n",owner,gid)),0644);err!=nil{return err}
    if err:=unix.Chroot(stage);err!=nil{return err}
    if err:=os.Chdir("/");err!=nil{return err}
    // Preexisting hardlinks can expose a shared inode even in a mount
    // namespace. Refuse them rather than silently exposing sibling content.
    count:=0
    if err:=filepath.WalkDir("/workspace",func(path string,entry os.DirEntry,err error)error{
        if err!=nil{return err};count++
        if count>200000{return errors.New("project exceeds sandbox entry limit")}
        if entry.Type().IsRegular(){
            info,err:=entry.Info();if err!=nil{return err}
            stat,ok:=info.Sys().(*syscall.Stat_t);if !ok||stat.Nlink!=1{return errors.New("project contains a hardlinked file")}
        }
        return nil
    });err!=nil{return err}
    for capability:=0;capability<64;capability++{
        err:=unix.Prctl(unix.PR_CAPBSET_DROP,uintptr(capability),0,0,0)
        if err!=nil&&!errors.Is(err,unix.EINVAL){return err}
    }
    if err:=syscall.Setgroups(groups);err!=nil{return err}
    if err:=syscall.Setresgid(gid,gid,gid);err!=nil{return err}
    if err:=syscall.Setresuid(uid,uid,uid);err!=nil{return err}
    if os.Geteuid()!=uid||os.Getegid()!=gid{return errors.New("failed to drop sandbox privileges")}
    if err:=unix.Prctl(unix.PR_SET_NO_NEW_PRIVS,1,0,0,0);err!=nil{return err}
    if err:=restrictSyscalls();err!=nil{return err}
    // openat2 forbids changing the initial cwd through an escaping symlink.
    root,err:=unix.Open("/workspace",unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC,0);if err!=nil{return err}
    if workdir==""{workdir="."}
    cwd,err:=unix.Openat2(root,workdir,&unix.OpenHow{Flags:uint64(unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC),Resolve:unix.RESOLVE_BENEATH|unix.RESOLVE_NO_SYMLINKS|unix.RESOLVE_NO_XDEV})
    unix.Close(root)
    if err!=nil{return err}
    err=unix.Fchdir(cwd);unix.Close(cwd);if err!=nil{return err}
    env:=[]string{"PATH=/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin","HOME=/tmp/home","USER="+owner,"LOGNAME="+owner,"LANG=C.UTF-8","GOCACHE=/tmp/go-cache","GOPATH=/tmp/go","GOPROXY=off","GOSUMDB=off"}
    if operation=="run_command"{
        if command==""||len(command)>16384{return errors.New("invalid command")}
        return unix.Exec("/bin/sh",[]string{"/bin/sh","-c",command},env)
    }
    return unix.Exec("/ogp-helper",[]string{"/ogp-helper","--mcp-file-helper",operation},env)
}

// Prevent access to external filesystem Unix sockets, namespace changes and
// io_uring's alternate syscall path. Local socketpairs remain available.
func restrictSyscalls()error{
    var arch uint32
    switch runtime.GOARCH{case "amd64":arch=unix.AUDIT_ARCH_X86_64;case "arm64":arch=unix.AUDIT_ARCH_AARCH64;default:return errors.New("unsupported sandbox architecture")}
    load:=uint16(unix.BPF_LD|unix.BPF_W|unix.BPF_ABS)
    jeq:=uint16(unix.BPF_JMP|unix.BPF_JEQ|unix.BPF_K)
    ret:=uint16(unix.BPF_RET|unix.BPF_K)
    denied:=uint32(unix.SECCOMP_RET_ERRNO)|uint32(unix.EPERM)
    filter:=[]unix.SockFilter{
        {Code:load,K:4},{Code:jeq,Jt:1,K:arch},{Code:ret,K:unix.SECCOMP_RET_KILL_PROCESS},
        {Code:load,K:0},
        {Code:uint16(unix.BPF_JMP|unix.BPF_JGE|unix.BPF_K),Jf:1,K:0x40000000},{Code:ret,K:denied},
    }
    for _,nr:=range []uint32{unix.SYS_SOCKET,unix.SYS_IO_URING_SETUP,unix.SYS_UNSHARE,unix.SYS_SETNS}{
        filter=append(filter,unix.SockFilter{Code:jeq,Jf:1,K:nr},unix.SockFilter{Code:ret,K:denied})
    }
    filter=append(filter,unix.SockFilter{Code:jeq,Jf:1,K:unix.SYS_CLONE3},unix.SockFilter{Code:ret,K:uint32(unix.SECCOMP_RET_ERRNO)|uint32(unix.ENOSYS)})
    // clone() remains available for threads and child processes, but no
    // process may create another namespace after the privileged setup.
    mask:=uint32(unix.CLONE_NEWNS|unix.CLONE_NEWUSER|unix.CLONE_NEWPID|unix.CLONE_NEWNET|unix.CLONE_NEWIPC|unix.CLONE_NEWUTS|unix.CLONE_NEWCGROUP)
    filter=append(filter,
        unix.SockFilter{Code:jeq,Jf:3,K:unix.SYS_CLONE},
        unix.SockFilter{Code:load,K:16},
        unix.SockFilter{Code:uint16(unix.BPF_JMP|unix.BPF_JSET|unix.BPF_K),Jf:1,K:mask},
        unix.SockFilter{Code:ret,K:denied},
        unix.SockFilter{Code:ret,K:unix.SECCOMP_RET_ALLOW})
    prog:=unix.SockFprog{Len:uint16(len(filter)),Filter:&filter[0]}
    return unix.Prctl(unix.PR_SET_SECCOMP,unix.SECCOMP_MODE_FILTER,uintptr(unsafe.Pointer(&prog)),0,0)
}
