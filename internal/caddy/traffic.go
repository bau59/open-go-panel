package caddy

import (
 "bufio"
 "context"
 "encoding/json"
 "fmt"
 "net/url"
 "os/exec"
 "sort"
 "strings"
 "time"
)

type TrafficItem struct { Name string; Count int }
type TrafficDay struct { Date string; Views int; Visitors int }
type TrafficReport struct {
 Domain string
 From time.Time
 To time.Time
 Requests int
 Views int
 Visitors int
 Errors int
 Days []TrafficDay
 Pages []TrafficItem
 Entrances []TrafficItem
 Referrers []TrafficItem
 Partial bool
}
type trafficRequest struct {
 Logger string `json:"logger"`
 Ts float64 `json:"ts"`
 Status int `json:"status"`
 Request struct {
  Host string `json:"host"`
  URI string `json:"uri"`
  Method string `json:"method"`
  ClientIP string `json:"client_ip"`
  RemoteIP string `json:"remote_ip"`
  Headers map[string][]string `json:"headers"`
 } `json:"request"`
}
func (m *Manager) Traffic(ctx context.Context, domain string, days int) (TrafficReport,error) {
 if days != 1 && days != 7 && days != 30 { days=7 }
 now:=time.Now().UTC()
 from:=now.AddDate(0,0,-days)
 out:=TrafficReport{Domain:domain,From:from,To:now}
 cmd:=exec.CommandContext(ctx,"journalctl","--no-pager","-o","json","-u","caddy.service","--since",from.Format("2006-01-02 15:04:05"))
 stdout,err:=cmd.StdoutPipe(); if err!=nil{return out,err}
 if err=cmd.Start();err!=nil{return out,err}
 scanner:=bufio.NewScanner(stdout)
 scanner.Buffer(make([]byte,64*1024),4*1024*1024)
 ips:=map[string]struct{}{}
 dailyIPs:=map[string]map[string]struct{}{}
 dailyViews:=map[string]int{}
 pages:=map[string]int{}
 entrances:=map[string]int{}
 sources:=map[string]int{}
 seenEntrance:=map[string]struct{}{}
 count:=0
 for scanner.Scan(){
  count++
  if count>300000 {out.Partial=true;break}
  var envelope journalEnvelope
  if json.Unmarshal(scanner.Bytes(),&envelope)!=nil {continue}
  var e trafficRequest
  raw:=envelope.Message
  idx:=strings.IndexByte(raw,'{')
  if idx<0 || json.Unmarshal([]byte(raw[idx:]),&e)!=nil {continue}
  if !strings.HasPrefix(e.Logger,"http.log.") || strings.Contains(e.Logger,".error") {continue}
  if !strings.EqualFold(strings.TrimSuffix(e.Request.Host,"."),domain) {continue}
  ts:=time.Unix(int64(e.Ts),0).UTC()
  if ts.Before(from)||ts.After(now){continue}
  out.Requests++
  if e.Status>=400 {out.Errors++}
  if e.Request.Method!="GET" && e.Request.Method!="HEAD" {continue}
  path:=e.Request.URI
  if u,err:=url.ParseRequestURI(path);err==nil{path=u.Path}
  if path==""{path="/"}
  if strings.HasPrefix(path,"/assets/")||strings.HasPrefix(path,"/static/")||strings.HasPrefix(path,"/img/")||strings.HasPrefix(path,"/uploads/")||strings.HasPrefix(path,"/api/")||strings.Contains(path,".") {continue}
  agent:=strings.ToLower(strings.Join(e.Request.Headers["User-Agent"]," "))
  if strings.Contains(agent,"bot")||strings.Contains(agent,"spider")||strings.Contains(agent,"crawler")||strings.Contains(agent,"curl/") {continue}
  ip:=e.Request.ClientIP; if ip==""{ip=e.Request.RemoteIP};if ip==""{continue}
  out.Views++
  ips[ip]=struct{}{}
  day:=ts.Format("2006-01-02")
  if dailyIPs[day]==nil {dailyIPs[day]=map[string]struct{}{}}
  dailyIPs[day][ip]=struct{}{}
  dailyViews[day]++
  pages[path]++
  key:=day+"|"+ip
  if _,ok:=seenEntrance[key];!ok {
   seenEntrance[key]=struct{}{}
   entrances[path]++
  }
  ref:=strings.Join(e.Request.Headers["Referer"]," ")
  source:="Direct / unknown"
  if u,err:=url.Parse(ref);err==nil&&u.Hostname()!="" {
   if !strings.EqualFold(u.Hostname(),domain){source=u.Hostname()}
   if strings.EqualFold(u.Hostname(),domain){source="Internal"}
  }
  sources[source]++
 }
 scanErr:=scanner.Err()
 if out.Partial { _=cmd.Process.Kill() }
 waitErr:=cmd.Wait()
 if scanErr!=nil && !out.Partial{return out,scanErr}
 if waitErr!=nil&&!out.Partial&&ctx.Err()==nil{return out,fmt.Errorf("journalctl: %w",waitErr)}
 if ctx.Err()!=nil{return out,ctx.Err()}
 out.Visitors=len(ips)
 for d:=0;d<days;d++ {day:=from.AddDate(0,0,d).Format("2006-01-02");out.Days=append(out.Days,TrafficDay{Date:day,Views:dailyViews[day],Visitors:len(dailyIPs[day])})}
 out.Pages=topTraffic(pages)
 out.Entrances=topTraffic(entrances)
 out.Referrers=topTraffic(sources)
 return out,nil
}
func topTraffic(values map[string]int) []TrafficItem {
 items:=make([]TrafficItem,0,len(values))
 for name,n:=range values {items=append(items,TrafficItem{Name:name,Count:n})}
 sort.Slice(items,func(i,j int)bool{if items[i].Count==items[j].Count{return items[i].Name<items[j].Name};return items[i].Count>items[j].Count})
 if len(items)>15 {items=items[:15]}
 return items
}
