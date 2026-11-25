package mcpaccess

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "mime"
    "net/http"
    "net/url"
    "path"
    "strings"
    "sync"
    "time"

    "github.com/bau59/open-go-panel/internal/mcpsandbox"
    "github.com/bau59/open-go-panel/internal/state"
)

const ListenAddr="127.0.0.1:18443"
const DomainSetting="mcp.domain"

type Runner func(context.Context,string,string,string,string,string,io.Reader)(mcpsandbox.Result,error)

type Server struct {
    grants *Store
    state *state.Store
    run Runner
    mu sync.Mutex
    active map[int64]bool
}

func NewHandler(s *state.Store,runner Runner)http.Handler{
    if runner==nil{runner=mcpsandbox.Run}
    server:=&Server{grants:New(s),state:s,run:runner,active:map[int64]bool{}}
    mux:=http.NewServeMux()
    oauth:=NewOAuth(server.grants)
    mux.Handle("/oauth/",oauth)
    mux.Handle("/.well-known/",oauth)
    mux.HandleFunc("/mcp/{id}",server.mcp)
    mux.HandleFunc("GET /health",func(w http.ResponseWriter,r *http.Request){_,_=io.WriteString(w,"ok\n")})
    return http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
        w.Header().Set("Cache-Control","no-store")
        w.Header().Set("X-Content-Type-Options","nosniff")
        if r.URL.Path!="/health"{
            domain,_,err:=s.Setting(DomainSetting)
            if err!=nil{http.Error(w,"MCP unavailable",503);return}
            if domain==""||!strings.EqualFold(r.Host,domain){http.NotFound(w,r);return}
            if !validRequestOrigin(r,domain){http.Error(w,"invalid origin",403);return}
        }
        mux.ServeHTTP(w,r)
    })
}

// The OAuth browser continuation is a same-origin navigation protected by
// a one-time, high-entropy polling secret. Older approval pages used
// no-referrer, which makes browsers serialize the Origin as "null".
// Do not extend that exception to the MCP transport or token endpoint.
func validRequestOrigin(r *http.Request,domain string)bool{
    values:=r.Header.Values("Origin")
    if len(values)==0{return true}
    if len(values)!=1{return false}
    origin:=values[0]
    if origin=="https://"+domain||origin=="https://chatgpt.com"{return true}
    if origin!="null"||r.Method!=http.MethodPost||r.Header.Get("Sec-Fetch-Site")!="same-origin"||r.Header.Get("Sec-Fetch-Mode")!="navigate"{return false}
    parts:=strings.Split(r.URL.Path,"/")
    return len(parts)==4&&parts[0]==""&&parts[1]=="oauth"&&len(parts[2])==43&&parts[3]=="continue"
}

type rpcRequest struct{
    JSONRPC string `json:"jsonrpc"`
    ID json.RawMessage `json:"id"`
    Method string `json:"method"`
    Params json.RawMessage `json:"params"`
}

func rpcResult(w http.ResponseWriter,id json.RawMessage,result any){
    jsonResponse(w,200,map[string]any{"jsonrpc":"2.0","id":id,"result":result})
}
func rpcError(w http.ResponseWriter,id json.RawMessage,code int,message string){
    if len(id)==0{id=json.RawMessage("null")}
    jsonResponse(w,200,map[string]any{"jsonrpc":"2.0","id":id,"error":map[string]any{"code":code,"message":message}})
}

