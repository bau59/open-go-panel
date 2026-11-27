package server

import (
    "context"
    "errors"
    "fmt"
    "html"
    "io"
    "net/http"
    "net/url"
    "os/exec"
    "strconv"
    "strings"
    "sync"
    "time"

    panelapp "github.com/bau59/open-go-panel/internal/app"
    "github.com/bau59/open-go-panel/internal/mcpaccess"
)

var mcpSettingsMu sync.Mutex

func registerMCPRoutes(mux *http.ServeMux,sessions *sessionStore,cfg Config){
    grants:=mcpaccess.New(cfg.State)
    mux.Handle("GET /settings/chatgpt",requireAuth(sessions,http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
        writeHTML(w,cfg.Logger,200,mcpSettingsPage(r.Context(),cfg,r.URL.Query().Get("message")))
    })))
    mux.Handle("POST /settings/chatgpt",requireAuth(sessions,http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
        if cfg.Caddy==nil{http.Error(w,"Caddy unavailable",503);return}
        if err:=r.ParseForm();err!=nil{http.Error(w,"invalid form",400);return}
        mcpSettingsMu.Lock();defer mcpSettingsMu.Unlock()
        err:=cfg.Caddy.SetMCPDomain(r.Context(),r.FormValue("domain"))
        message:="MCP domain saved. Ensure DNS points to this VPS."
        if err!=nil{message=err.Error()}else{cfg.State.Audit(r.Context(),"mcp.domain","server","domain configured")}
        http.Redirect(w,r,"/settings/chatgpt?message="+url.QueryEscape(message),303)
    })))
    mux.Handle("POST /settings/chatgpt/check",requireAuth(sessions,http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
        domain,_,err:=cfg.State.Setting(mcpaccess.DomainSetting)
        message:="HTTPS connection verified through Caddy."
        if err==nil{
            var origin string
            origin,err=mcpaccess.Origin(domain)
            if err==nil{
                ctx,cancel:=context.WithTimeout(r.Context(),10*time.Second);defer cancel()
                req,e:=http.NewRequestWithContext(ctx,"GET",origin+"/health",nil)
                if e!=nil{err=e}else{
                    client:=&http.Client{Timeout:10*time.Second,CheckRedirect:func(*http.Request,[]*http.Request)error{return http.ErrUseLastResponse}}
                    res,e:=client.Do(req)
                    if e!=nil{err=errors.New("HTTPS health request failed; check DNS, certificate and Caddy")}else{
                        data,e:=io.ReadAll(io.LimitReader(res.Body,16));res.Body.Close()
                        if e!=nil||res.StatusCode!=200||strings.TrimSpace(string(data))!="ok"{err=errors.New("MCP HTTPS health check failed")}
                    }
                }
            }
        }
        if err!=nil{message=err.Error()}
        http.Redirect(w,r,"/settings/chatgpt?message="+url.QueryEscape(message),303)
    })))
    post:=func(suffix string,action func(*http.Request,int64)error){
        mux.Handle("POST /apps/{id}/chatgpt/"+suffix,requireAuth(sessions,http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
            id,err:=strconv.ParseInt(r.PathValue("id"),10,64)
            if err!=nil||id<=0{http.Error(w,"invalid application",400);return}
            if _,err:=cfg.Apps.Get(id);err!=nil{http.NotFound(w,r);return}
            if err:=r.ParseForm();err!=nil{http.Error(w,"invalid form",400);return}
            mcpSettingsMu.Lock();defer mcpSettingsMu.Unlock()
            if err:=action(r,id);err!=nil{writeHTML(w,cfg.Logger,400,pageHead("ChatGPT access")+"<body>"+appHeader("apps")+`<main class="shell"><section class="panel panel-pad"><p class="alert">`+html.EscapeString(err.Error())+`</p><a class="secondary" href="/apps/`+fmt.Sprint(id)+`">Back to application</a></section></main></body></html>`);return}
            http.Redirect(w,r,fmt.Sprintf("/apps/%d#chatgpt-access",id),303)
        })))
    }
    post("enable",func(r *http.Request,id int64)error{
        domain,_,err:=cfg.State.Setting(mcpaccess.DomainSetting);if err!=nil{return err}
        origin,err:=mcpaccess.Origin(domain);if err!=nil{return errors.New("configure ChatGPT access under Infrastructure first")}
        if _,err:=grants.Enable(r.Context(),id,origin);err!=nil{return err}
        cfg.State.Audit(r.Context(),"mcp.enable",fmt.Sprintf("app:%d",id),"awaiting authorization")
        return nil
    })
    post("terminal-mode",func(r *http.Request,id int64)error{
        mode:=r.FormValue("mode")
        if (mode=="root"||mode=="sudo")&&r.FormValue("confirm")!="1"{return errors.New("confirm elevated terminal access")}
        previous,err:=grants.SetTerminalMode(r.Context(),id,mode);if err!=nil{return err}
        cfg.State.Audit(r.Context(),"mcp.terminal.mode",fmt.Sprintf("app:%d",id),"mode="+mode+"; old OAuth tokens invalidated")
        if err:=mcpaccess.AdminTerminalRevoke(r.Context(),previous);err!=nil{return fmt.Errorf("tokens invalidated; terminal stop not confirmed: %w",err)}
        return nil
    })
    post("terminal-stop",func(r *http.Request,id int64)error{
        return mcpaccess.AdminTerminalStop(r.Context(),id,r.FormValue("session_id"))
    })
    post("revoke",func(r *http.Request,id int64)error{
        c,err:=grants.Connection(r.Context(),id);if err!=nil{return err}
        if err:=grants.Revoke(r.Context(),id);err!=nil{return err}
        cfg.State.Audit(r.Context(),"mcp.revoke",fmt.Sprintf("app:%d",id),"access and refresh tokens revoked")
        if err:=mcpaccess.AdminTerminalRevoke(r.Context(),c);err!=nil{return fmt.Errorf("tokens revoked; terminal stop not confirmed: %w",err)}
        return nil
    })
    post("decide",func(r *http.Request,id int64)error{
        decision:=r.FormValue("decision")
        if decision!="approve"&&decision!="deny"{return errors.New("invalid authorization decision")}
        if err:=grants.Decide(r.Context(),id,r.FormValue("request_id"),decision=="approve");err!=nil{return err}
        cfg.State.Audit(r.Context(),"mcp."+decision,fmt.Sprintf("app:%d",id),"administrator decision")
        return nil
    })
}

