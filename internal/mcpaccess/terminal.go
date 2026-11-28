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
    "os/user"
    "strconv"
    "path/filepath"
    "strings"
    "sort"
    "syscall"
    "sync"
    "time"
    "unicode/utf8"
    "bytes"

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
    EndedAt *time.Time `json:"ended_at,omitempty"`
    Status string `json:"status"`
    ExitCode *int `json:"exit_code"`
}
type terminalSession struct {
    conn Connection
    info TerminalInfo
    runError string
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
// read returns a single byte-exact representation. Binary or split UTF-8
// chunks use base64 automatically, without a second lossy text copy.
func (s *terminalSession) read(offset int64, limit int, encoding, format string) map[string]any {
    s.mu.Lock();defer s.mu.Unlock()
    lost:=offset<s.base
    if offset<s.base {offset=s.base}
    end:=s.base+int64(len(s.data))
    if offset>end {offset=end}
    next:=offset+int64(limit);if next>end{next=end}
    data:=make([]byte,int(next-offset))
    if len(data)>0{
        start:=(s.head+int(offset-s.base))%len(s.data)
        n:=copy(data,s.data[start:])
        copy(data[n:],s.data)
    }
    result:=map[string]any{"session_id":s.info.ID,"status":s.info.Status,"exit_code":s.info.ExitCode,
        "merged_streams":true,"offset":offset,"next_offset":next,"oldest_offset":s.base,"end_offset":end,"truncated":lost,"has_more":next<end}
    if format=="legacy"{
        result["stdout"]=string(data);result["stderr"]="";result["output"]=string(data)
        result["output_base64"]=base64.StdEncoding.EncodeToString(data)
    }else if encoding=="base64"||!utf8.Valid(data)||bytes.IndexByte(data,0)>=0{
        result["output_base64"]=base64.StdEncoding.EncodeToString(data)
    }else{
        result["output"]=string(data)
    }
    return result
}
func (s *terminalSession) metadata()map[string]any{
    s.mu.Lock();defer s.mu.Unlock()
    return map[string]any{"session_id":s.info.ID,"app_id":s.info.AppID,"user":s.info.User,"mode":s.info.Mode,
        "started_at":s.info.StartedAt,"ended_at":s.info.EndedAt,"status":s.info.Status,"exit_code":s.info.ExitCode,
        "oldest_offset":s.base,"end_offset":s.base+int64(len(s.data))}
}
// systemd-run's own diagnostics are not shell output. The shell writes
// directly to the PTY, so a failed launcher can be reported separately.
type terminalDiagnostics struct{buffer bytes.Buffer}
func (d *terminalDiagnostics) Write(p []byte)(int,error){
    n:=len(p)
    if left:=8192-d.buffer.Len();left>0{_,_=d.buffer.Write(p[:min(left,n)])}
    return n,nil
}
func (d *terminalDiagnostics) String()string{return d.buffer.String()}
func (s *terminalSession) executionError()error{
    s.mu.Lock();defer s.mu.Unlock()
    if s.runError!=""{return errors.New(s.runError)}
    return nil
}
func terminalExecutionError(waitErr error,diagnostic string)string{
    if waitErr==nil{return ""}
    var exit *exec.ExitError
    if errors.As(waitErr,&exit)&&exit.ExitCode()>=0&&strings.TrimSpace(diagnostic)==""{return ""}
    diagnostic=strings.TrimSpace(diagnostic)
    if diagnostic==""{diagnostic=waitErr.Error()}
    if len(diagnostic)>2048{diagnostic=diagnostic[:2048]}
    return "terminal launcher failed: "+diagnostic
}

func terminalUnit(id string) string {return "ogp-mcp-terminal-"+id+".service"}
func terminalArgs(c Connection,id,command,workdir,ttyPath string)([]string,error){
    if !challengeRE.MatchString(id){return nil,errors.New("invalid session ID")}
    username,err:=c.TerminalUser();if err!=nil{return nil,err}
    if workdir==""{workdir=c.Root}else if !filepath.IsAbs(workdir){workdir=filepath.Join(c.Root,workdir)}
    if strings.ContainsAny(workdir,"\x00\r\n")||strings.ContainsRune(command,0){return nil,errors.New("invalid command or working directory")}
    // Disable systemd's variable/specifier expansion on user-provided arguments.
    // The target service receives the selected user's normal host permissions.
    args:=[]string{"--quiet","--wait","--collect","--service-type=exec","--expand-environment=no","--description=Open Go Panel MCP terminal",
        "--unit="+terminalUnit(id),"--property=User="+username,"--property=WorkingDirectory="+strings.ReplaceAll(workdir,"%","%%"),
        "--property=KillMode=control-group","--property=TimeoutStopSec=5s","--property=BindsTo=open-go-panel-mcp.service",
        "--property=After=open-go-panel-mcp.service","--setenv=TERM=xterm-256color",
        "--property=TTYPath="+ttyPath,"--property=StandardInput=tty-force","--property=StandardOutput=tty","--property=StandardError=tty",
        "--property=TTYReset=no","--property=TTYVHangup=no","--property=TTYVTDisallocate=no","--","/bin/bash","-l"}
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
        if info:=s.snapshot();info.EndedAt!=nil&&time.Since(*info.EndedAt)>time.Hour{delete(m.sessions,id)}
    }
    if len(m.sessions)>=128{
        var oldest *terminalSession
        for _,s:=range m.sessions{if info:=s.snapshot();info.EndedAt!=nil&&(oldest==nil||info.EndedAt.Before(*oldest.snapshot().EndedAt)){oldest=s}}
        if oldest!=nil{delete(m.sessions,oldest.info.ID)}else{return nil,errors.New("too many active terminal sessions")}
    }
    id,err:=secret();if err!=nil{return nil,err}
    username,err:=current.TerminalUser();if err!=nil{return nil,err}
    account,err:=user.Lookup(username);if err!=nil{return nil,err}
    uid,err:=strconv.Atoi(account.Uid);if err!=nil{return nil,err}
    gid,err:=strconv.Atoi(account.Gid);if err!=nil{return nil,err}
    // One real PTY, attached directly by PID 1 to the transient service.
    // The local systemd-run client only waits for its exit status.
    tty,slave,err:=pty.Open();if err!=nil{return nil,err}
    closePTY:=func(){_ = slave.Close();_ = tty.Close()}
    if err=slave.Chown(uid,gid);err!=nil{closePTY();return nil,err}
    if err=slave.Chmod(0600);err!=nil{closePTY();return nil,err}
    if err=pty.Setsize(tty,&pty.Winsize{Rows:24,Cols:120});err!=nil{closePTY();return nil,err}
    args,err:=terminalArgs(current,id,command,workdir,slave.Name());if err!=nil{closePTY();return nil,err}
    cmd:=exec.Command("systemd-run",args...)
    cmd.Env=[]string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin","TERM=xterm-256color"}
    s:=&terminalSession{conn:current,info:TerminalInfo{ID:id,AppID:c.AppID,User:username,Mode:current.TerminalMode(),StartedAt:time.Now().UTC(),Status:"starting"},cmd:cmd,tty:tty,done:make(chan struct{}),input:make(chan []byte,16)}
    diagnostics:=&terminalDiagnostics{}
    cmd.Stdout=diagnostics;cmd.Stderr=diagnostics
    if err=cmd.Start();err!=nil{closePTY();return nil,err}
    m.sessions[id]=s
    kind:="shell";if command!=""{kind="command"}
    m.audit(s,"start",fmt.Sprintf("kind=%s command_bytes=%d (content omitted)",kind,len(command)))
    drained:=make(chan struct{})
    go func(){defer close(drained);buf:=make([]byte,8192);for{n,err:=tty.Read(buf);if n>0{s.append(buf[:n])};if err!=nil{return}}}()
    go func(){for{select{case <-s.done:return;case data:=<-s.input:if _,err:=tty.Write(data);err!=nil{return}}}}()
    go func(){
        err:=cmd.Wait()
        _=slave.Close()
        select{case <-drained:case <-time.After(time.Second):_ = tty.Close();<-drained}
        _=tty.Close()
        code:=0;if err!=nil{code=-1;var exit *exec.ExitError;if errors.As(err,&exit){code=exit.ExitCode()}}
        ended:=time.Now().UTC()
        runError:=terminalExecutionError(err,diagnostics.String())
        s.mu.Lock();s.info.Status="exited";s.info.ExitCode=&code;s.info.EndedAt=&ended;s.runError=runError;s.mu.Unlock()
        close(s.done)
        m.audit(s,"exit",fmt.Sprintf("exit_code=%d",code))
    }()
    // Wait until systemd has accepted the unit, or a short command exits.
    // This closes the start/stop race with grant revocation.
    readyCtx,cancel:=context.WithTimeout(context.Background(),8*time.Second);defer cancel()
    completed:=func()(*terminalSession,error){
        if err:=s.executionError();err!=nil{delete(m.sessions,id);return nil,err}
        return s,nil
    }
    for {
        select{case <-s.done:return completed();default:}
        out,e:=exec.CommandContext(readyCtx,"systemctl","show","--property=ActiveState","--value",terminalUnit(id)).Output()
        if e==nil&&strings.TrimSpace(string(out))=="active" {
            _=slave.Close()
            s.mu.Lock();if s.info.Status=="starting"{s.info.Status="running"};s.mu.Unlock()
            return s,nil
        }
        select{case <-s.done:return completed();case <-readyCtx.Done():
            _=cmd.Process.Kill();_ = slave.Close();_ = s.tty.Close()
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
func (m *TerminalManager) stopMatching(appID int64,id,grant,policy string)error{
    // Serialize the lookup with startup, but never hold the manager lock
    // while waiting for systemd to terminate a process group.
    m.mu.Lock()
    var sessions []*terminalSession
    for _,s:=range m.sessions{
        if s.conn.AppID==appID&&(id==""||s.info.ID==id)&&(grant==""||s.conn.ID==grant)&&(policy==""||s.conn.TerminalPolicy==policy){sessions=append(sessions,s)}
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
        var req struct{AppID int64; ID,Grant,Policy,Action string}
        r.Body=http.MaxBytesReader(w,r.Body,4096)
        if json.NewDecoder(r.Body).Decode(&req)!=nil||req.AppID<=0{http.Error(w,"invalid request",400);return}
        switch req.Action{
        case "list":jsonResponse(w,200,m.list(req.AppID,"","",""))
        case "stop":
            if !challengeRE.MatchString(req.ID){http.Error(w,"invalid session",400);return}
            if err:=m.stopMatching(req.AppID,req.ID,"","");err!=nil{http.Error(w,err.Error(),500);return};jsonResponse(w,200,map[string]bool{"ok":true})
        case "revoke":
            if !challengeRE.MatchString(req.Grant){http.Error(w,"invalid grant",400);return}
            if err:=m.stopMatching(req.AppID,"",req.Grant,req.Policy);err!=nil{http.Error(w,err.Error(),500);return};jsonResponse(w,200,map[string]bool{"ok":true})
        default:http.Error(w,"invalid action",400)
        }
    })
    srv:=&http.Server{Handler:mux,ReadHeaderTimeout:3*time.Second,ReadTimeout:5*time.Second,WriteTimeout:90*time.Second}
    go srv.Serve(ln)
    go m.Monitor(ctx)
    return func(){_ = srv.Close();_ = os.Remove(terminalSocket)},nil
}
func terminalAdmin(ctx context.Context,appID int64,action,id,grant,policy string,out any)error{
    data,err:=json.Marshal(map[string]any{"AppID":appID,"Action":action,"ID":id,"Grant":grant,"Policy":policy});if err!=nil{return err}
    tr:=&http.Transport{DialContext:func(ctx context.Context,_,_ string)(net.Conn,error){return (&net.Dialer{}).DialContext(ctx,"unix",terminalSocket)}}
    defer tr.CloseIdleConnections()
    req,err:=http.NewRequestWithContext(ctx,"POST","http://localhost/sessions",strings.NewReader(string(data)));if err!=nil{return err}
    res,err:=(&http.Client{Transport:tr,Timeout:85*time.Second}).Do(req);if err!=nil{return fmt.Errorf("MCP terminal control unavailable: %w",err)};defer res.Body.Close()
    if res.StatusCode!=200{data,_:=io.ReadAll(io.LimitReader(res.Body,4096));return errors.New(strings.TrimSpace(string(data)))}
    return json.NewDecoder(res.Body).Decode(out)
}
func AdminTerminalList(ctx context.Context,appID int64)([]TerminalInfo,error){
    var out []TerminalInfo;err:=terminalAdmin(ctx,appID,"list","","","",&out);return out,err
}
func AdminTerminalStop(ctx context.Context,appID int64,id string)error{
    var out map[string]bool;return terminalAdmin(ctx,appID,"stop",id,"","",&out)
}
func AdminTerminalRevoke(ctx context.Context,c Connection)error{
    var out map[string]bool;return terminalAdmin(ctx,c.AppID,"revoke","",c.ID,"",&out)
}


// A policy change stops the old generation, never a concurrently reauthorized
// client under the new mode. Full revoke still stops all generations.
func AdminTerminalPolicyChanged(ctx context.Context,c Connection)error{
    var out map[string]bool;return terminalAdmin(ctx,c.AppID,"revoke","",c.ID,c.TerminalPolicy,&out)
}
