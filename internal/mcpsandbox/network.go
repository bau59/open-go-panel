package mcpsandbox

import (
    "context"
    "errors"
    "fmt"
    "io"
    "net"
    "os"
    "runtime"
    "strconv"
    "sync"

    "golang.org/x/sys/unix"
)

// NetworkAccess selects only local database TCP endpoints. No interface
// connecting the sandbox to the host or Internet is ever created.
type NetworkAccess struct {
    MySQL bool
    Redis bool
}

func (a NetworkAccess) ports() []int {
    var ports []int
    if a.MySQL { ports=append(ports,3306) }
    if a.Redis { ports=append(ports,6379) }
    return ports
}

// Open listening sockets inside the child's private network namespace.
// All outbound dialing happens on ordinary host threads after setns is undone.
func sandboxListeners(pid int, ports []int) ([]int,error) {
    type outcome struct { fds []int; err error }
    done:=make(chan outcome,1)
    go func(){
        runtime.LockOSThread()
        // If restoring the namespace fails, exit this locked goroutine
        // without unlocking; Go discards the thread instead of reusing it.
        original,err:=unix.Open("/proc/thread-self/ns/net",unix.O_RDONLY|unix.O_CLOEXEC,0)
        if err!=nil{runtime.UnlockOSThread();done<-outcome{err:err};return}
        defer unix.Close(original)
        target,err:=unix.Open(fmt.Sprintf("/proc/%d/ns/net",pid),unix.O_RDONLY|unix.O_CLOEXEC,0)
        if err!=nil{runtime.UnlockOSThread();done<-outcome{err:err};return}
        defer unix.Close(target)
        var a,b unix.Stat_t
        if err=unix.Fstat(original,&a);err==nil{err=unix.Fstat(target,&b)}
        if err!=nil||a.Dev==b.Dev&&a.Ino==b.Ino{
            runtime.UnlockOSThread()
            if err==nil{err=errors.New("sandbox network namespace is not private")}
            done<-outcome{err:err};return
        }
        if err=unix.Setns(target,unix.CLONE_NEWNET);err!=nil{runtime.UnlockOSThread();done<-outcome{err:err};return}
        fds,setupErr:=listenInNamespace(ports)
        restoreErr:=unix.Setns(original,unix.CLONE_NEWNET)
        if restoreErr==nil{runtime.UnlockOSThread()}
        if setupErr!=nil||restoreErr!=nil{
            for _,fd:=range fds{unix.Close(fd)}
            done<-outcome{err:errors.Join(setupErr,restoreErr)};return
        }
        done<-outcome{fds:fds}
    }()
    r:=<-done
    return r.fds,r.err
}

func listenInNamespace(ports []int)(fds []int,err error){
    defer func(){if err!=nil{for _,fd:=range fds{unix.Close(fd)};fds=nil}}()
    control,err:=unix.Socket(unix.AF_INET,unix.SOCK_DGRAM|unix.SOCK_CLOEXEC,0)
    if err!=nil{return nil,err}
    defer unix.Close(control)
    req,err:=unix.NewIfreq("lo");if err!=nil{return nil,err}
    if err:=unix.IoctlIfreq(control,unix.SIOCGIFFLAGS,req);err!=nil{return nil,err}
    req.SetUint16(req.Uint16()|unix.IFF_UP)
    if err:=unix.IoctlIfreq(control,unix.SIOCSIFFLAGS,req);err!=nil{return nil,err}
    for _,port:=range ports{
        fd,e:=unix.Socket(unix.AF_INET,unix.SOCK_STREAM|unix.SOCK_CLOEXEC,0)
        if e!=nil{return fds,e}
        fds=append(fds,fd)
        if e=unix.Bind(fd,&unix.SockaddrInet4{Port:port,Addr:[4]byte{127,0,0,1}});e!=nil{return fds,e}
        if e=unix.Listen(fd,16);e!=nil{return fds,e}
    }
    return fds,nil
}

func startDatabaseRelay(ctx context.Context,pid int,ports []int)(func(),error){
    fds,err:=sandboxListeners(pid,ports)
    if err!=nil{return nil,fmt.Errorf("prepare database network: %w",err)}
    var listeners []net.Listener
    for i,fd:=range fds{
        file:=os.NewFile(uintptr(fd),"sandbox-database-listener")
        listener,e:=net.FileListener(file)
        file.Close()
        if e!=nil{
            for _,l:=range listeners{l.Close()}
            for _,remaining:=range fds[i+1:]{unix.Close(remaining)}
            return nil,e
        }
        listeners=append(listeners,listener)
    }
    relayCtx,cancel:=context.WithCancel(ctx)
    slots:=make(chan struct{},16)
    var wg sync.WaitGroup
    for i,listener:=range listeners{
        target:=net.JoinHostPort("127.0.0.1",strconv.Itoa(ports[i]))
        wg.Add(1)
        go func(l net.Listener,target string){
            defer wg.Done()
            defer l.Close()
            stop:=context.AfterFunc(relayCtx,func(){l.Close()})
            defer stop()
            for {
                client,err:=l.Accept()
                if err!=nil{return}
                select{
                case slots<-struct{}{}:
                    wg.Add(1)
                    go func(){
                        defer wg.Done()
                        defer func(){<-slots}()
                        defer client.Close()
                        var dialer net.Dialer
                        upstream,err:=dialer.DialContext(relayCtx,"tcp4",target)
                        if err!=nil{return}
                        defer upstream.Close()
                        stop:=context.AfterFunc(relayCtx,func(){client.Close();upstream.Close()})
                        defer stop()
                        done:=make(chan struct{})
                        go func(){defer close(done);_,_=io.Copy(upstream,client);if c,ok:=upstream.(*net.TCPConn);ok{_ = c.CloseWrite()}}()
                        _,_=io.Copy(client,upstream)
                        if c,ok:=client.(*net.TCPConn);ok{_ = c.CloseWrite()}
                        <-done
                    }()
                default:client.Close()
                }
            }
        }(listener,target)
    }
    return func(){cancel();for _,l:=range listeners{l.Close()};wg.Wait()},nil
}