func mcpSettingsPage(ctx context.Context,cfg Config,message string)string{
    domain,_,err:=cfg.State.Setting(mcpaccess.DomainSetting)
    if err!=nil{message=err.Error()}
    status:="Inactive"
    checkCtx,cancel:=context.WithTimeout(ctx,2*time.Second);defer cancel()
    if exec.CommandContext(checkCtx,"systemctl","is-active","--quiet","open-go-panel-mcp.service").Run()==nil{status="Running"}
    var out strings.Builder
    out.WriteString(pageHead("ChatGPT access")+"<body>"+appHeader("chatgpt")+`<main class="shell"><div class="page-head"><div><h1>ChatGPT access</h1><p class="sub">One MCP service for this server, independent authorization for each application.</p></div></div>`)
    if message!=""{fmt.Fprintf(&out,`<p class="note">%s</p>`,html.EscapeString(message))}
    fmt.Fprintf(&out,`<section class="panel panel-pad"><h2>Connection settings</h2><p>Service: <strong>%s</strong></p><p class="note">Local listener: %s. Only OAuth, MCP and health routes are exposed via Caddy. The administrative interface remains private.</p><form method="post" action="/settings/chatgpt"><label>Dedicated HTTPS domain<input name="domain" placeholder="mcp.example.com" value="%s"></label><div class="actions"><button class="button">Save domain</button></div></form><form method="post" action="/settings/chatgpt/check"><button class="secondary">Test HTTPS connection</button></form><p class="note">Create a DNS A/AAAA record for the domain before enabling an application. Revoke existing grants before changing the domain.</p></section>`,status,mcpaccess.ListenAddr,html.EscapeString(domain))
    out.WriteString("</main></body></html>")
    return out.String()
}

