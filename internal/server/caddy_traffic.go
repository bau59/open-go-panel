package server

import (
 "fmt"
 "html"
 "strings"
 panelcaddy "github.com/bau59/open-go-panel/internal/caddy"
)

func caddyTrafficPage(site panelcaddy.Site, report panelcaddy.TrafficReport, days int, message string) string {
 var b strings.Builder
 b.WriteString(pageHead("Domain analytics")+"<body>"+appHeader("caddy")+`<main class="shell"><div class="page-head"><div><p class="eyebrow">Caddy / Traffic</p><h1>`+html.EscapeString(site.Domain)+`</h1><p class="sub">Approximate traffic analytics from Caddy journal; IP is not a person or session. UTC dates.</p></div><div class="actions"><a class="secondary" href="/caddy?app=`+fmt.Sprint(site.AppID)+`">Domain settings</a>`)
 if site.AppID>0 {b.WriteString(`<a class="secondary" href="/apps/`+fmt.Sprint(site.AppID)+`">Application</a>`)}
 b.WriteString(`</div></div><div class="actions" style="margin-bottom:16px">`)
 for _,n:=range []int{1,7,30}{b.WriteString(fmt.Sprintf(`<a class="secondary" href="/caddy/site/%d/analytics?days=%d">%d day(s)</a>`,site.AppID,n,n))}
 b.WriteString("</div>")
 if message!="" {b.WriteString(`<div class="alert">`+html.EscapeString(message)+`</div>`)}
 if report.Partial {b.WriteString(`<div class="alert">Report truncated after 300,000 log records. Narrow the period for complete data.</div>`)}
 b.WriteString(fmt.Sprintf(`<section class="panel panel-pad"><h2>Summary — last %d days</h2><div class="domain-summary"><div><span>Page views</span><strong>%d</strong></div><div><span>Unique IPs (approx.)</span><strong>%d</strong></div><div><span>HTTP requests</span><strong>%d</strong></div><div><span>HTTP errors (4xx/5xx)</span><strong>%d</strong></div></div></section>`,days,report.Views,report.Visitors,report.Requests,report.Errors))
 b.WriteString(`<section class="panel panel-pad" style="margin-top:16px"><h2>Daily traffic</h2><div class="table-scroll"><table><thead><tr><th>Day (UTC)</th><th>Page views</th><th>Unique IPs</th></tr></thead><tbody>`)
 for _,row:=range report.Days{b.WriteString(fmt.Sprintf("<tr><td>%s</td><td>%d</td><td>%d</td></tr>",row.Date,row.Views,row.Visitors))}
 b.WriteString("</tbody></table></div></section>")
 table:=func(title string,items []panelcaddy.TrafficItem){
  b.WriteString(`<section class="panel panel-pad" style="margin-top:16px"><h2>`+html.EscapeString(title)+`</h2><div class="table-scroll"><table><thead><tr><th>Path / source</th><th>Count</th></tr></thead><tbody>`)
  for _,item:=range items {b.WriteString(fmt.Sprintf("<tr><td><code>%s</code></td><td>%d</td></tr>",html.EscapeString(item.Name),item.Count))}
  if len(items)==0{b.WriteString(`<tr><td colspan="2">No data</td></tr>`)}
  b.WriteString("</tbody></table></div></section>")
 }
 table("Popular pages",report.Pages)
 table("Approximate entry pages (first observed page per IP/day)",report.Entrances)
 table("Referrer hosts",report.Referrers)
 b.WriteString(`<p class="sub" style="margin-top:16px">Filters: GET / HEAD, common static resources and crawlers excluded. No tracking cookies or persisted visitor profiles. Results depend on journal retention and trustworthy client IP forwarding.</p></main></body></html>`)
 return b.String()
}
