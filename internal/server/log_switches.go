package server

import (
 "context"
 "fmt"
 "html"
 "net/http"
 "strconv"
 "strings"
 "time"

 panelapp "github.com/bau59/open-go-panel/internal/app"
)

func registerLogSwitches(mux *http.ServeMux,store *sessionStore,cfg Config){
 mux.Handle("POST /log-retention/caddy/toggle",requireAuth(store,http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  if err:=r.ParseForm();err!=nil{http.Error(w,"Invalid form",400);return}
  ctx,cancel:=context.WithTimeout(r.Context(),20*time.Second);defer cancel()
  if err:=cfg.Caddy.SetAccessLogging(ctx,r.FormValue("enabled")=="1");err!=nil{
   http.Error(w,err.Error(),400);return
  }
  http.Redirect(w,r,"/log-retention",http.StatusSeeOther)
 })))
 mux.Handle("POST /log-retention/app/{id}/toggle",requireAuth(store,http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  id,err:=strconv.ParseInt(r.PathValue("id"),10,64)
  if err!=nil||id<=0{http.Error(w,"Invalid application",400);return}
  if err:=r.ParseForm();err!=nil{http.Error(w,"Invalid form",400);return}
  item,err:=cfg.Apps.Get(id)
  if err!=nil{http.NotFound(w,r);return}
  if item.Type=="static"||item.Service.Mode=="raw"{
   http.Error(w,"Application does not have managed journal output",400);return
  }
  item.Service.LogDisabled=r.FormValue("enabled")!="1"
  ctx,cancel:=context.WithTimeout(r.Context(),20*time.Second);defer cancel()
  if err:=cfg.Apps.SetServiceConfig(ctx,id,item.Service);err!=nil{
   http.Error(w,err.Error(),400);return
  }
  if r.FormValue("restart_now")=="1"{
   if err:=cfg.Apps.Restart(ctx,id);err!=nil{
    http.Error(w,"Setting saved; restart failed: "+err.Error(),500);return
   }
  }
  http.Redirect(w,r,"/log-retention",http.StatusSeeOther)
 })))
 mux.Handle("POST /log-retention/database/{engine}/toggle",requireAuth(store,http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  engine:=r.PathValue("engine")
  if engine!="mysql"&&engine!="postgres"&&engine!="redis"{http.NotFound(w,r);return}
  if err:=r.ParseForm();err!=nil{http.Error(w,"Invalid form",400);return}
  ctx,cancel:=context.WithTimeout(r.Context(),12*time.Second);defer cancel()
  if err:=cfg.Databases.SetOptionalLogging(ctx,engine,r.FormValue("enabled")=="1");err!=nil{
   http.Error(w,err.Error(),400);return
  }
  http.Redirect(w,r,"/log-retention",http.StatusSeeOther)
 })))
 mux.Handle("POST /log-retention/audit/toggle",requireAuth(store,http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  if err:=r.ParseForm();err!=nil{http.Error(w,"Invalid form",400);return}
  enabled:="0";if r.FormValue("enabled")=="1"{enabled="1"}
  if err:=cfg.State.SetSetting("logs.audit_enabled",enabled);err!=nil{
   http.Error(w,err.Error(),500);return
  }
  http.Redirect(w,r,"/log-retention",http.StatusSeeOther)
 })))
 mux.Handle("POST /log-retention/optional/disable",requireAuth(store,http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  if err:=r.ParseForm();err!=nil{http.Error(w,"Invalid form",400);return}
  if r.FormValue("confirm")!="DISABLE"{http.Error(w,"Explicit confirmation required",400);return}
  ctx,cancel:=context.WithTimeout(r.Context(),75*time.Second);defer cancel()
  var problems []string
  if err:=cfg.Caddy.SetAccessLogging(ctx,false);err!=nil{problems=append(problems,"Caddy: "+err.Error())}
  if apps,err:=cfg.Apps.List();err!=nil{problems=append(problems,"Apps: "+err.Error())}else{
   for _,item:=range apps {
    if ctx.Err()!=nil{problems=append(problems,"Timeout while updating apps");break}
    if item.Type=="static"||item.Service.Mode=="raw"||item.Service.LogDisabled{continue}
    item.Service.LogDisabled=true
    if err:=cfg.Apps.SetServiceConfig(ctx,item.ID,item.Service);err!=nil{
     problems=append(problems,fmt.Sprintf("%s: %v",item.Name,err))
    }
   }
  }
  states:=cfg.Databases.OptionalLoggingState(ctx)
  for engine,value:=range map[string]string{"mysql":states.MySQL,"postgres":states.Postgres,"redis":states.Redis}{
   if value=="Unavailable"{continue}
   if err:=cfg.Databases.SetOptionalLogging(ctx,engine,false);err!=nil{
    problems=append(problems,engine+": "+err.Error())
   }
  }
  if err:=cfg.State.SetSetting("logs.audit_enabled","0");err!=nil{
   problems=append(problems,"Audit: "+err.Error())
  }
  if len(problems)>0{
   http.Error(w,"Optional logs partially disabled: "+strings.Join(problems,"; "),500);return
  }
  http.Redirect(w,r,"/log-retention",http.StatusSeeOther)
 })))
}

