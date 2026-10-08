package server

import (
 "fmt"
 "html"
 "net/http"
 "strconv"
 "strings"

 "github.com/bau59/open-go-panel/internal/state"
)

type performanceThresholds struct {
 Fast, Ordinary, Slow int
}

func defaultPerformanceThresholds() performanceThresholds {
 return performanceThresholds{Fast:100,Ordinary:500,Slow:1000}
}

func parsePerformanceThresholds(raw string)(performanceThresholds,bool){
 parts:=strings.Split(raw,",")
 if len(parts)!=3{return performanceThresholds{},false}
 var t performanceThresholds
 var err error
 t.Fast,err=strconv.Atoi(parts[0]);if err!=nil{return t,false}
 t.Ordinary,err=strconv.Atoi(parts[1]);if err!=nil{return t,false}
 t.Slow,err=strconv.Atoi(parts[2]);if err!=nil{return t,false}
 return t,t.Fast>0&&t.Fast<t.Ordinary&&t.Ordinary<t.Slow&&t.Slow<=60000
}

func (t performanceThresholds) serialize()string{
 return fmt.Sprintf("%d,%d,%d",t.Fast,t.Ordinary,t.Slow)
}

func getPerformanceThresholds(store *state.Store,appID int64)performanceThresholds {
 fallback:=defaultPerformanceThresholds()
 if appID>0 {
  fallback=getPerformanceThresholds(store,0)
 }
 key:="performance.thresholds.default"
 if appID>0{key=fmt.Sprintf("performance.thresholds.app.%d",appID)}
 raw,ok,err:=store.Setting(key)
 if err!=nil||!ok{return fallback}
 value,valid:=parsePerformanceThresholds(raw)
 if !valid{return fallback}
 return value
}

func (t performanceThresholds) classify(duration float64)(string,string){
 switch{
 case duration< float64(t.Fast): return "Fast","ok"
 case duration< float64(t.Ordinary): return "Normal",""
 case duration<=float64(t.Slow): return "Slow","warn"
 default:return "Very slow","danger"
 }
}

func registerPerformanceSettings(mux *http.ServeMux,store *sessionStore,cfg Config){
 mux.Handle("POST /performance/thresholds",requireAuth(store,http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  if err:=r.ParseForm();err!=nil{http.Error(w,"invalid form",400);return}
  appID,err:=strconv.ParseInt(r.FormValue("app_id"),10,64)
  if err!=nil||appID<0{http.Error(w,"invalid app ID",400);return}
  if appID>0{
   if _,err:=cfg.Apps.Get(appID);err!=nil{http.Error(w,"application not found",404);return}
  }
  t:=r.FormValue("fast_ms")+","+r.FormValue("ordinary_ms")+","+r.FormValue("slow_ms")
  limits,valid:=parsePerformanceThresholds(t)
  if !valid{http.Error(w,"thresholds must be strictly ascending, within 1–60000ms",400);return}
  key:="performance.thresholds.default"
  if appID>0{key=fmt.Sprintf("performance.thresholds.app.%d",appID)}
  if err:=cfg.State.SetSetting(key,limits.serialize());err!=nil{http.Error(w,err.Error(),500);return}
  redirect:="/performance"
  if appID>0{redirect=fmt.Sprintf("/performance?app=%d",appID)}
  http.Redirect(w,r,redirect,http.StatusSeeOther)
 })))
}

func performanceThresholdForm(t performanceThresholds,appID int64,apps []performanceAppOption)string{
 var opts strings.Builder
 opts.WriteString(`<option value="0"`)
 if appID==0{opts.WriteString(" selected")}
 opts.WriteString(`>All domains (global default)</option>`)
 for _,app:=range apps{
  mark:=""
  if app.ID==appID{mark=" selected"}
  fmt.Fprintf(&opts,`<option value="%d"%s>%s</option>`,app.ID,mark,html.EscapeString(app.Name))
 }
 return `<section class="panel panel-pad" style="margin-top:16px">
  <h2>Duration classification</h2>
  <p class="note">Classification is visual only, not an SLA. Thresholds are global by default and may be overridden for individual applications.</p>
  <form method="post" action="/performance/thresholds" style="margin-top:14px">
  <div class="caddy-timeouts" style="max-width:100%">
   <div><label>Scope</label><select name="app_id">`+opts.String()+`</select></div>
   <div><label>Fast below (ms)</label><input name="fast_ms" type="number" min="1" max="60000" value="`+strconv.Itoa(t.Fast)+`" required></div>
   <div><label>Normal below (ms)</label><input name="ordinary_ms" type="number" min="1" max="60000" value="`+strconv.Itoa(t.Ordinary)+`" required></div>
   <div><label>Slow up to (ms)</label><input name="slow_ms" type="number" min="1" max="60000" value="`+strconv.Itoa(t.Slow)+`" required></div>
  </div>
  <button class="button" style="margin-top:12px">Save thresholds</button>
  </form></section>`
}

type performanceAppOption struct{ID int64;Name string}
