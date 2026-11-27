package mcpaccess

import (
    "context"
    "encoding/base64"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "net"
    "net/http"
    "os"
    "os/exec"
    "path/filepath"
    "strings"
    "sort"
    "syscall"
    "sync"
    "time"

    "github.com/creack/pty"
    "github.com/bau59/open-go-panel/internal/state"
)

const terminalSocket = "/run/open-go-panel-mcp/control.sock"
const terminalHistory = 2 << 20
const terminalChunk = 64 << 10

type TerminalInfo struct {
    ID string `json:"session_id"`
    AppID int64 `json:"app_id"`
    User string `json:"user"`
    Mode string `json:"mode"`
    StartedAt time.Time `json:"started_at"`
    Status string `json:"status"`
    ExitCode *int `json:"exit_code"`
}
type terminalSession struct {
    conn Connection
    info TerminalInfo
    cmd *exec.Cmd
    tty *os.File
    mu sync.Mutex
    data []byte
    head int
    base int64
    stopMu sync.Mutex
    done chan struct{}
    input chan []byte
}
type TerminalManager struct {
    grants *Store
    state *state.Store
    mu sync.Mutex
    sessions map[string]*terminalSession
}
func NewTerminalManager(s *state.Store)*TerminalManager {
    return &TerminalManager{grants:New(s),state:s,sessions:map[string]*terminalSession{}}
}
func (s *terminalSession) snapshot() TerminalInfo {
    s.mu.Lock();defer s.mu.Unlock();return s.info
}
// Circular byte buffer: high-volume output must not copy the full
// retained history on every PTY read.
func (s *terminalSession) append(p []byte){
    s.mu.Lock();defer s.mu.Unlock()
    if len(p)>=terminalHistory{
        s.base+=int64(len(s.data)+len(p)-terminalHistory)
        s.data=append(s.data[:0],p[len(p)-terminalHistory:]...);s.head=0;return
    }
    if free:=terminalHistory-len(s.data);free>0{
        n:=len(p);if n>free{n=free}
        s.data=append(s.data,p[:n]...);p=p[n:]
    }
    if len(p)==0{return}
    n:=copy(s.data[s.head:],p)
    copy(s.data,p[n:])
    s.head=(s.head+len(p))%terminalHistory
    s.base+=int64(len(p))
}
func (s *terminalSession) read(offset int64) map[string]any {
    s.mu.Lock();defer s.mu.Unlock()
    lost:=offset<s.base
    if offset<s.base {offset=s.base}
    end:=s.base+int64(len(s.data))
    if offset>end {offset=end}
    next:=offset+terminalChunk;if next>end{next=end}
    data:=make([]byte,int(next-offset))
    if len(data)>0{
        start:=(s.head+int(offset-s.base))%len(s.data)
        n:=copy(data,s.data[start:])
        copy(data[n:],s.data)
    }
    return map[string]any{"session_id":s.info.ID,"status":s.info.Status,"exit_code":s.info.ExitCode,
        "stdout":string(data),"stderr":"","output":string(data),"output_base64":base64.StdEncoding.EncodeToString(data),
        "merged_streams":true,"offset":offset,"next_offset":next,"oldest_offset":s.base,"end_offset":end,"truncated":lost}
}
func terminalUnit(id string) string {return "ogp-mcp-terminal-"+id+".service"}
func terminalArgs(c Connection,id,command,workdir string)([]string,error){
    if !challengeRE.MatchString(id){return nil,errors.New("invalid session ID")}
    username,err:=c.TerminalUser();if err!=nil{return nil,err}
    if workdir==""{workdir=c.Root}else if !filepath.IsAbs(workdir){workdir=filepath.Join(c.Root,workdir)}
    if strings.ContainsAny(workdir,"\x00\r\n")||strings.ContainsRune(command,0){return nil,errors.New("invalid command or working directory")}
    // Disable systemd's variable/specifier expansion on user-provided arguments.
    // The target service receives the selected user's normal host permissions.
    args:=[]string{"--quiet","--pty","--wait","--collect","--service-type=exec","--expand-environment=no",
        "--unit="+terminalUnit(id),"--property=User="+username,"--property=WorkingDirectory="+strings.ReplaceAll(workdir,"%","%%"),
        "--property=KillMode=control-group","--property=TimeoutStopSec=5s","--property=BindsTo=open-go-panel-mcp.service",
        "--property=After=open-go-panel-mcp.service","--setenv=TERM=xterm-256color","--","/bin/bash","-l"}
    if command!=""{args=append(args,"-c",command)}
    return args,nil
}