func currentAuditLogging(cfg Config) bool {
 if cfg.State==nil{return true}
 value,ok,err:=cfg.State.Setting("logs.audit_enabled")
 return err!=nil||!ok||value!="0"
}

func logSwitchesPanel(ctx context.Context,cfg Config)string{
 settings,caddyErr:=cfg.Caddy.GlobalSettings()
 caddyState:="Unavailable"
 if caddyErr==nil {if settings.AccessLog{caddyState="Enabled"}else{caddyState="Disabled"}}
 states:=struct{MySQL,Postgres,Redis string}{"Unavailable","Unavailable","Unavailable"}
 if cfg.Databases!=nil{
  limited,cancel:=context.WithTimeout(ctx,5*time.Second)
  defer cancel()
  current:=cfg.Databases.OptionalLoggingState(limited)
  states.MySQL,states.Postgres,states.Redis=current.MySQL,current.Postgres,current.Redis
 }
 auditState:="Enabled";if !currentAuditLogging(cfg){auditState="Disabled"}
 choose:=func(enabled bool)string{
  if enabled{return `<option value="1" selected>Enabled</option><option value="0">Disabled</option>`}
  return `<option value="1">Enabled</option><option value="0" selected>Disabled</option>`
 }
 items:=[]struct{title,desc,path,state string}{
  {"Caddy access logs","Per-request logs of managed sites. Error logs and custom overrides remain.","/log-retention/caddy/toggle",caddyState},
  {"Panel audit","Admin mutation audit entries. Login/security events may remain in system logs.","/log-retention/audit/toggle",auditState},
  {"MySQL slow queries","Global slow-query logging, not MySQL error logs.","/log-retention/database/mysql/toggle",states.MySQL},
  {"PostgreSQL slow queries","Global duration setting; per-database overrides may remain.","/log-retention/database/postgres/toggle",states.Postgres},
  {"Redis slow commands","Redis SLOWLOG capture only; server errors remain.","/log-retention/database/redis/toggle",states.Redis},
 }
 var out strings.Builder
 out.WriteString(`<section class="panel panel-pad" style="margin-bottom:16px"><h2>Enable or disable log sources</h2>
 <p class="note">Turning a source off stops future optional records. Existing history remains until retention removes it. Essential system/security messages are not disabled.</p>
 <div class="grid" style="grid-template-columns:repeat(auto-fit,minmax(min(100%,260px),1fr));gap:12px;margin-top:16px">`)
 for _,item:=range items{
  fmt.Fprintf(&out,`<div class="metric"><span>%s</span><strong>%s</strong><small>%s</small>
  <form method="post" action="%s" class="compact-form" style="margin-top:12px">
  <select name="enabled" aria-label="Enable %s">%s</select><button class="secondary">Save</button>
  </form></div>`,html.EscapeString(item.title),html.EscapeString(item.state),html.EscapeString(item.desc),
  item.path,html.EscapeString(item.title),choose(item.state=="Enabled"||item.state=="Enabled (global)"))
 }
 out.WriteString(`</div><div style="margin-top:16px;padding-top:14px;border-top:1px solid var(--border)">
 <h3>Disable all optional logging</h3><p class="note">Disables managed Caddy access, managed app stdout/stderr (effective at their next restart), available DB slow logs, Redis SLOWLOG and audit. This can partially succeed; system and error logs remain active. Existing records are not erased.</p>
 <form method="post" action="/log-retention/optional/disable" onsubmit="return confirm('Disable all optional logging? App changes become effective after restart.');">
 <input type="hidden" name="confirm" value="DISABLE"><button class="danger">Disable optional logging</button></form></div></section>`)
 return out.String()
}

func appLogToggleForm(app panelapp.App)string{
 choices:=`<option value="1" selected>Enabled</option><option value="0">Disabled</option>`
 if app.Service.LogDisabled{choices=`<option value="1">Enabled</option><option value="0" selected>Disabled</option>`}
 return fmt.Sprintf(`<form method="post" action="/log-retention/app/%d/toggle" class="compact-form">
 <select name="enabled" aria-label="Application output logging">%s</select>
 <label class="note"><input name="restart_now" type="checkbox" value="1" style="width:auto;height:auto"> Restart now</label>
 <button class="secondary">Save</button></form>`,app.ID,choices)
}
