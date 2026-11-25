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
    post("network",func(r *http.Request,id int64)error{
        mysql,redis:=r.FormValue("mysql")=="1",r.FormValue("redis")=="1"
        if err:=grants.SetNetworkAccess(r.Context(),id,mysql,redis);err!=nil{return err}
        cfg.State.Audit(r.Context(),"mcp.network",fmt.Sprintf("app:%d",id),fmt.Sprintf("mysql=%t redis=%t",mysql,redis))
        return nil
    })
    post("revoke",func(r *http.Request,id int64)error{
        if err:=grants.Revoke(r.Context(),id);err!=nil{return err}
        cfg.State.Audit(r.Context(),"mcp.revoke",fmt.Sprintf("app:%d",id),"access and refresh tokens revoked")
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
    fmt.Fprintf(&out,`<section id="chatgpt-access" class="panel panel-pad app-card-wide"><div class="section-title"><div><h2>ChatGPT access</h2><p class="note">One plugin, one application. Commands and file operations run as %s, scoped to <code>%s</code>.</p></div></div>`,html.EscapeString(a.User),html.EscapeString(a.Root))
    if errors.Is(err,mcpaccess.ErrUnauthorized)||err==nil&&c.RevokedAt!=0{
        label:="Disabled"
        if err==nil{label="Revoked "+time.Unix(c.RevokedAt,0).UTC().Format("2006-01-02 15:04 UTC")}
        fmt.Fprintf(&out,`<p><strong>%s</strong></p><form method="post" action="/apps/%d/chatgpt/enable"><button class="button">Enable connection</button></form><p class="note"><a href="/settings/chatgpt">Server MCP settings</a></p></section>`,html.EscapeString(label),a.ID)
        return out.String()
    }
    if err!=nil{out.WriteString(`<p class="alert">Unable to load connection status.</p></section>`);return out.String()}
    label:="Awaiting authorization"
    if c.AuthorizedAt>0{label="Connected "+time.Unix(c.AuthorizedAt,0).UTC().Format("2006-01-02 15:04 UTC");if active,err:=grants.HasRefresh(ctx,c.ID);err!=nil{label="Authorization status unavailable"}else if !active{label="Authorization expired — reconnect in ChatGPT"}}
    pending,err:=grants.Pending(ctx,a.ID)
    if err!=nil{out.WriteString(`<p class="alert">Unable to load pending requests.</p>`)}else if len(pending)>0{label="Authorization request received"}
    fmt.Fprintf(&out,`<p><strong>%s</strong></p><label>Plugin name<input id="mcp-plugin-name" readonly value="%s" onclick="this.select()"></label><button type="button" class="secondary" onclick="navigator.clipboard.writeText(document.getElementById('mcp-plugin-name').value)">Copy name</button><label>MCP URL<input id="mcp-plugin-url" readonly value="%s" onclick="this.select()"></label><button type="button" class="secondary" onclick="navigator.clipboard.writeText(document.getElementById('mcp-plugin-url').value)">Copy URL</button><p class="note">In ChatGPT on the web: Plugins → + → Add custom MCP server. Use this URL, choose OAuth with dynamic registration, create and install the plugin. Approve only the request ID shown in the OAuth browser.</p>`,html.EscapeString(label),html.EscapeString("Open Go Panel — "+strings.ToUpper(a.Name)),html.EscapeString(c.Resource))
    if access,err:=grants.NetworkAccess(c);err!=nil{
        out.WriteString(`<p class="alert">Unable to load database network permissions.</p>`)
    }else{
        mysqlChecked,redisChecked:="",""
        if access.MySQL{mysqlChecked=" checked"}
        if access.Redis{redisChecked=" checked"}
        fmt.Fprintf(&out,`<form method="post" action="/apps/%d/chatgpt/network"><h3>Database network access</h3><label><input type="checkbox" name="mysql" value="1"%s> MySQL — 127.0.0.1:3306</label><label><input type="checkbox" name="redis" value="1"%s> Redis — 127.0.0.1:6379</label><p class="note">MySQL uses normal database credentials. Redis is shared by applications; enabling it may grant access to other applications' keys unless Redis ACLs are configured. No other network endpoints are opened. Changes apply to the next command.</p><button class="secondary">Save database access</button></form>`,a.ID,mysqlChecked,redisChecked)
    }
    if c.LastSuccessAt>0{fmt.Fprintf(&out,`<p class="note">Last successful call: %s</p>`,time.Unix(c.LastSuccessAt,0).UTC().Format("2006-01-02 15:04:05 UTC"))}
    for _,req:=range pending{
        fmt.Fprintf(&out,`<div class="snapshot-fact"><strong>Pending authorization</strong><code>%s</code><span>Expires %s. Permissions: read/write project files and isolated commands.</span><form method="post" action="/apps/%d/chatgpt/decide"><input type="hidden" name="request_id" value="%s"><div class="actions"><button class="button" name="decision" value="approve">Approve</button><button class="danger" name="decision" value="deny">Deny</button></div></form></div>`,html.EscapeString(req.ID),time.Unix(req.ExpiresAt,0).UTC().Format("15:04 UTC"),a.ID,html.EscapeString(req.ID))
    }
    fmt.Fprintf(&out,`<div class="actions"><a class="secondary" href="/apps/%d#chatgpt-access">Refresh status</a><form method="post" action="/apps/%d/chatgpt/revoke" onsubmit="return confirm('Revoke all ChatGPT access for this application?')"><button class="danger">Revoke access</button></form></div></section>`,a.ID,a.ID)
    return out.String()
}
