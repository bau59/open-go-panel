package server

import (
 "context"
 "fmt"
 "html"
 "net/http"
 "net/url"
 "strconv"
 "strings"
 "time"
 "math"

 "github.com/bau59/open-go-panel/internal/caddy"
)

func registerPerformanceRoutes(mux *http.ServeMux, store *sessionStore, cfg Config) {
 mux.Handle("GET /performance", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  v:=r.URL.Query()
  period:=v.Get("period")
  now:=time.Now()
  from:=now.Add(-time.Hour)
  switch period {
  case "5m": from=now.Add(-5*time.Minute)
  case "24h": from=now.Add(-24*time.Hour)
  case "7d": from=now.Add(-7*24*time.Hour)
  case "custom":
   a,ae:=time.ParseInLocation("2006-01-02T15:04",v.Get("from"),time.Local)
   b,be:=time.ParseInLocation("2006-01-02T15:04",v.Get("to"),time.Local)
   if ae!=nil || be!=nil || !a.Before(b) || b.After(now.Add(time.Minute)) || b.Sub(a)>31*24*time.Hour {
     http.Error(w,"invalid custom time window",http.StatusBadRequest);return
   }
   from,now=a,b
  case "","1h":period="1h"
  default:http.Error(w,"invalid period",http.StatusBadRequest);return
  }
  threshold,_:=strconv.ParseFloat(v.Get("threshold"),64)
  if threshold<=0 || threshold>60000 {threshold=500}
  status,_:=strconv.Atoi(v.Get("status"))
  if status!=0 && status!=200 && status!=300 && status!=400 && status!=500 {status=0}
  page,_:=strconv.Atoi(v.Get("page"));if page<1 || page>1000 {page=1}
  f:=caddy.PerformanceFilter{Since:from,Until:now,Domain:v.Get("domain"),Method:v.Get("method"),Route:v.Get("route"),
   Status:status,SlowOnly:v.Get("slow")=="1",ThresholdMS:threshold,Page:page,PerPage:50,Sort:v.Get("sort")}
  ctx,cancel:=context.WithTimeout(r.Context(),15*time.Second);defer cancel()
  data,err:=cfg.Caddy.QueryPerformance(ctx,f)
  errMsg:=""
  if err!=nil {errMsg=err.Error()}
  writeHTML(w,cfg.Logger,http.StatusOK,performancePage(period,v,f,data,errMsg))
 })))
}