func mcpAppBlock(ctx context.Context,cfg Config,a panelapp.App)string{
    if cfg.State==nil{return ""}
    grants:=mcpaccess.New(cfg.State)
    c,err:=grants.Connection(ctx,a.ID)
    var out strings.Builder
    fmt.Fprintf(&out,`<section id="chatgpt-access" class="panel panel-pad app-card-wide mcp-access-card" data-panel-tab="chatgpt"><div class="section-title"><div><h2>ChatGPT access</h2><p class="note">One plugin per application. File operations run as %s, scoped to <code>%s</code>. Terminal commands use host permissions.</p></div></div>`,html.EscapeString(a.User),html.EscapeString(a.Root))
    if errors.Is(err,mcpaccess.ErrUnauthorized)||err==nil&&c.RevokedAt!=0{
        label:="Disabled"
        if err==nil{label="Revoked "+time.Unix(c.RevokedAt,0).UTC().Format("2006-01-02 15:04 UTC")}
        fmt.Fprintf(&out,`<p><strong>%s</strong></p><form method="post" action="/apps/%d/chatgpt/enable"><button class="button">Enable connection</button></form><p class="note"><a href="/settings/chatgpt">Server MCP settings</a></p>`,html.EscapeString(label),a.ID)
        // A failed revoke must not hide sessions that still need to be stopped.
        if err==nil { out.WriteString(mcpTerminalSessions(ctx,a.ID)) }
        out.WriteString("</section>")
        return out.String()
    }
    if err!=nil{out.WriteString(`<p class="alert">Unable to load connection status.</p></section>`);return out.String()}
    label:="Awaiting authorization"
    if c.AuthorizedAt>0{label="Connected "+time.Unix(c.AuthorizedAt,0).UTC().Format("2006-01-02 15:04 UTC");if active,err:=grants.HasRefresh(ctx,c.ID);err!=nil{label="Authorization status unavailable"}else if !active{label="Authorization expired — reconnect in ChatGPT"}}
    pending,err:=grants.Pending(ctx,a.ID)
    if err!=nil{out.WriteString(`<p class="alert">Unable to load pending requests.</p>`)}else if len(pending)>0{label="Authorization request received"}
    fmt.Fprintf(&out,`<p><strong>%s</strong></p><div class="mcp-copy-fields"><div><label for="mcp-plugin-name">Plugin name</label><div class="mcp-copy-row"><input id="mcp-plugin-name" readonly value="%s" onclick="this.select()"><button type="button" class="secondary" onclick="navigator.clipboard.writeText(document.getElementById('mcp-plugin-name').value)">Copy name</button></div></div><div><label for="mcp-plugin-url">MCP URL</label><div class="mcp-copy-row"><input id="mcp-plugin-url" readonly value="%s" onclick="this.select()"><button type="button" class="secondary" onclick="navigator.clipboard.writeText(document.getElementById('mcp-plugin-url').value)">Copy URL</button></div></div></div><p class="note">In ChatGPT on the web: Plugins → + → Add custom MCP server. Use this URL, choose OAuth with dynamic registration, create and install the plugin. Approve only the request ID shown in the OAuth browser.</p>`,html.EscapeString(label),html.EscapeString("Open Go Panel — "+strings.ToUpper(a.Name)),html.EscapeString(c.Resource))
    mode:=c.TerminalMode()
    selected:=func(value string)string{if mode==value{return " selected"};return ""}
    fmt.Fprintf(&out,`<form method="post" action="/apps/%d/chatgpt/terminal-mode" class="mcp-network-form" onsubmit="return confirm('Changing terminal access revokes all current tokens and stops existing terminal sessions. Continue?')"><h3>Terminal access</h3><label>Access level<select name="mode"><option value="user"%s>Project user</option><option value="sudo"%s>Project user with system sudo</option><option value="root"%s>Server administrator (root)</option></select></label><p class="note">Terminal commands have normal host filesystem and network access. User and sudo modes run as the project owner; sudo is governed by existing sudoers policy and is not granted automatically. Root provides unrestricted server administration. Only file tools remain project-scoped. Changing mode requires a new OAuth authorization.</p><label><input type="checkbox" name="confirm" value="1"> I authorize elevated access if selected</label><button class="secondary">Change level and revoke tokens</button></form>`,a.ID,selected("user"),selected("sudo"),selected("root"))
    out.WriteString(mcpTerminalSessions(ctx,a.ID))
    if c.LastSuccessAt>0{fmt.Fprintf(&out,`<p class="note">Last successful call: %s</p>`,time.Unix(c.LastSuccessAt,0).UTC().Format("2006-01-02 15:04:05 UTC"))}
    for _,req:=range pending{
        fmt.Fprintf(&out,`<div class="snapshot-fact"><strong>Pending authorization</strong><code>%s</code><span>Expires %s. Permissions: read/write project files and host terminal (%s).</span><form method="post" action="/apps/%d/chatgpt/decide"><input type="hidden" name="request_id" value="%s"><div class="actions"><button class="button" name="decision" value="approve">Approve</button><button class="danger" name="decision" value="deny">Deny</button></div></form></div>`,html.EscapeString(req.ID),time.Unix(req.ExpiresAt,0).UTC().Format("15:04 UTC"),html.EscapeString(mode),a.ID,html.EscapeString(req.ID))
    }
    fmt.Fprintf(&out,`<div class="actions"><a class="secondary" href="/apps/%d#chatgpt-access">Refresh status</a><form method="post" action="/apps/%d/chatgpt/revoke" onsubmit="return confirm('Revoke all ChatGPT access for this application?')"><button class="danger">Revoke access</button></form></div></section>`,a.ID,a.ID)
    return out.String()
}