func (s *Server) mcp(w http.ResponseWriter,r *http.Request){
    c,err:=s.grants.ByID(r.Context(),r.PathValue("id"))
    if err!=nil{
        if errors.Is(err,ErrUnauthorized){http.Error(w,"authorization revoked or unavailable",401)}else{http.Error(w,"MCP unavailable",503)}
        return
    }
    origin:=strings.TrimSuffix(c.Resource,"/mcp/"+c.ID)
    if origin!="https://"+r.Host{http.NotFound(w,r);return}
    auth:=r.Header.Get("Authorization")
    token,ok:=strings.CutPrefix(auth,"Bearer ")
    if !ok{token=""}
    c,err=s.grants.Authenticate(r.Context(),c.ID,c.Resource,token)
    if err!=nil{
        if !errors.Is(err,ErrUnauthorized){http.Error(w,"MCP unavailable",503);return}
        metadata:=origin+"/.well-known/oauth-protected-resource/mcp/"+r.PathValue("id")
        challenge:=`Bearer resource_metadata="`+metadata+`", scope="`+Scope+`"`
        if token!=""{challenge+=`, error="invalid_token"`}
        w.Header().Set("WWW-Authenticate",challenge)
        http.Error(w,"authorization required",401);return
    }
    if r.Method!="POST"{w.Header().Set("Allow","POST");http.Error(w,"POST required",405);return}
    contentType,_,err:=mime.ParseMediaType(r.Header.Get("Content-Type"))
    if err!=nil||contentType!="application/json"{http.Error(w,"application/json required",415);return}
    if accept:=r.Header.Get("Accept");accept!=""&&!strings.Contains(accept,"application/json")&&!strings.Contains(accept,"*/*"){http.Error(w,"JSON responses required",406);return}
    r.Body=http.MaxBytesReader(w,r.Body,4<<20)
    var req rpcRequest
    decoder:=json.NewDecoder(r.Body)
    if err:=decoder.Decode(&req);err!=nil{rpcError(w,nil,-32700,"invalid JSON");return}
    if err:=decoder.Decode(new(any));!errors.Is(err,io.EOF){rpcError(w,nil,-32600,"invalid request");return}
    if req.JSONRPC!="2.0"||req.Method==""{rpcError(w,req.ID,-32600,"invalid request");return}
    if len(req.ID)==0{
        // Notifications are acknowledged but cannot initiate operations.
        w.WriteHeader(http.StatusAccepted);return
    }
    var validID any
    if err:=json.Unmarshal(req.ID,&validID);err!=nil{rpcError(w,nil,-32600,"invalid request ID");return}
    switch validID.(type){case string,float64:default:rpcError(w,nil,-32600,"invalid request ID");return}
    switch req.Method{
    case "initialize":
        var params struct{Version string `json:"protocolVersion"`}
        if err:=json.Unmarshal(req.Params,&params);err!=nil{rpcError(w,req.ID,-32602,"invalid initialization");return}
        version:=params.Version
        switch version{case "2025-03-26","2025-06-18","2025-11-25":default:version="2025-11-25"}
        rpcResult(w,req.ID,map[string]any{"protocolVersion":version,"capabilities":map[string]any{"tools":map[string]any{"listChanged":false}},
            "serverInfo":map[string]any{"name":"open-go-panel","version":"1.0.0"},
            "instructions":"This connection is limited to one project. Use relative paths; read a file before editing and supply its version. Commands are noninteractive and isolated. The project root is /workspace."})
    case "ping":
        rpcResult(w,req.ID,map[string]any{})
    case "tools/list":
        rpcResult(w,req.ID,map[string]any{"tools":tools()})
    case "resources/list":
        rpcResult(w,req.ID,map[string]any{"resources":[]any{}})
    case "prompts/list":
        rpcResult(w,req.ID,map[string]any{"prompts":[]any{}})
    case "tools/call":
        s.call(w,r,req,c,token)
        return
    default:rpcError(w,req.ID,-32601,"method not found");return
    }
    _=s.grants.RecordSuccess(r.Context(),c)
}

func property(kind,description string)map[string]any{return map[string]any{"type":kind,"description":description}}
func tool(name,description string,readOnly,destructive bool,props map[string]any,required ...string)map[string]any{
    if required==nil{required=[]string{}}
    return map[string]any{"name":name,"description":description,
        "inputSchema":map[string]any{"type":"object","properties":props,"required":required,"additionalProperties":false},
        "annotations":map[string]any{"readOnlyHint":readOnly,"destructiveHint":destructive,"openWorldHint":false,"idempotentHint":readOnly},
        "securitySchemes":[]any{map[string]any{"type":"oauth2","scopes":[]string{Scope}}},
        "_meta":map[string]any{"securitySchemes":[]any{map[string]any{"type":"oauth2","scopes":[]string{Scope}}}}}
}