func (m *TerminalManager) start(ctx context.Context,c Connection,token,command,workdir string)(*terminalSession,error){
    m.mu.Lock();defer m.mu.Unlock()
    current,err:=m.grants.Authenticate(ctx,c.ID,c.Resource,token)
    if err!=nil||current.TerminalPolicy!=c.TerminalPolicy{return nil,ErrUnauthorized}
    // Bound retained output, not execution time. Finished sessions are kept
    // for reconnect/read until evicted by the next start.
    for id,s:=range m.sessions {
        if s.snapshot().Status!="running"&&s.snapshot().Status!="starting"&&time.Since(s.info.StartedAt)>time.Hour{delete(m.sessions,id)}
    }
    if len(m.sessions)>=128{
        var oldest *terminalSession
        for _,s:=range m.sessions{if s.snapshot().Status=="exited"&&(oldest==nil||s.info.StartedAt.Before(oldest.info.StartedAt)){oldest=s}}
        if oldest!=nil{delete(m.sessions,oldest.info.ID)}else{return nil,errors.New("too many active terminal sessions")}
    }
    id,err:=secret();if err!=nil{return nil,err}
    args,err:=terminalArgs(current,id,command,workdir);if err!=nil{return nil,err}
    user,err:=current.TerminalUser();if err!=nil{return nil,err}
    cmd:=exec.Command("systemd-run",args...)
    cmd.Env=[]string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin","TERM=xterm-256color"}
    tty,err:=pty.StartWithSize(cmd,&pty.Winsize{Rows:24,Cols:120});if err!=nil{return nil,err}
    s:=&terminalSession{conn:current,info:TerminalInfo{ID:id,AppID:c.AppID,User:user,Mode:current.TerminalMode(),StartedAt:time.Now().UTC(),Status:"starting"},cmd:cmd,tty:tty,done:make(chan struct{}),input:make(chan []byte,16)}
    m.sessions[id]=s
    drained:=make(chan struct{})
    go func(){defer close(drained);buf:=make([]byte,8192);for{n,err:=tty.Read(buf);if n>0{s.append(buf[:n])};if err!=nil{return}}}()
    go func(){for{select{case <-s.done:return;case data:=<-s.input:if _,err:=tty.Write(data);err!=nil{return}}}}()
    go func(){
        err:=cmd.Wait()
        select{case <-drained:case <-time.After(time.Second):_ = tty.Close();<-drained}
        _=tty.Close()
        code:=0;if err!=nil{code=-1;var exit *exec.ExitError;if errors.As(err,&exit){code=exit.ExitCode()}}
        s.mu.Lock();s.info.Status="exited";s.info.ExitCode=&code;s.mu.Unlock()
        close(s.done)
        m.audit(s,"exit",fmt.Sprintf("exit_code=%d",code))
    }()
    // Wait until systemd has accepted the unit, or a short command exits.
    // This closes the start/stop race with grant revocation.
    readyCtx,cancel:=context.WithTimeout(context.Background(),8*time.Second);defer cancel()
    for {
        select{case <-s.done:m.audit(s,"start","command exited");return s,nil;default:}
        out,e:=exec.CommandContext(readyCtx,"systemctl","show","--property=LoadState","--value",terminalUnit(id)).Output()
        if e==nil&&strings.TrimSpace(string(out))=="loaded" {
            s.mu.Lock();if s.info.Status=="starting"{s.info.Status="running"};s.mu.Unlock()
            m.audit(s,"start","");return s,nil
        }
        select{case <-s.done:return s,nil;case <-readyCtx.Done():
            _=cmd.Process.Kill();_ = s.tty.Close()
            stopCtx,stopCancel:=context.WithTimeout(context.Background(),10*time.Second)
            _=exec.CommandContext(stopCtx,"systemctl","stop",terminalUnit(id)).Run();stopCancel()
            return nil,errors.New("terminal unit failed to start")
        case <-time.After(100*time.Millisecond):}
    }
}
func (m *TerminalManager) audit(s *terminalSession,action,detail string){
    ctx,cancel:=context.WithTimeout(context.Background(),3*time.Second);defer cancel()
    m.state.Audit(ctx,"mcp.terminal."+action,fmt.Sprintf("app:%d",s.conn.AppID),
        fmt.Sprintf("grant=%s client=%s session=%s user=%s mode=%s %s",s.conn.ID,s.conn.ClientID,s.info.ID,s.info.User,s.info.Mode,detail))
}
func (m *TerminalManager) list(appID int64,grant,client,policy string)[]TerminalInfo{
    m.mu.Lock();defer m.mu.Unlock()
    out:=[]TerminalInfo{}
    for _,s:=range m.sessions{if (appID==0||s.conn.AppID==appID)&&(grant==""||s.conn.ID==grant)&&(client==""||(s.conn.ClientID==client&&s.conn.TerminalPolicy==policy)){out=append(out,s.snapshot())}}
    sort.Slice(out,func(i,j int)bool{return out[i].StartedAt.After(out[j].StartedAt)})
    return out
}
func (m *TerminalManager) get(c Connection,id string)(*terminalSession,error){
    m.mu.Lock();defer m.mu.Unlock()
    s:=m.sessions[id];if s==nil||s.conn.ID!=c.ID||s.conn.ClientID!=c.ClientID||s.conn.TerminalPolicy!=c.TerminalPolicy{return nil,errors.New("terminal session not found")}
    return s,nil
}
func (m *TerminalManager) stop(s *terminalSession)error{
    s.stopMu.Lock();defer s.stopMu.Unlock()
    select{case <-s.done:return nil;default:}
    ctx,cancel:=context.WithTimeout(context.Background(),15*time.Second);defer cancel()
    out,err:=exec.CommandContext(ctx,"systemctl","stop",terminalUnit(s.info.ID)).CombinedOutput()
    if err!=nil{select{case <-s.done:return nil;default:return fmt.Errorf("stop terminal: %w: %s",err,strings.TrimSpace(string(out)))}}
    select{case <-s.done:m.audit(s,"stop","");return nil;case <-ctx.Done():return errors.New("terminal stop not confirmed")}
}
func (m *TerminalManager) stopAll(sessions []*terminalSession)error{
    errs:=make(chan error,len(sessions))
    for _,s:=range sessions{go func(s *terminalSession){errs<-m.stop(s)}(s)}
    var failures []error
    for range sessions{if err:=<-errs;err!=nil{failures=append(failures,err)}}
    return errors.Join(failures...)
}
func (m *TerminalManager) stopMatching(appID int64,id,grant string)error{
    // Serialize the lookup with startup, but never hold the manager lock
    // while waiting for systemd to terminate a process group.
    m.mu.Lock()
    var sessions []*terminalSession
    for _,s:=range m.sessions{
        if s.conn.AppID==appID&&(id==""||s.info.ID==id)&&(grant==""||s.conn.ID==grant){sessions=append(sessions,s)}
    }
    m.mu.Unlock()
    if id!=""&&len(sessions)==0{return errors.New("terminal session not found")}
    return m.stopAll(sessions)
}
func (m *TerminalManager) Monitor(ctx context.Context){
    ticker:=time.NewTicker(time.Second);defer ticker.Stop()
    for{select{case <-ctx.Done():return;case <-ticker.C:
        m.mu.Lock()
        var sessions []*terminalSession
        for _,s:=range m.sessions{if s.snapshot().Status!="exited"{sessions=append(sessions,s)}}
        m.mu.Unlock()
        var invalid []*terminalSession
        for _,s:=range sessions{
            if ctx.Err()!=nil{return}
            c,err:=m.grants.ByID(ctx,s.conn.ID)
            if err==nil&&c.TerminalPolicy==s.conn.TerminalPolicy {
                var active bool;active,err=m.grants.HasClientRefresh(ctx,c.ID,s.conn.ClientID);if err==nil&&active{continue}
            }
            // Fail closed on loss of grant or inability to verify it.
            invalid=append(invalid,s)
        }
        _=m.stopAll(invalid)
    }}
}

