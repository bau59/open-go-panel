package mcpsandbox

import (
    "errors"
    "fmt"
    "io"
    "os"

    "golang.org/x/sys/unix"
)

// The sandbox binds only the project, but an existing hardlink can name an
// inode also reachable outside the project. Check shell access without
// collecting or sorting an entire directory in memory. The enclosing
// subprocess deadline bounds the work. Never skip a directory on error.
func checkCommandTree(root string) error {
    fd,err:=unix.Open(root,unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC,0)
    if err!=nil{return err}
    return checkCommandDirectory(fd,root)
}

func checkCommandDirectory(fd int, name string) error {
    dir:=os.NewFile(uintptr(fd),name)
    defer dir.Close()
    for {
        names,readErr:=dir.Readdirnames(512)
        if readErr!=nil&&!errors.Is(readErr,io.EOF){return readErr}
        for _,name:=range names{
            var info unix.Stat_t
            if err:=unix.Fstatat(fd,name,&info,unix.AT_SYMLINK_NOFOLLOW);err!=nil{
                if errors.Is(err,unix.ENOENT){continue}
                return fmt.Errorf("inspect project entry %q: %w",name,err)
            }
            switch info.Mode&unix.S_IFMT{
            case unix.S_IFDIR:
                child,err:=unix.Openat2(fd,name,&unix.OpenHow{
                    Flags:unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC,
                    Resolve:unix.RESOLVE_BENEATH|unix.RESOLVE_NO_SYMLINKS|unix.RESOLVE_NO_XDEV,
                })
                if err!=nil{
                    if errors.Is(err,unix.ENOENT){continue}
                    return fmt.Errorf("inspect project directory %q: %w",name,err)
                }
                if err:=checkCommandDirectory(child,name);err!=nil{return err}
            case unix.S_IFREG:
                if info.Nlink!=1{return fmt.Errorf("project contains a hardlinked file: %q",name)}
            }
        }
        if errors.Is(readErr,io.EOF){return nil}
    }
}
