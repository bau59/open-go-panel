package server

import (
 "fmt"
 "html"
 "net/http"
 "os"
 "os/exec"
 "path/filepath"
 "strconv"
 "strings"
 "context"
 "time"

 panelcaddy "github.com/bau59/open-go-panel/internal/caddy"
)

const journalPolicyFile="/etc/systemd/journald.conf.d/90-open-go-panel-retention.conf"

func readJournalPolicy() (sizeMB,days int,err error){
 sizeMB,days=256,7
 b,err:=os.ReadFile(journalPolicyFile)
 if os.IsNotExist(err){return sizeMB,days,nil}
 if err!=nil {return sizeMB,days,err}
 for _,line:=range strings.Split(string(b),"\n"){
  line=strings.TrimSpace(line)
  if strings.HasPrefix(line,"SystemMaxUse="){
   n,e:=strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(line,"SystemMaxUse="),"M"));if e==nil {sizeMB=n}
  }
  if strings.HasPrefix(line,"MaxRetentionSec="){
   n,e:=strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(line,"MaxRetentionSec="),"day"));if e==nil {days=n}
  }
 }
 return sizeMB,days,nil
}

func applyJournalPolicy(ctx context.Context,sizeMB,days int)error{
 validSize:=map[int]bool{128:true,256:true,512:true,1024:true,2048:true}
 validDays:=map[int]bool{1:true,3:true,7:true,14:true,30:true}
 if !validSize[sizeMB]||!validDays[days]{return fmt.Errorf("invalid journal retention limits")}
 if err:=os.MkdirAll(filepath.Dir(journalPolicyFile),0755);err!=nil{return err}
 config:=fmt.Sprintf("# Managed by Open Go Panel: shared across Caddy, systemd and application journals.\n[Journal]\nSystemMaxUse=%dM\nMaxRetentionSec=%dday\n",sizeMB,days)
 old,readErr:=os.ReadFile(journalPolicyFile)
 if readErr!=nil&&!os.IsNotExist(readErr){return readErr}
 tmp,err:=os.CreateTemp(filepath.Dir(journalPolicyFile),".ogp-journal-*.conf")
 if err!=nil{return err}
 defer os.Remove(tmp.Name())
 if _,err=tmp.WriteString(config);err!=nil{tmp.Close();return err}
 if err=tmp.Chmod(0644);err!=nil{tmp.Close();return err}
 if err=tmp.Close();err!=nil{return err}
 if err=os.Rename(tmp.Name(),journalPolicyFile);err!=nil{return err}
 if out,err:=exec.CommandContext(ctx,"systemctl","restart","systemd-journald.service").CombinedOutput();err!=nil{
  if readErr==nil{_ = os.WriteFile(journalPolicyFile,old,0644)}else{_ = os.Remove(journalPolicyFile)}
  return fmt.Errorf("restart journald: %w: %s",err,strings.TrimSpace(string(out)))
 }
 return nil
}

