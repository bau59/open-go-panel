package caddy

import (
 "context"
 "database/sql"
 "fmt"
 "os/exec"
 "strings"
 "time"
)

type ServiceTarget struct {
 AppID int64
 Name string
}

type ProcessStart struct {
 AppID int64
 Service string
 Time time.Time
 Source string
 Reason string
}

type ColdSample struct {
 AppID int64
 Service string
 Started time.Time
 FirstTime time.Time
 FirstRoute string
 FirstDurationMS float64
 FirstMinuteRequests int
 FirstMinuteAverageMS float64
 LaterSameRouteCount int
 LaterSameRouteAverageMS float64
}

// ObserveProcessStarts obtains actual systemd ExecMainStartTimestamp values.
// This does not pretend to see Air child rebuilds or unknown termination reasons.
func (m *Manager) ObserveProcessStarts(ctx context.Context, targets []ServiceTarget) error {
 for i,target:=range targets {
  if i>=80 || ctx.Err()!=nil{break}
  if target.AppID<=0{continue}
  service:=fmt.Sprintf("open-go-panel-app-%d.service",target.AppID)
  out,err:=exec.CommandContext(ctx,"systemctl","show","--property=ExecMainStartTimestamp","--value",service).Output()
  if err!=nil{continue}
  value:=strings.TrimSpace(string(out))
  if value==""||value=="n/a"{continue}
  started,err:=time.Parse("Mon 2006-01-02 15:04:05 MST",value)
  if err!=nil || started.IsZero() || started.After(time.Now().Add(time.Minute)) ||
   started.Before(time.Now().AddDate(0,0,-30)){continue}
  if _,err=m.store.DB().ExecContext(ctx,`INSERT OR IGNORE INTO http_perf_restarts
   (app_id,started_ns,service,source,reason) VALUES(?,?,?,'systemd','unknown')`,
   target.AppID,started.UnixNano(),service);err!=nil{return err}
 }
 return ctx.Err()
}

func (m *Manager) PerformanceRestarts(ctx context.Context,since,until time.Time) ([]ProcessStart,error){
 rows,err:=m.store.DB().QueryContext(ctx,`SELECT app_id,started_ns,service,source,reason
 FROM http_perf_restarts WHERE started_ns>=? AND started_ns<=? ORDER BY started_ns`,since.UnixNano(),until.UnixNano())
 if err!=nil{return nil,err}
 defer rows.Close()
 var result []ProcessStart
 for rows.Next(){
  var event ProcessStart
  var nano int64
  if err:=rows.Scan(&event.AppID,&nano,&event.Service,&event.Source,&event.Reason);err!=nil{return nil,err}
  event.Time=time.Unix(0,nano)
  result=append(result,event)
 }
 return result,rows.Err()
}

// ColdStartupSamples only considers requests after a confirmed process start.
// No result is emitted if no matching stored request exists.
func (m *Manager) ColdStartupSamples(ctx context.Context,since,until time.Time)([]ColdSample,error){
 events,err:=m.PerformanceRestarts(ctx,since,until)
 if err!=nil{return nil,err}
 var samples []ColdSample
 for _,event:=range events{
  if ctx.Err()!=nil{return nil,ctx.Err()}
  var s ColdSample
  s.AppID,s.Service,s.Started=event.AppID,event.Service,event.Time
  var firstNS int64
  err:=m.store.DB().QueryRowContext(ctx,`SELECT time_ns,route,duration_ms FROM http_perf_requests
   WHERE app_id=? AND time_ns>=? AND time_ns<? ORDER BY time_ns LIMIT 1`,
   event.AppID,event.Time.UnixNano(),event.Time.Add(time.Hour).UnixNano()).
   Scan(&firstNS,&s.FirstRoute,&s.FirstDurationMS)
  if err==sql.ErrNoRows{continue}
  if err!=nil{return nil,err}
  s.FirstTime=time.Unix(0,firstNS)
  err=m.store.DB().QueryRowContext(ctx,`SELECT count(*),coalesce(avg(duration_ms),0)
   FROM http_perf_requests WHERE app_id=? AND time_ns>=? AND time_ns<?`,
   event.AppID,event.Time.UnixNano(),event.Time.Add(time.Minute).UnixNano()).
   Scan(&s.FirstMinuteRequests,&s.FirstMinuteAverageMS)
  if err!=nil{return nil,err}
  err=m.store.DB().QueryRowContext(ctx,`SELECT count(*),coalesce(avg(duration_ms),0)
   FROM http_perf_requests WHERE app_id=? AND route=? AND time_ns>=? AND time_ns<?`,
   event.AppID,s.FirstRoute,event.Time.Add(time.Minute).UnixNano(),event.Time.Add(time.Hour).UnixNano()).
   Scan(&s.LaterSameRouteCount,&s.LaterSameRouteAverageMS)
  if err!=nil{return nil,err}
  samples=append(samples,s)
  if len(samples)>=50{break}
 }
 return samples,nil
}

type IdleSample struct {
 AppID int64
 Route string
 Time time.Time
 Gap time.Duration
 DurationMS float64
}

// Idle events are deliberately separate from process starts. They are only
// observed when consecutive recorded requests for the same app are 15m apart.
func (m *Manager) IdlePerformanceSamples(ctx context.Context,since,until time.Time)([]IdleSample,error){
 rows,err:=m.store.DB().QueryContext(ctx,`SELECT app_id,time_ns,route,duration_ms
 FROM http_perf_requests WHERE app_id>0 AND time_ns>=? AND time_ns<=?
 ORDER BY app_id,time_ns LIMIT 100001`,since.UnixNano(),until.UnixNano())
 if err!=nil{return nil,err}
 defer rows.Close()
 last:=map[int64]time.Time{}
 var samples []IdleSample
 count:=0
 for rows.Next(){
  var appID,nano int64
  var route string
  var duration float64
  if err:=rows.Scan(&appID,&nano,&route,&duration);err!=nil{return nil,err}
  count++
  if count>100000{break}
  when:=time.Unix(0,nano)
  if prior,known:=last[appID];known && when.Sub(prior)>=15*time.Minute{
   samples=append(samples,IdleSample{AppID:appID,Route:route,Time:when,Gap:when.Sub(prior),DurationMS:duration})
  }
  last[appID]=when
 }
 if err:=rows.Err();err!=nil{return nil,err}
 if count>100000 {return nil,fmt.Errorf("idle detection needs a narrower interval; more than 100,000 requests")}
 if len(samples)>50{samples=samples[len(samples)-50:]}
 return samples,nil
}