func tools()[]any{
    p:=func()map[string]any{return property("string","Path relative to the selected project. No absolute paths or parent traversal.")}
    return []any{
        tool("list_directory","List files and directories; reports truncation.",true,false,map[string]any{"path":p()}),
        tool("search_files","Find literal text with file paths, line numbers and nearby lines.",true,false,map[string]any{"path":p(),"query":property("string","Literal search text"),"context_lines":property("integer","Nearby lines, 0–3")},"query"),
        tool("read_file","Read a UTF-8 file or line range; returns the version required for edits.",true,false,map[string]any{"path":p(),"start_line":property("integer","First line, starting at 1"),"end_line":property("integer","Last line, inclusive")},"path"),
        tool("create_file","Create a new UTF-8 file; refuses to overwrite.",false,false,map[string]any{"path":p(),"content":property("string","Complete new file content")},"path","content"),
        tool("edit_file","Replace file content only if its read_file version still matches.",false,true,map[string]any{"path":p(),"content":property("string","Complete replacement file content"),"version":property("string","Version returned by read_file")},"path","content","version"),
        tool("create_directory","Create one new directory under the project.",false,false,map[string]any{"path":p()},"path"),
        tool("delete_path","Delete one file or empty directory. Recursive deletion must be explicitly enabled.",false,true,map[string]any{"path":p(),"recursive":property("boolean","Explicit recursive deletion")},"path"),
        tool("run_command","Run a noninteractive command in the isolated project. No network or host home directories.",false,true,map[string]any{"command":property("string","Shell command"),"workdir":p(),"timeout_seconds":property("integer","Timeout, 1–120 seconds; default 120")},"command"),
    }
}

func validRelative(value string)bool{
    if value==""||value=="."{return true}
    if len(value)>4096||path.IsAbs(value)||strings.ContainsAny(value,"\\\x00\r\n"){return false}
    for _,part:=range strings.Split(value,"/"){if part==""||part=="."||part==".."{return false}}
    return true
}

func (s *Server) acquire(appID int64)bool{
    s.mu.Lock();defer s.mu.Unlock()
    if len(s.active)>=4||s.active[appID]{return false}
    s.active[appID]=true
    return true
}
func (s *Server) release(appID int64){s.mu.Lock();defer s.mu.Unlock();delete(s.active,appID)}

func toolResult(w http.ResponseWriter,id json.RawMessage,value any,failed bool){
    data,err:=json.Marshal(value)
    if err!=nil{rpcError(w,id,-32603,"cannot encode result");return}
    rpcResult(w,id,map[string]any{"content":[]any{map[string]any{"type":"text","text":string(data)}},"structuredContent":value,"isError":failed})
}
func toolFailure(w http.ResponseWriter,id json.RawMessage,message string){toolResult(w,id,map[string]any{"error":message},true)}