func registerLogRetentionRoutes(mux *http.ServeMux,store *sessionStore,cfg Config){
 mux.Handle("POST /log-retention/app/{id}",requireAuth(store,http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  id,err:=strconv.ParseInt(r.PathValue("id"),10,64)
  if err!=nil||id<=0{http.Error(w,"invalid application",400);return}
  if err:=r.ParseForm();err!=nil{http.Error(w,"invalid form",400);return}
  days,err:=strconv.Atoi(r.FormValue("days"))
  if err!=nil{http.Error(w,"invalid period",400);return}
  allowed:=map[int]bool{1:true,3:true,7:true,14:true,30:true,60:true,90:true}
  if !allowed[days]{http.Error(w,"unsupported log retention period",400);return}
  app,err:=cfg.Apps.Get(id)
  if err!=nil{http.NotFound(w,r);return}
  if app.Type=="static"||app.Service.Mode=="raw"{
   http.Error(w,"raw and static services cannot use managed journal namespace retention",400);return
  }
  app.Service.LogRetentionDays=days
  ctx,cancel:=context.WithTimeout(r.Context(),20*time.Second);defer cancel()
  if err:=cfg.Apps.SetServiceConfig(ctx,id,app.Service);err!=nil{
   http.Error(w,err.Error(),400);return
  }
  http.Redirect(w,r,"/log-retention",http.StatusSeeOther)
 })))

 mux.Handle("POST /log-retention/source",requireAuth(store,http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  if err:=r.ParseForm();err!=nil{http.Error(w,"invalid form",400);return}
  source:=r.FormValue("source")
  if source!="journal"&&source!="file"{http.Error(w,"invalid metrics source",400);return}
  path:=strings.TrimSpace(r.FormValue("file_path"))
  if source=="file"&&!panelcaddy.ValidCaddyLogPath(path){
   http.Error(w,"file source must be under /var/log/caddy/",400);return
  }
  if source=="file"{
   if err:=cfg.State.SetSetting("performance.file_path",path);err!=nil{http.Error(w,err.Error(),500);return}
  }
  if err:=cfg.State.SetSetting("performance.source",source);err!=nil{http.Error(w,err.Error(),500);return}
  http.Redirect(w,r,"/log-retention",http.StatusSeeOther)
 })))

 mux.Handle("POST /log-retention/performance",requireAuth(store,http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  if err:=r.ParseForm();err!=nil{http.Error(w,"invalid form",400);return}
  detail,e1:=strconv.Atoi(r.FormValue("detail_days"))
  aggregate,e2:=strconv.Atoi(r.FormValue("aggregate_days"))
  if e1!=nil||e2!=nil||detail<1||detail>30||aggregate<detail||aggregate>90{
   http.Error(w,"invalid HTTP metrics retention",400);return
  }
  if err:=cfg.State.SetSetting("performance.detail_days",strconv.Itoa(detail));err!=nil{
   http.Error(w,err.Error(),500);return
  }
  if err:=cfg.State.SetSetting("performance.aggregate_days",strconv.Itoa(aggregate));err!=nil{
   http.Error(w,err.Error(),500);return
  }
  ctx,cancel:=context.WithTimeout(r.Context(),15*time.Second);defer cancel()
  if err:=cfg.Caddy.PrunePerformance(ctx);err!=nil{http.Error(w,err.Error(),500);return}
  http.Redirect(w,r,"/log-retention",http.StatusSeeOther)
 })))

 mux.Handle("POST /log-retention/redis",requireAuth(store,http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  if err:=r.ParseForm();err!=nil{http.Error(w,"invalid form",400);return}
  n,err:=strconv.Atoi(r.FormValue("length"));if err!=nil{http.Error(w,"invalid SLOWLOG limit",400);return}
  ctx,cancel:=context.WithTimeout(r.Context(),8*time.Second);defer cancel()
  if err:=cfg.Databases.SetRedisSlowLogLength(ctx,n);err!=nil{http.Error(w,err.Error(),400);return}
  http.Redirect(w,r,"/log-retention",http.StatusSeeOther)
 })))

 mux.Handle("POST /log-retention/audit",requireAuth(store,http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  if err:=r.ParseForm();err!=nil{http.Error(w,"invalid form",400);return}
  days,err:=strconv.Atoi(r.FormValue("days"))
  if err!=nil || (days!=7 && days!=14 && days!=30 && days!=90 && days!=365){
   http.Error(w,"invalid audit retention period",400);return
  }
  if err:=cfg.State.SetSetting("logs.audit_retention_days",strconv.Itoa(days));err!=nil{
   http.Error(w,err.Error(),500);return
  }
  // Apply immediately, not just on the next audit write.
  if _,err:=cfg.State.DB().ExecContext(r.Context(),
   "DELETE FROM audit_log WHERE created_at < strftime('%Y-%m-%dT%H:%M:%fZ', 'now', ?)",
   fmt.Sprintf("-%d days",days));err!=nil{
   http.Error(w,err.Error(),500);return
  }
  http.Redirect(w,r,"/log-retention",http.StatusSeeOther)
 })))

 mux.Handle("GET /log-retention",requireAuth(store,http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  size,days,err:=readJournalPolicy()
  msg:=""
  if err!=nil{msg=err.Error()}
  auditDays:=30
  if raw,found,err:=cfg.State.Setting("logs.audit_retention_days");err==nil&&found{
   if n,e:=strconv.Atoi(raw);e==nil{auditDays=n}
  }
  redisLength,redisErr:=cfg.Databases.RedisSlowLogLength(r.Context())
  if redisErr!=nil && msg==""{msg="Redis SLOWLOG policy unavailable: "+redisErr.Error()}
  appRows:=""
  if apps,err:=cfg.Apps.List();err==nil{
   var b strings.Builder
   for _,app:=range apps{
    label:="Shared default (raw unit or disabled)"
    if app.Service.Mode!="raw" && app.Service.LogRetentionDays>0{label=fmt.Sprintf("%d days (dedicated namespace)",app.Service.LogRetentionDays)}
    fmt.Fprintf(&b,`<tr><td>%s</td><td>%s</td><td><a class="secondary" href="/apps/%d">Application settings</a></td></tr>`,html.EscapeString(app.Name),html.EscapeString(label),app.ID)
   }
   appRows=b.String()
  }
  detailDays,aggregateDays:=7,30
  if v,ok,_:=cfg.State.Setting("performance.detail_days");ok{if n,e:=strconv.Atoi(v);e==nil&&n>=1&&n<=30{detailDays=n}}
  if v,ok,_:=cfg.State.Setting("performance.aggregate_days");ok{if n,e:=strconv.Atoi(v);e==nil&&n>=detailDays&&n<=90{aggregateDays=n}}
  source,hasSource,_:=cfg.State.Setting("performance.source")
  if !hasSource||source!="file"{source="journal"}
  path,_,_:=cfg.State.Setting("performance.file_path")
  writeHTML(w,cfg.Logger,http.StatusOK,logRetentionPage(size,days,auditDays,redisLength,msg,appRows,detailDays,aggregateDays,source,path))
 })))
 mux.Handle("POST /log-retention/journal",requireAuth(store,http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  if err:=r.ParseForm();err!=nil{http.Error(w,"invalid form",400);return}
  size,err1:=strconv.Atoi(r.FormValue("max_mb"))
  days,err2:=strconv.Atoi(r.FormValue("max_days"))
  if err1!=nil||err2!=nil{http.Error(w,"invalid limits",400);return}
  ctx,cancel:=context.WithTimeout(r.Context(),15*time.Second);defer cancel()
  if err:=applyJournalPolicy(ctx,size,days);err!=nil{
   writeHTML(w,cfg.Logger,http.StatusBadRequest,logRetentionPage(size,days,30,0,err.Error(),""));return
  }
  http.Redirect(w,r,"/log-retention",http.StatusSeeOther)
 })))
}

