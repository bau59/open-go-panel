package caddy

import (
 "bufio"
 "context"
 "database/sql"
 "encoding/json"
 "errors"
 "fmt"
 "os/exec"
 "os"
 "regexp"
 "strconv"
 "strings"
 "time"

 "github.com/bau59/open-go-panel/internal/state"
)

const (
 performancePollLimit = 1000
 performanceStorageMaxRows = 200000
)

type CollectorStatus struct {
 Source string
 Backlog bool
 Scanned, ParseErrors, Dropped int64
 LastSuccess time.Time
 LastError string
 Stored int64
 SQLiteBytes int64
}

var allowedRequestID = regexp.MustCompile("^[A-Za-z0-9_.-]{1,128}$")

func (m *Manager) PerformanceCollectorStatus(ctx context.Context) (CollectorStatus,error) {
 var s CollectorStatus
 var timestamp int64
 var backlog int
 source,ok,readErr:=m.store.Setting("performance.source")
 if readErr!=nil{return s,readErr}
 if !ok||source!="file"{source="journal"}
 s.Source=source
 err:=m.store.DB().QueryRowContext(ctx,`SELECT scanned,parse_errors,dropped,last_success_ns,last_error,backlog
 FROM http_perf_cursor WHERE driver=?`,source).Scan(&s.Scanned,&s.ParseErrors,&s.Dropped,&timestamp,&s.LastError,&backlog)
 s.Backlog=backlog!=0
 if err!=nil && !errors.Is(err,sql.ErrNoRows){return s,err}
 if timestamp>0{s.LastSuccess=time.Unix(0,timestamp)}
 if err=m.store.DB().QueryRowContext(ctx,"SELECT count(*) FROM http_perf_requests").Scan(&s.Stored);err!=nil{return s,err}
 var seq int
 var schemaName,dbPath string
 if err=m.store.DB().QueryRowContext(ctx,"PRAGMA database_list").Scan(&seq,&schemaName,&dbPath);err==nil && dbPath!="" {
  for _,path:=range []string{dbPath,dbPath+"-wal",dbPath+"-shm"} {
   if stat,statErr:=os.Stat(path);statErr==nil{s.SQLiteBytes+=stat.Size()}
  }
 }
 return s,nil
}