func (s *Server) call(w http.ResponseWriter,r *http.Request,req rpcRequest,c Connection,token string){
    var params struct{Name string `json:"name"`;Arguments json.RawMessage `json:"arguments"`}
    if err:=json.Unmarshal(req.Params,&params);err!=nil{rpcError(w,req.ID,-32602,"invalid tool call");return}
    if len(params.Arguments)==0{params.Arguments=json.RawMessage("{}")}
    allowed:=false
    var spec map[string]any
    for _,item:=range tools(){v:=item.(map[string]any);if v["name"]==params.Name{allowed=true;spec=v;break}}
    if !allowed{rpcError(w,req.ID,-32602,"unknown tool");return}
    // Reject unknown parameters, particularly app IDs, users and roots.
    var args map[string]json.RawMessage
    if err:=json.Unmarshal(params.Arguments,&args);err!=nil||args==nil{rpcError(w,req.ID,-32602,"invalid tool arguments");return}
    schema:=spec["inputSchema"].(map[string]any)
    props:=schema["properties"].(map[string]any)
    for key,value:=range args{
        prop,ok:=props[key];if !ok{rpcError(w,req.ID,-32602,"unknown tool argument");return}
        var decoded any
        if err:=json.Unmarshal(value,&decoded);err!=nil||decoded==nil{rpcError(w,req.ID,-32602,"invalid tool argument");return}
        switch prop.(map[string]any)["type"]{
        case "string":if _,ok:=decoded.(string);!ok{rpcError(w,req.ID,-32602,"expected string");return}
        case "integer":var n int;if err:=json.Unmarshal(value,&n);err!=nil{rpcError(w,req.ID,-32602,"expected integer");return}
        case "boolean":if _,ok:=decoded.(bool);!ok{rpcError(w,req.ID,-32602,"expected boolean");return}
        }
    }
    for _,key:=range schema["required"].([]string){if _,ok:=args[key];!ok{rpcError(w,req.ID,-32602,"missing required argument");return}}
    var relative,command string
    var timeout int
    if params.Name=="run_command"{
        _=json.Unmarshal(args["command"],&command)
        _=json.Unmarshal(args["workdir"],&relative)
        _=json.Unmarshal(args["timeout_seconds"],&timeout)
        if command==""||len(command)>16384||timeout<0||timeout>120{toolFailure(w,req.ID,"invalid command or timeout");return}
    }else{_=json.Unmarshal(args["path"],&relative)}
    if !validRelative(relative){toolFailure(w,req.ID,"path must remain within the project");return}
    if !s.acquire(c.AppID){toolFailure(w,req.ID,"project busy; retry when the current operation completes");return}
    defer s.release(c.AppID)
    current,err:=s.grants.Authenticate(r.Context(),c.ID,c.Resource,token)
    if err!=nil{toolFailure(w,req.ID,"authorization no longer valid");return}
    started:=time.Now()
    ctx:=r.Context()
    if timeout>0{var cancel context.CancelFunc;ctx,cancel=context.WithTimeout(ctx,time.Duration(timeout)*time.Second);defer cancel()}
    workdir:=""
    if params.Name=="run_command"{workdir=relative}
    var input io.Reader=bytes.NewReader(params.Arguments)
    if params.Name=="run_command"{input=nil}
    result,err:=s.run(ctx,current.Owner,current.Root,workdir,params.Name,command,input)
    failed:=err!=nil||result.ExitCode!=0||result.TimedOut
    if params.Name!="run_command"&&result.Truncated{failed=true}
    auditCtx,cancelAudit:=context.WithTimeout(context.Background(),3*time.Second)
    defer cancelAudit()
    status:="success";if failed{status="failed"}
    // Never store command contents, file contents, tokens or command output.
    detail:=fmt.Sprintf("path=%q duration_ms=%d result=%s exit_code=%d timeout=%t",relative,time.Since(started).Milliseconds(),status,result.ExitCode,result.TimedOut)
    s.state.Audit(auditCtx,"mcp."+params.Name,fmt.Sprintf("app:%d",c.AppID),detail)
    if err!=nil{toolFailure(w,req.ID,err.Error());return}
    if params.Name=="run_command"{
        if !failed{_=s.grants.RecordSuccess(auditCtx,current)}
        toolResult(w,req.ID,result,failed);return
    }
    if failed{toolFailure(w,req.ID,strings.TrimSpace(result.Output));return}
    var value map[string]any
    if err:=json.Unmarshal([]byte(result.Output),&value);err!=nil||value==nil{toolFailure(w,req.ID,"invalid file helper response");return}
    _=s.grants.RecordSuccess(auditCtx,current)
    toolResult(w,req.ID,value,false)
}

// Origin validates a stored or administratively configured domain.
func Origin(domain string)(string,error){
    u,err:=url.Parse("https://"+domain)
    if err!=nil||u.Hostname()!=domain||u.Port()!=""||u.Path!=""||u.User!=nil||u.RawQuery!=""||u.Fragment!=""||!strings.Contains(domain,"."){
        return "",errors.New("invalid MCP domain")
    }
    return "https://"+domain,nil
}