func performancePage(period string, values url.Values, f caddy.PerformanceFilter, data caddy.PerformanceResult, problem string)string{
 opt:=func(name,val,label string) string{
  return `<option value="`+html.EscapeString(val)+`"`+selected(values.Get(name),val)+`>`+html.EscapeString(label)+`</option>`
 }
 var rows strings.Builder
 for _,entry:=range data.Rows{
  fmt.Fprintf(&rows,`<tr><td>%s</td><td>%s</td><td>%s</td><td><code>%s</code></td><td>%d</td><td><strong>%.2f ms</strong></td><td>%d B</td><td><details><summary class="secondary">Details</summary><div class="inline-popover wide">
  <p>Caddy: %.2f ms · Protocol: %s</p><p>Backend / Server-Timing: No data</p><p>Request ID: No data</p><p>Related restart: No data</p></div></details></td></tr>`,
   html.EscapeString(entry.Time.Local().Format("01-02 15:04:05")),html.EscapeString(entry.Domain),
   html.EscapeString(entry.Method),html.EscapeString(entry.Route),entry.Status,entry.DurationMS,entry.Size,
   entry.DurationMS,html.EscapeString(entry.Protocol))
 }
 if rows.Len()==0 {rows.WriteString(`<tr><td colspan="8" class="empty">No measured HTTP requests in the selected interval.</td></tr>`)}
 var routes strings.Builder
 for i,route:=range data.Routes{
  if i>=40 {break}
  fmt.Fprintf(&routes,`<tr><td>%s</td><td><code>%s %s</code></td><td>%d</td><td>%.2f</td><td>%.2f</td><td>%.2f</td><td>%.2f</td><td>%.1f%%</td></tr>`,
   html.EscapeString(route.Domain),html.EscapeString(route.Method),html.EscapeString(route.Route),
   route.Count,route.P50,route.P95,route.P99,route.Max,100*float64(route.Errors)/float64(route.Count))
 }
 if routes.Len()==0 {routes.WriteString(`<tr><td colspan="8" class="empty">No route data.</td></tr>`)}
 query:=url.Values{}
 for name,arr:=range values {if name!="page" {for _,v:=range arr{query.Add(name,v)}}}
 next:=""
 if data.HasNext {query.Set("page",strconv.Itoa(f.Page+1));next=`<a class="secondary" href="/performance?`+html.EscapeString(query.Encode())+`">Next page</a>`}
 prev:=""
 if f.Page>1 {query.Set("page",strconv.Itoa(f.Page-1));prev=`<a class="secondary" href="/performance?`+html.EscapeString(query.Encode())+`">Previous</a>`}
 caution:=""
 if data.Truncated {caution=`<div class="alert">Source scan capped at 20,000 matching requests. Percentiles and totals refer only to the scanned sample, not all requests in this interval.</div>`}
 if problem!="" {caution+=`<div class="alert">`+html.EscapeString(problem)+`</div>`}
 stat:=func(name string,value string) string {return `<div class="metric"><span>`+name+`</span><strong>`+value+`</strong></div>`}
 div:=func(n int)string{if n==0{return "—"};return fmt.Sprintf("%.1f%%",100*float64(data.Errors)/float64(n))}
 var graphic strings.Builder
 maxv:=0.0
 for _,b:=range data.Buckets {maxv=math.Max(maxv,b.P99)}
 if maxv>0 && len(data.Buckets)>1{
  graph:=func(pick func(caddy.PerformanceBucket)float64)string{
   var points strings.Builder
   for i,b:=range data.Buckets{
    x:=float64(i)*940/float64(len(data.Buckets)-1)+20
    y:=180-(pick(b)/maxv)*150
    fmt.Fprintf(&points,"%.1f,%.1f ",x,y)
   }
   return strings.TrimSpace(points.String())
  }
  graphic.WriteString(`<svg viewBox="0 0 980 210" role="img" aria-label="P50 P95 and P99 measured Caddy request durations over time" style="display:block;width:100%;height:230px;max-width:100%">`)
  for _,line:=range []struct{color,points string}{
   {"#8e88fa",graph(func(b caddy.PerformanceBucket)float64{return b.P50})},
   {"#53c2bd",graph(func(b caddy.PerformanceBucket)float64{return b.P95})},
   {"#ffab70",graph(func(b caddy.PerformanceBucket)float64{return b.P99})},
  }{graphic.WriteString(`<polyline fill="none" stroke="`+line.color+`" stroke-width="2" points="`+line.points+`"/>`)}
  for i,b:=range data.Buckets{
   x:=float64(i)*940/float64(len(data.Buckets)-1)+20
   y:=180-(b.P95/maxv)*150
   fmt.Fprintf(&graphic,`<circle cx="%.1f" cy="%.1f" r="4" fill="#53c2bd"><title>%s | %d requests | P50 %.2f ms | P95 %.2f ms | P99 %.2f ms</title></circle>`,
    x,y,html.EscapeString(b.Time.Local().Format("2006-01-02 15:04")),b.Count,b.P50,b.P95,b.P99)
  }
  graphic.WriteString(`</svg><p class="note">Purple P50 · Turquoise P95 · Orange P99. Vertical scale: 0–`+fmt.Sprintf("%.2f",maxv)+` ms.</p>`)
 }else{graphic.WriteString(`<p class="note">Not enough time buckets for a percentile trend.</p>`)}
 var buckets strings.Builder
 for _,b:=range data.Buckets{fmt.Fprintf(&buckets,`<tr><td>%s</td><td>%d</td><td>%.2f</td><td>%.2f</td><td>%.2f</td></tr>`,
 html.EscapeString(b.Time.Local().Format("2006-01-02 15:04")),b.Count,b.P50,b.P95,b.P99)}
 if buckets.Len()==0 {buckets.WriteString(`<tr><td colspan="5" class="empty">No measured time buckets.</td></tr>`)}
 return pageHead("Performance")+`<body>`+appHeader("performance")+`<main class="shell">
 <div class="page-head"><div><p class="eyebrow">Observability / HTTP</p><h1>Performance</h1><p class="sub">Measured Caddy HTTP handling time, not isolated Go, DNS, TLS or browser rendering time.</p></div>
 <a href="/log-retention" class="secondary">Log retention</a></div>`+caution+`
 <section class="panel panel-pad" style="margin-bottom:16px"><h2>Filters</h2>
 <form method="get" action="/performance" class="grid" style="grid-template-columns:repeat(auto-fit,minmax(160px,1fr));margin-top:14px">
 <div><label>Period</label><select name="period">`+opt("period","5m","Last 5 minutes")+opt("period","1h","Last hour")+opt("period","24h","Last 24 hours")+opt("period","7d","Last 7 days")+opt("period","custom","Custom window")+`</select></div>
 <div><label>Domain</label><input name="domain" placeholder="Any domain" value="`+html.EscapeString(f.Domain)+`"></div>
 <div><label>Method</label><select name="method">`+opt("method","","Any method")+opt("method","GET","GET")+opt("method","POST","POST")+opt("method","PUT","PUT")+opt("method","PATCH","PATCH")+opt("method","DELETE","DELETE")+`</select></div>
 <div><label>Route</label><input name="route" placeholder="/api/orders" value="`+html.EscapeString(f.Route)+`"></div>
 <div><label>Status class</label><select name="status">`+opt("status","0","Any status")+opt("status","200","2xx")+opt("status","300","3xx")+opt("status","400","4xx")+opt("status","500","5xx")+`</select></div>
 <div><label>Slow threshold (ms)</label><input type="number" min="1" max="60000" name="threshold" value="`+fmt.Sprintf("%.0f",f.ThresholdMS)+`"></div>
 <div><label>Sort requests</label><select name="sort">`+opt("sort","","Newest first")+opt("sort","duration","Slowest first")+`</select></div>
 <div><label>From (custom)</label><input type="datetime-local" name="from" value="`+html.EscapeString(values.Get("from"))+`"></div>
 <div><label>Until (custom)</label><input type="datetime-local" name="to" value="`+html.EscapeString(values.Get("to"))+`"></div>
 <div><label style="margin-top:8px"><input type="checkbox" style="width:auto;height:auto" name="slow" value="1"`+checked(f.SlowOnly)+`> Only slow</label><button class="button">Apply</button></div>
 </form></section>
 <section class="metrics-grid" style="margin-bottom:16px">`+
 stat("Measured requests",strconv.Itoa(data.Total))+stat("Average",fmt.Sprintf("%.2f ms",data.Average))+
 stat("P50",fmt.Sprintf("%.2f ms",data.P50))+stat("P95",fmt.Sprintf("%.2f ms",data.P95))+
 stat("P99",fmt.Sprintf("%.2f ms",data.P99))+stat("Slow requests",strconv.Itoa(data.Slow))+
 stat("HTTP 5xx",strconv.Itoa(data.Errors))+stat("5xx rate",div(data.Total))+`</section>
 <section class="panel panel-pad" style="margin-bottom:16px"><h2>Request duration trend</h2>
 <p class="note">Measured Caddy request durations; request counts and precise percentiles by time bucket appear below.</p>`+graphic.String()+`
 <details><summary class="secondary">Exact bucket values</summary><div class="table-scroll"><table><thead><tr><th>Time</th><th>Requests</th><th>P50 ms</th><th>P95 ms</th><th>P99 ms</th></tr></thead><tbody>`+buckets.String()+`</tbody></table></div></details></section>
 <section class="panel" style="margin-bottom:16px"><div class="panel-pad"><h2>Slowest routes by P95</h2><p class="note">Numeric path segments and UUIDs are grouped; unknown dynamic route templates are not inferred.</p></div>
 <div class="table-scroll"><table><thead><tr><th>Domain</th><th>Route</th><th>Count</th><th>P50 ms</th><th>P95 ms</th><th>P99 ms</th><th>Max ms</th><th>5xx</th></tr></thead><tbody>`+routes.String()+`</tbody></table></div></section>
 <section class="panel"><div class="panel-pad"><h2>Request log</h2><p class="note">Query strings and client IP addresses are not retained in performance results.</p></div>
 <div class="table-scroll"><table><thead><tr><th>Time</th><th>Domain</th><th>Method</th><th>Route</th><th>Status</th><th>Duration</th><th>Size</th><th></th></tr></thead><tbody>`+rows.String()+`</tbody></table></div>
 <div class="pager" style="padding:16px"><span class="pager-info">Page `+strconv.Itoa(f.Page)+`</span><div class="pager-actions">`+prev+next+`</div></div></section>
 <section class="panel panel-pad" style="margin-top:16px"><h2>Cold starts and backend timing</h2><p class="note">No data: process restart correlation and Server-Timing are not available from the current Caddy access-log fields. These measurements are not estimated.</p></section>
 </main></body></html>`
}