func logRetentionPage(sizeMB,days,auditDays,redisLength int,problem,appRows string,options ...any)string{
 detailDays,aggregateDays:=7,30
 source,filePath:="journal",""
 if len(options)>=2{
  if n,ok:=options[0].(int);ok{detailDays=n}
  if n,ok:=options[1].(int);ok{aggregateDays=n}
 }
 if len(options)>=4{
  if v,ok:=options[2].(string);ok{source=v}
  if v,ok:=options[3].(string);ok{filePath=v}
 }
 option:=func(current,value int,label string)string{
  attr:="";if current==value{attr=" selected"}
  return fmt.Sprintf(`<option value="%d"%s>%s</option>`,value,attr,label)
 }
 alert:=""
 if problem!=""{alert=`<div class="alert">`+html.EscapeString(problem)+`</div>`}
 return pageHead("Log retention")+`<body>`+appHeader("log-retention")+`<main class="shell">
 <div class="page-head"><div><p class="eyebrow">Observability / retention</p><h1>Log retention</h1>
 <p class="sub">One place to understand log sources, retention boundaries and their actual scope.</p></div><a class="secondary" href="/performance">HTTP performance</a></div>`+alert+`
 <section class="panel panel-pad" style="margin-bottom:16px">
 <h2>System journal (shared limit)</h2>
 <p class="note">Caddy access/error logs and non-namespaced systemd services share this quota. Managed Go applications with dedicated journal namespaces have their own retention policy and are not governed by this shared limit.</p>
 <form method="post" action="/log-retention/journal" style="margin-top:16px;max-width:700px">
 <div class="caddy-timeouts">
 <div><label>Maximum persistent journal size</label><select name="max_mb">`+
 option(sizeMB,128,"128 MiB")+option(sizeMB,256,"256 MiB")+option(sizeMB,512,"512 MiB")+option(sizeMB,1024,"1 GiB")+option(sizeMB,2048,"2 GiB")+`</select></div>
 <div><label>Maximum age</label><select name="max_days">`+
 option(days,1,"1 day")+option(days,3,"3 days")+option(days,7,"7 days")+option(days,14,"14 days")+option(days,30,"30 days")+`</select></div></div>
 <p class="note">Applies via /etc/systemd/journald.conf.d. Restarting journald is required; existing archived journals are not vacuumed by this action. If journald uses volatile storage, SystemMaxUse may not govern its disk allocation.</p>
 <button class="button" type="submit" onclick="return confirm('Apply shared system journal limits and restart systemd-journald?')">Apply journal limits</button></form></section>
 <section class="panel panel-pad" style="margin-bottom:16px"><h2>Caddy HTTP metrics source</h2>
 <p class="note">Choose the already configured JSON access-log destination. Journal mode reads caddy.service from journald; file mode reads a JSON-lines file in /var/log/caddy/. Collector offsets and journal cursors are persisted for restart-safe continuation. Switching sources may import overlapping entries from both sources.</p>
 <form method="post" action="/log-retention/source" style="margin-top:14px">
 <div class="caddy-timeouts"><div><label>Driver</label><select name="source">
 <option value="journal"`+selected(source,"journal")+`>systemd-journald</option>
 <option value="file"`+selected(source,"file")+`>JSON file</option></select></div>
 <div><label>Caddy JSON file path</label><input name="file_path" value="`+html.EscapeString(filePath)+`" placeholder="/var/log/caddy/access.log"></div></div>
 <button class="secondary" style="margin-top:12px">Save metrics source</button></form></section>
 <section class="panel panel-pad" style="margin-bottom:16px"><h2>HTTP performance history</h2>
 <p class="note">SQLite metrics are collected in the background. Detailed requests expire after the selected period; hourly request counts and duration sums are retained separately for long-term history. Total raw history is additionally capped at 200,000 requests. Once detail rows expire, historical request percentiles cannot be reconstructed exactly.</p>
 <form method="post" action="/log-retention/performance" style="max-width:700px;margin-top:14px">
 <div class="caddy-timeouts"><div><label>Detailed HTTP requests</label><select name="detail_days">`+
 option(detailDays,1,"1 day")+option(detailDays,3,"3 days")+option(detailDays,7,"7 days")+option(detailDays,14,"14 days")+option(detailDays,30,"30 days")+`</select></div>
 <div><label>Hourly aggregates</label><select name="aggregate_days">`+
 option(aggregateDays,7,"7 days")+option(aggregateDays,14,"14 days")+option(aggregateDays,30,"30 days")+option(aggregateDays,60,"60 days")+option(aggregateDays,90,"90 days")+`</select></div></div>
 <button class="secondary" style="margin-top:12px">Save HTTP retention</button></form></section>
 <section class="panel panel-pad" style="margin-bottom:16px"><h2>Panel audit trail (SQLite)</h2>
 <p class="note">Audit records have their own retention window, independent of systemd-journald. Expired events are deleted immediately on save and on subsequent audit writes.</p>
 <form method="post" action="/log-retention/audit" class="compact-form" style="max-width:640px">
 <select name="days" aria-label="Audit log retention">`+
 option(auditDays,7,"7 days")+option(auditDays,14,"14 days")+option(auditDays,30,"30 days")+option(auditDays,90,"90 days")+option(auditDays,365,"365 days") +`</select>
 <button class="secondary">Save audit retention</button></form></section>
 <section class="panel" style="margin-bottom:16px"><div class="panel-pad"><h2>Managed application journal retention</h2>
 <p class="note">Existing per-app retention policies use separate journald namespaces. Settings are managed in each application's service configuration; this overview does not change running applications.</p></div>
 <div class="table-scroll"><table><thead><tr><th>Application</th><th>Retention</th><th></th></tr></thead><tbody>`+appRows+`</tbody></table></div></section>
 <section class="panel panel-pad" style="margin-bottom:16px"><h2>Redis SLOWLOG memory buffer</h2>
 <p class="note">Redis retains a bounded number of slow commands in memory, independent of journald. The limit is per Redis instance, not per logical database. Changes use CONFIG SET and CONFIG REWRITE.</p>
 <form method="post" action="/log-retention/redis" class="compact-form" style="max-width:640px">
 <select name="length" aria-label="Redis SLOWLOG maximum commands">`+option(redisLength,128,"128 commands")+option(redisLength,256,"256 commands")+option(redisLength,512,"512 commands")+option(redisLength,1024,"1024 commands")+`</select><button class="secondary">Save Redis SLOWLOG limit</button></form></section>
 <section class="panel panel-pad"><h2>Source-specific logging and retention</h2>
 <p class="note">Separate controls are shown only where the underlying service genuinely supports an independent policy.</p>
 <div class="table-scroll"><table><thead><tr><th>Log source</th><th>Storage / retention scope</th><th>Configure</th></tr></thead><tbody>
 <tr><td>Caddy access and error</td><td>Shared system journal. HTTP performance is calculated on demand and does not store a second raw log.</td><td><a class="secondary" href="/caddy/logs">Caddy logs</a></td></tr>
 <tr><td>Managed application journals</td><td>Per-app journald namespaces with existing LogRetentionDays settings (except raw units). See each application for configuration.</td><td><a class="secondary" href="/apps">Applications</a></td></tr>
 <tr><td>MySQL slow queries</td><td>MySQL FILE/TABLE slow-log output, independent of journald. Rotation and table cleanup require separate server policy.</td><td><a class="secondary" href="/databases">Databases</a></td></tr>
 <tr><td>PostgreSQL slow queries</td><td>PostgreSQL file log; retention follows the host logrotate/logging configuration.</td><td><a class="secondary" href="/databases">Databases</a></td></tr>
 <tr><td>Redis slow commands</td><td>In-memory Redis SLOWLOG capped by the maximum count configured above; no extra copies made by the panel.</td><td><a class="secondary" href="/databases/redis/slow-queries">Redis SLOWLOG</a></td></tr>
 <tr><td>Panel audit log</td><td>SQLite audit_log; configured above.</td><td><a class="secondary" href="/activity">Activity</a></td></tr>
 </tbody></table></div></section>
 </main></body></html>`
}