// The administrative listener is never exposed by the public MCP HTTP mux.
// Its filesystem directory and socket are owned by root with mode 0700/0600.
func (m *TerminalManager) ServeAdmin(ctx context.Context)(func(),error){
    dir:=filepath.Dir(terminalSocket)
    if err:=os.MkdirAll(dir,0700);err!=nil{return nil,err}
    info,err:=os.Lstat(dir);if err!=nil||!info.IsDir()||info.Mode().Perm()!=0700{return nil,errors.New("invalid MCP control directory")}
    if stat,ok:=info.Sys().(*syscall.Stat_t);!ok||stat.Uid!=0{return nil,errors.New("MCP control directory must be root-owned")}
    if err:=os.Remove(terminalSocket);err!=nil&&!errors.Is(err,os.ErrNotExist){return nil,err}
    ln,err:=net.Listen("unix",terminalSocket);if err!=nil{return nil,err}
    if err:=os.Chmod(terminalSocket,0600);err!=nil{ln.Close();return nil,err}
    mux:=http.NewServeMux()
    mux.HandleFunc("POST /sessions",func(w http.ResponseWriter,r *http.Request){
        var req struct{AppID int64; ID,Grant,Action string}
        r.Body=http.MaxBytesReader(w,r.Body,4096)
        if json.NewDecoder(r.Body).Decode(&req)!=nil||req.AppID<=0{http.Error(w,"invalid request",400);return}
        switch req.Action{
        case "list":jsonResponse(w,200,m.list(req.AppID,"","",""))
        case "stop":
            if !challengeRE.MatchString(req.ID){http.Error(w,"invalid session",400);return}
            if err:=m.stopMatching(req.AppID,req.ID,"");err!=nil{http.Error(w,err.Error(),500);return};jsonResponse(w,200,map[string]bool{"ok":true})
        case "revoke":
            if !challengeRE.MatchString(req.Grant){http.Error(w,"invalid grant",400);return}
            if err:=m.stopMatching(req.AppID,"",req.Grant);err!=nil{http.Error(w,err.Error(),500);return};jsonResponse(w,200,map[string]bool{"ok":true})
        default:http.Error(w,"invalid action",400)
        }
    })
    srv:=&http.Server{Handler:mux,ReadHeaderTimeout:3*time.Second,ReadTimeout:5*time.Second,WriteTimeout:90*time.Second}
    go srv.Serve(ln)
    go m.Monitor(ctx)
    return func(){_ = srv.Close();_ = os.Remove(terminalSocket)},nil
}
func terminalAdmin(ctx context.Context,appID int64,action,id,grant string,out any)error{
    data,err:=json.Marshal(map[string]any{"AppID":appID,"Action":action,"ID":id,"Grant":grant});if err!=nil{return err}
    tr:=&http.Transport{DialContext:func(ctx context.Context,_,_ string)(net.Conn,error){return (&net.Dialer{}).DialContext(ctx,"unix",terminalSocket)}}
    defer tr.CloseIdleConnections()
    req,err:=http.NewRequestWithContext(ctx,"POST","http://localhost/sessions",strings.NewReader(string(data)));if err!=nil{return err}
    res,err:=(&http.Client{Transport:tr,Timeout:85*time.Second}).Do(req);if err!=nil{return fmt.Errorf("MCP terminal control unavailable: %w",err)};defer res.Body.Close()
    if res.StatusCode!=200{data,_:=io.ReadAll(io.LimitReader(res.Body,4096));return errors.New(strings.TrimSpace(string(data)))}
    return json.NewDecoder(res.Body).Decode(out)
}
func AdminTerminalList(ctx context.Context,appID int64)([]TerminalInfo,error){
    var out []TerminalInfo;err:=terminalAdmin(ctx,appID,"list","","",&out);return out,err
}
func AdminTerminalStop(ctx context.Context,appID int64,id string)error{
    var out map[string]bool;return terminalAdmin(ctx,appID,"stop",id,"",&out)
}
func AdminTerminalRevoke(ctx context.Context,c Connection)error{
    var out map[string]bool;return terminalAdmin(ctx,c.AppID,"revoke","",c.ID,&out)
}
