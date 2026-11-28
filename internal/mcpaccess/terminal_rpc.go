package mcpaccess

import (
    "encoding/json"
    "errors"
    "fmt"
    "net/http"
    "time"

    "github.com/creack/pty"
)

func terminalTool(name,description string,readOnly,destructive bool,props map[string]any,required ...string)map[string]any{
    t:=tool(name,description,readOnly,destructive,props,required...)
    t["annotations"].(map[string]any)["openWorldHint"]=true
    return t
}
func terminalTools()[]any{
    id:=func()map[string]any{return property("string","Terminal session ID returned by terminal_start or run_command")}
    return []any{
        terminalTool("terminal_start","Start a persistent host PTY shell or a single shell command under the configured Linux access level. Sessions survive MCP request disconnects.",false,true,map[string]any{"command":property("string","Optional command; omit for an interactive login shell"),"workdir":property("string","Absolute or project-relative working directory")}),
        terminalTool("terminal_write","Queue terminal input, including newlines and control characters. Input may contain passwords; never log it.",false,true,map[string]any{"session_id":id(),"input":property("string","Text or terminal control characters; include newline to submit a command"),"response_format":property("string","compact (default), text or legacy")},"session_id","input"),
        terminalTool("terminal_read","Read the next output fragment by byte offset. PTY combines stdout and stderr. Reuse next_offset; output is text, or output_base64 for binary bytes or explicit base64 encoding.",true,false,map[string]any{"session_id":id(),"offset":property("integer","Byte offset, initially 0"),"max_output_bytes":property("integer","Maximum bytes in this read, 1–65536; default 16384"),"output_encoding":property("string","auto (default) or base64"),"response_format":property("string","compact (default), text or legacy")},"session_id"),
        terminalTool("terminal_list","List terminal sessions owned by the current connection.",true,false,map[string]any{}),
        terminalTool("terminal_status","Return status, retained byte offsets and exit code if the session has ended.",true,false,map[string]any{"session_id":id(),"response_format":property("string","compact (default), text or legacy")},"session_id"),
        terminalTool("terminal_resize","Resize the interactive terminal.",false,false,map[string]any{"session_id":id(),"rows":property("integer","Terminal rows (2–65535)"),"cols":property("integer","Terminal columns (2–65535)")},"session_id","rows","cols"),
        terminalTool("terminal_stop","Stop the managed systemd unit and its process group.",false,true,map[string]any{"session_id":id()},"session_id"),
    }
}
func (s *Server) callTerminal(w http.ResponseWriter,r *http.Request,req rpcRequest,c Connection,token,name string,args map[string]json.RawMessage){
    current,err:=s.grants.Authenticate(r.Context(),c.ID,c.Resource,token)
    if err!=nil||current.TerminalPolicy!=c.TerminalPolicy{toolFailure(w,req.ID,"authorization no longer valid");return}
    m:=s.terminals
    text:=func(key string)string{var v string;_=json.Unmarshal(args[key],&v);return v}
    number:=func(key string)int{var v int;_=json.Unmarshal(args[key],&v);return v}
    format:=text("response_format")
    if format==""{format="compact"}
    if format!="compact"&&format!="text"&&format!="legacy"{toolFailure(w,req.ID,"invalid response_format");return}
    encoding:=text("output_encoding")
    if encoding==""{encoding="auto"}
    if encoding!="auto"&&encoding!="base64"{toolFailure(w,req.ID,"invalid output_encoding");return}
    limit:=number("max_output_bytes")
    if _,ok:=args["max_output_bytes"];ok&&(limit<1||limit>terminalChunk){toolFailure(w,req.ID,"max_output_bytes must be 1–65536");return}
    if limit==0{limit=16<<10;if format=="legacy"{limit=terminalChunk}}
    var result any
    if name=="terminal_start"||name=="run_command" {
        command:=text("command")
        if name=="run_command"&&command==""{toolFailure(w,req.ID,"command is required");return}
        wait:=number("timeout_seconds")
        if wait<0||wait>10{toolFailure(w,req.ID,"initial output wait must be 0–10 seconds; use terminal_read for later output");return}
        session,e:=m.start(r.Context(),current,token,command,text("workdir"))
        if e!=nil{toolFailure(w,req.ID,e.Error());return}
        if name=="run_command" {
            if wait==0{wait=2}
            select{case <-session.done:case <-time.After(time.Duration(wait)*time.Second):case <-r.Context().Done():}
            if err:=session.executionError();err!=nil{toolFailure(w,req.ID,err.Error());return}
            output:=session.read(0,limit,encoding,format)
            output["timed_out"]=false
            result=output
        }else{result=session.snapshot()}
    }else if name=="terminal_list"{
        list:=m.list(current.AppID,current.ID,current.ClientID,current.TerminalPolicy)
        result=map[string]any{"sessions":list}
    }else{
        session,e:=m.get(current,text("session_id"));if e!=nil{toolFailure(w,req.ID,e.Error());return}
        switch name{
        case "terminal_status":
            if err:=session.executionError();err!=nil{toolFailure(w,req.ID,err.Error());return}
            result=session.metadata()
        case "terminal_read":
            if err:=session.executionError();err!=nil{toolFailure(w,req.ID,err.Error());return}
            offset:=number("offset");if offset<0{toolFailure(w,req.ID,"invalid offset");return}
            result=session.read(int64(offset),limit,encoding,format)
        case "terminal_write":
            input:=[]byte(text("input"))
            if len(input)>terminalChunk{toolFailure(w,req.ID,"split terminal input into fragments of at most 64 KiB");return}
            if session.snapshot().Status=="exited"{toolFailure(w,req.ID,"terminal has exited");return}
            select{
            case <-session.done:toolFailure(w,req.ID,"terminal has exited");return
            case session.input<-input:
                m.audit(session,"input",fmt.Sprintf("bytes=%d (content omitted)",len(input)))
                meta:=session.metadata();meta["queued_bytes"]=len(input);result=meta
            default:toolFailure(w,req.ID,"terminal input queue busy; retry");return
            }
        case "terminal_resize":
            rows,cols:=number("rows"),number("cols")
            if rows<2||rows>65535||cols<2||cols>65535{toolFailure(w,req.ID,"invalid terminal size");return}
            if err=pty.Setsize(session.tty,&pty.Winsize{Rows:uint16(rows),Cols:uint16(cols)});err==nil{result=session.snapshot()}
        case "terminal_stop":
            err=m.stop(session)
            if err==nil{result=session.snapshot()}
        default:err=errors.New("unknown terminal tool")
        }
        if err!=nil{toolFailure(w,req.ID,err.Error());return}
    }
    _=s.grants.RecordSuccess(r.Context(),current)
    if name=="run_command"||name=="terminal_read"||name=="terminal_status"||name=="terminal_write"{
        terminalResult(w,req.ID,result,format)
        return
    }
    toolResult(w,req.ID,result,false)
}


// MCP requires a content array. Compact clients receive the full result only
// in structuredContent; text-only clients can explicitly request JSON text.
// Legacy restores the v0.1.58 wire format for existing integrations on demand.
func terminalResult(w http.ResponseWriter,id json.RawMessage,value any,format string){
    switch format{
    case "legacy":
        toolResult(w,id,value,false)
    case "text":
        data,err:=json.Marshal(value);if err!=nil{rpcError(w,id,-32603,"cannot encode result");return}
        rpcResult(w,id,map[string]any{"content":[]any{map[string]any{"type":"text","text":string(data)}},"isError":false})
    default:
        rpcResult(w,id,map[string]any{"content":[]any{},"structuredContent":value,"isError":false})
    }
}
