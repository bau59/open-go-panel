package server

import (
 "fmt"
 "html"
 "strings"
 "time"

 "github.com/bau59/open-go-panel/internal/caddy"
)

type performanceExtras struct {
 Global performanceThresholds
 PerApp map[int64]performanceThresholds
 Apps []performanceAppOption
 Restarts []caddy.ProcessStart
 Cold []caddy.ColdSample
 Idle []caddy.IdleSample
 Collector caddy.CollectorStatus
 Rollups []caddy.PerformanceRollup
}

func (e performanceExtras) classify(ms float64,appID int64)(string,string){
 limits:=e.Global
 if limits.Fast==0{limits=defaultPerformanceThresholds()}
 if v,ok:=e.PerApp[appID];ok{limits=v}
 return limits.classify(ms)
}

func (e performanceExtras) restartFor(appID int64,when time.Time)string {
 if appID<=0{return "No data"}
 var known time.Time
 for _,ev:=range e.Restarts{
  if ev.AppID==appID && !ev.Time.After(when) && ev.Time.After(known){
   known=ev.Time
  }
 }
 if known.IsZero(){return "No data"}
 return "Observed systemd start at "+known.Local().Format("2006-01-02 15:04:05")
}

func performanceColdPanel(e performanceExtras,appFilter int64)string{
 var starts,firsts,idles strings.Builder
 for _,event:=range e.Restarts {
  if appFilter>0 && appFilter!=event.AppID{continue}
  fmt.Fprintf(&starts,`<tr><td>#%d</td><td>%s</td><td>%s</td><td>%s</td></tr>`,
   event.AppID,html.EscapeString(event.Time.Local().Format("2006-01-02 15:04:05")),
   html.EscapeString(event.Service),html.EscapeString(event.Source))
 }
 for _,s:=range e.Cold{
  if appFilter>0 && appFilter!=s.AppID{continue}
  later:="No data"
  if s.LaterSameRouteCount>0{later=fmt.Sprintf("%.2f ms (%d requests)",s.LaterSameRouteAverageMS,s.LaterSameRouteCount)}
  fmt.Fprintf(&firsts,`<tr><td>#%d</td><td>%s</td><td>%s</td><td><code>%s</code></td><td>%.2f ms</td><td>%d / %.2f ms</td><td>%s</td></tr>`,
   s.AppID,html.EscapeString(s.Started.Local().Format("2006-01-02 15:04")),html.EscapeString(s.Source),
   html.EscapeString(s.FirstRoute),s.FirstDurationMS,s.FirstMinuteRequests,s.FirstMinuteAverageMS,
   html.EscapeString(later))
 }
 for _,s:=range e.Idle{
  if appFilter>0 && appFilter!=s.AppID{continue}
  fmt.Fprintf(&idles,`<tr><td>#%d</td><td>%s</td><td><code>%s</code></td><td>%.1f min</td><td>%.2f ms</td></tr>`,
   s.AppID,html.EscapeString(s.Time.Local().Format("2006-01-02 15:04")),
   html.EscapeString(s.Route),s.Gap.Minutes(),s.DurationMS)
 }
 if starts.Len()==0{starts.WriteString(`<tr><td colspan="4" class="empty">No confirmed process starts for this interval.</td></tr>`)}
 if firsts.Len()==0{firsts.WriteString(`<tr><td colspan="7" class="empty">No first requests after a confirmed process start in the available history.</td></tr>`)}
 if idles.Len()==0{idles.WriteString(`<tr><td colspan="5" class="empty">No observed requests after 15 minutes of inactivity.</td></tr>`)}
 return `<section class="panel panel-pad" style="margin-top:16px">
  <h2>Cold starts — observed systemd and Air events</h2>
  <p class="note">Systemd process launches use ExecMainStartTimestamp. Air child runs are recorded only from explicit Air running... journal markers and are labelled air-log; these are observations, not verified process PIDs. Exit reasons are not inferred. First requests are the first observed in the saved history.</p>
  <div class="table-scroll"><table><thead><tr><th>App</th><th>Process started</th><th>Service</th><th>Source</th></tr></thead><tbody>`+starts.String()+`</tbody></table></div>
  <h2 style="margin-top:20px">First request and first minute after start</h2>
  <div class="table-scroll"><table><thead><tr><th>App</th><th>Start</th><th>Source</th><th>First observed route</th><th>First request</th><th>First minute: count / average</th><th>Later same route (1 hour)</th></tr></thead><tbody>`+firsts.String()+`</tbody></table></div>
  <h2 style="margin-top:20px">Requests after 15 minutes idle</h2>
  <p class="note">Inactivity is a separate observation, not a process restart. It is calculated from consecutive requests captured for each app.</p>
  <div class="table-scroll"><table><thead><tr><th>App</th><th>Time</th><th>Route</th><th>Idle</th><th>Caddy duration</th></tr></thead><tbody>`+idles.String()+`</tbody></table></div>
 </section>`
}