func (m *Manager) CollectPerformanceJournal(ctx context.Context) error {
 var cursor string
 var lastOK int64
 err:=m.store.DB().QueryRowContext(ctx,`SELECT cursor,last_success_ns FROM http_perf_cursor WHERE driver='journal'`).Scan(&cursor,&lastOK)
 if err!=nil && !errors.Is(err,sql.ErrNoRows){return err}
 args:=[]string{"-u","caddy.service","-o","json","--no-pager"}
 if cursor!="" {args=append(args,"--after-cursor",cursor)} else {
  since:=time.Now().Add(-7*24*time.Hour)
  if lastOK>0{since=time.Unix(0,lastOK).Add(-5*time.Minute)}
  args=append(args,"--since",since.Format("2006-01-02 15:04:05"))
 }
 cmd:=exec.CommandContext(ctx,"journalctl",args...)
 stdout,err:=cmd.StdoutPipe()
 if err!=nil{return err}
 var stderr strings.Builder
 cmd.Stderr=&stderr
 if err=cmd.Start();err!=nil{return err}
 type event struct {
  Cursor string `json:"__CURSOR"`
  Message string `json:"MESSAGE"`
 }
 type pending struct {key string; p PerformancePoint; appID int64}
 var batch []pending
 var latestCursor string
 var scanned,parseErrors int64
 scanner:=bufio.NewScanner(stdout)
 scanner.Buffer(make([]byte,16*1024),2*1024*1024)
 sites,siteErr:=m.Sites()
 if siteErr!=nil{_ = cmd.Process.Kill();_ = cmd.Wait();return siteErr}
 appByDomain:=make(map[string]int64,len(sites))
 for _,site:=range sites{if site.AppID>0{appByDomain[strings.ToLower(site.Domain)]=site.AppID}}
 limitHit:=false
 for scanner.Scan(){
  var line event
  if err=json.Unmarshal(scanner.Bytes(),&line);err!=nil {
   parseErrors++;continue
  }
  if line.Cursor==""{parseErrors++;continue}
  latestCursor=line.Cursor
  scanned++
  if strings.TrimSpace(line.Message)!="" && strings.Contains(line.Message,`"http.log.`){
   p,ok:=parseCaddyLog(line.Message)
   if !ok{parseErrors++;continue}
   if p.Kind!="access"{continue}
   var raw struct{
    Duration *float64 `json:"duration"`
    RequestID string `json:"request_id"`
    RespHeaders map[string][]string `json:"resp_headers"`
   }
   if json.Unmarshal([]byte(p.Raw),&raw)!=nil || raw.Duration==nil ||
      *raw.Duration<0 || *raw.Duration>86400 {parseErrors++;continue}
   id:=""
   if allowedRequestID.MatchString(raw.RequestID){id=raw.RequestID}
   var headerValues []string
   for key,values:=range raw.RespHeaders{
    if strings.EqualFold(key,"Server-Timing"){headerValues=append(headerValues,values...)}
   }
   domain:=strings.ToLower(strings.TrimSpace(p.Domain))
   if strings.Contains(domain,":"){domain,_,_=strings.Cut(domain,":")}
   if len(domain)>253 || !domainRE.MatchString(domain){continue}
   route:=cleanPerformanceRoute(p.URI)
   batch=append(batch,pending{key:line.Cursor,appID:appByDomain[domain],p:PerformancePoint{
    Time:p.Time,Domain:domain,Method:p.Method,Route:route,Protocol:p.Protocol,
    Status:p.Status,DurationMS:p.DurationMS,Size:p.Size,RequestID:id,
    ServerTimings:parseServerTiming(headerValues),
   }})
  }
  if scanned>=performancePollLimit{limitHit=true;break}
 }
 scanErr:=scanner.Err()
 if limitHit{_ = cmd.Process.Kill()}
 _ = stdout.Close()
 waitErr:=cmd.Wait()
 if scanErr!=nil{return m.setCollectorError(ctx,scanErr)}
 if waitErr!=nil&&!limitHit{
  return m.setCollectorError(ctx,fmt.Errorf("journalctl: %w: %s",waitErr,strings.TrimSpace(stderr.String())))
 }
 if ctx.Err()!=nil{return ctx.Err()}
 if latestCursor==""{return nil}
 tx,err:=m.store.DB().BeginTx(ctx,nil)
 if err!=nil{return err}
 defer tx.Rollback()
 for _,entry:=range batch{
  timingJSON,_:=json.Marshal(entry.p.ServerTimings)
  inserted,err:=tx.ExecContext(ctx,`INSERT OR IGNORE INTO http_perf_requests
   (source_key,time_ns,app_id,domain,method,route,protocol,status,duration_ms,response_bytes,request_id,server_timings)
   VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
   entry.key,entry.p.Time.UnixNano(),entry.appID,entry.p.Domain,entry.p.Method,entry.p.Route,
   entry.p.Protocol,entry.p.Status,entry.p.DurationMS,entry.p.Size,entry.p.RequestID,string(timingJSON))
  if err!=nil{return err}
  affected,_:=inserted.RowsAffected()
  if affected==0{continue}
  _,err=tx.ExecContext(ctx,`INSERT INTO http_perf_rollup
   (hour_ns,domain,app_id,method,route,requests,total_ms,errors_5xx,slow_500,max_ms)
   VALUES(?,?,?,?,?,1,?,?,?,?)
   ON CONFLICT(hour_ns,domain,app_id,method,route) DO UPDATE SET
    requests=requests+1,total_ms=total_ms+excluded.total_ms,
    errors_5xx=errors_5xx+excluded.errors_5xx,
    slow_500=slow_500+excluded.slow_500,
    max_ms=MAX(max_ms,excluded.max_ms)`,
   entry.p.Time.Truncate(time.Hour).UnixNano(),entry.p.Domain,entry.appID,entry.p.Method,entry.p.Route,
   entry.p.DurationMS,boolInt(entry.p.Status>=500),boolInt(entry.p.DurationMS>=500),entry.p.DurationMS)
  if err!=nil{return err}
 }
 _,err=tx.ExecContext(ctx,`INSERT INTO http_perf_cursor(driver,cursor,scanned,parse_errors,last_success_ns,last_error,backlog)
   VALUES('journal',?,?,?,?,'',?)
   ON CONFLICT(driver) DO UPDATE SET cursor=excluded.cursor,
   scanned=scanned+excluded.scanned,parse_errors=parse_errors+excluded.parse_errors,
   last_success_ns=excluded.last_success_ns,last_error='',backlog=excluded.backlog`,
   latestCursor,scanned,parseErrors,time.Now().UnixNano(),boolInt(limitHit))
 if err!=nil{return err}
 return tx.Commit()
}

func boolInt(value bool)int{if value{return 1};return 0}

func (m *Manager) setCollectorError(ctx context.Context,source error)error{
 msg:=source.Error()
 if len(msg)>300{msg=msg[:300]}
 _,_ = m.store.DB().ExecContext(ctx,`INSERT INTO http_perf_cursor(driver,last_error)
 VALUES('journal',?) ON CONFLICT(driver) DO UPDATE SET last_error=excluded.last_error`,msg)
 return source
}

func settingDays(store *state.Store,key string,fallback int)int{
 v,ok,err:=store.Setting(key)
 if err!=nil||!ok{return fallback}
 n,err:=strconv.Atoi(v)
 if err!=nil||n<1||n>90{return fallback}
 return n
}

func (m *Manager) PrunePerformance(ctx context.Context) error {
 details:=settingDays(m.store,"performance.detail_days",7)
 aggregates:=settingDays(m.store,"performance.aggregate_days",30)
 tx,err:=m.store.DB().BeginTx(ctx,nil)
 if err!=nil{return err}
 defer tx.Rollback()
 cutoff:=time.Now().AddDate(0,0,-details).UnixNano()
 if _,err=tx.ExecContext(ctx,"DELETE FROM http_perf_requests WHERE time_ns < ?",cutoff);err!=nil{return err}
 if _,err=tx.ExecContext(ctx,"DELETE FROM http_perf_rollup WHERE hour_ns < ?",time.Now().AddDate(0,0,-aggregates).UnixNano());err!=nil{return err}
 if _,err=tx.ExecContext(ctx,"DELETE FROM http_perf_restarts WHERE started_ns < ?",time.Now().AddDate(0,0,-aggregates).UnixNano());err!=nil{return err}
 if _,err=tx.ExecContext(ctx,"DELETE FROM http_perf_lifecycle WHERE event_ns < ?",time.Now().AddDate(0,0,-aggregates).UnixNano());err!=nil{return err}
 var count int64
 if err=tx.QueryRowContext(ctx,"SELECT count(*) FROM http_perf_requests").Scan(&count);err!=nil{return err}
 if count>performanceStorageMaxRows {
  deleteCount:=count-performanceStorageMaxRows
  if _,err=tx.ExecContext(ctx,`DELETE FROM http_perf_requests
   WHERE id IN (SELECT id FROM http_perf_requests ORDER BY time_ns ASC LIMIT ?)`,deleteCount);err!=nil{return err}
  if _,err=tx.ExecContext(ctx,`INSERT INTO http_perf_cursor(driver,dropped)
   VALUES('journal',?) ON CONFLICT(driver) DO UPDATE SET dropped=dropped+excluded.dropped`,deleteCount);err!=nil{return err}
 }
 var rollupCount int64
 if err=tx.QueryRowContext(ctx,"SELECT count(*) FROM http_perf_rollup").Scan(&rollupCount);err!=nil{return err}
 if rollupCount>performanceStorageMaxRows{
  if _,err=tx.ExecContext(ctx,`DELETE FROM http_perf_rollup WHERE rowid IN (
    SELECT rowid FROM http_perf_rollup ORDER BY hour_ns ASC LIMIT ?)`,
    rollupCount-performanceStorageMaxRows);err!=nil{return err}
 }
 return tx.Commit()
}