func mcpTerminalSessions(ctx context.Context,appID int64)string{
    var out strings.Builder
    out.WriteString(`<div class="mcp-network-form"><h3>Terminal sessions</h3><p class="note">Sessions survive individual MCP requests. Stopping a session ends its managed process group.</p>`)
    query,cancel:=context.WithTimeout(ctx,2*time.Second);defer cancel()
    sessions,err:=mcpaccess.AdminTerminalList(query,appID)
    if err!=nil{return out.String()+`<p class="note">Terminal session status unavailable. Check the MCP service.</p></div>`}
    if len(sessions)==0{return out.String()+`<p class="note">No terminal sessions.</p></div>`}
    out.WriteString(`<div class="table-scroll"><table data-no-pager="1"><thead><tr><th>Session</th><th>User / level</th><th>Started (UTC)</th><th>Status</th><th>Action</th></tr></thead><tbody>`)
    for _,s:=range sessions{
        status:=s.Status
        if s.ExitCode!=nil{status+=fmt.Sprintf(" (%d)",*s.ExitCode)}
        fmt.Fprintf(&out,`<tr><td><code title="%s">%s</code></td><td>%s / %s</td><td>%s</td><td>%s</td><td>`,html.EscapeString(s.ID),html.EscapeString(s.ID[:12]),html.EscapeString(s.User),html.EscapeString(s.Mode),s.StartedAt.UTC().Format("2006-01-02 15:04"),html.EscapeString(status))
        if s.Status!="exited"{fmt.Fprintf(&out,`<form method="post" action="/apps/%d/chatgpt/terminal-stop" onsubmit="return confirm('Stop this terminal and its managed processes?')"><input type="hidden" name="session_id" value="%s"><button class="danger compact-action">Stop</button></form>`,appID,html.EscapeString(s.ID))}
        out.WriteString("</td></tr>")
    }
    out.WriteString("</tbody></table></div></div>")
    return out.String()
}