func performanceCollectorPanel(s caddy.CollectorStatus)string{
 status:="No successful collection yet"
 if !s.LastSuccess.IsZero(){status=s.LastSuccess.Local().Format("2006-01-02 15:04:05")}
 warning:=""
 if s.LastError!=""{warning=`<div class="alert">Collector: `+html.EscapeString(s.LastError)+`</div>`}
 return `<section class="panel panel-pad" style="margin-bottom:16px">
 <h2>Background collector</h2>
 <p class="note">Source: `+html.EscapeString(s.Source)+` · Latest successful batch: `+html.EscapeString(status)+` · Persisted requests: `+fmt.Sprintf("%d",s.Stored)+
 ` · Journal entries scanned: `+fmt.Sprintf("%d",s.Scanned)+` · Parse errors: `+
 fmt.Sprintf("%d",s.ParseErrors)+` · Metric loss/overflow indicators: `+fmt.Sprintf("%d",s.Dropped)+`</p>`+warning+
 `<p class="note">The journal reader runs in bounded batches independently from the HTTP request path. Metrics can be incomplete if upstream logs are missing, rotation removes unread records, or the storage cap is reached.</p></section>`
}

func performanceRollupPanel(e performanceExtras) string{
 var rows strings.Builder
 var count,errorCount int64
 for _,r:=range e.Rollups{
  count+=r.Requests
  errorCount+=r.Errors5xx
  fmt.Fprintf(&rows,`<tr><td>%s</td><td>%d</td><td>%.2f ms</td><td>%d</td><td>%d</td></tr>`,
   html.EscapeString(r.Hour.Local().Format("2006-01-02 15:00")),
   r.Requests,r.AverageMS,r.Slow500,r.Errors5xx)
 }
 if rows.Len()==0{rows.WriteString(`<tr><td colspan="5" class="empty">No hourly aggregates for this interval.</td></tr>`)}
 return `<section class="panel" style="margin-top:16px">
  <div class="panel-pad"><h2>Historical hourly aggregates</h2>
  <p class="note">Measured counts, errors and arithmetic mean from persisted hourly buckets. Aggregates are retained independently from individual requests. P50/P95/P99 are not reconstructed from averages after detail rows expire. Status and slow-only request filters do not apply to this summary.</p>
  <p class="note">Hourly requests: `+fmt.Sprintf("%d",count)+` · 5xx: `+fmt.Sprintf("%d",errorCount)+`</p></div>
  <details><summary class="secondary" style="margin:0 16px 16px">Show hourly aggregates</summary><div class="table-scroll">
  <table><thead><tr><th>Hour</th><th>Requests</th><th>Mean Caddy duration</th><th>≥500 ms</th><th>5xx</th></tr></thead>
  <tbody>`+rows.String()+`</tbody></table></div></details></section>`
}
