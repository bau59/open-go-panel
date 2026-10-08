package caddy

import (
 "bufio"
 "context"
 "database/sql"
 "encoding/json"
 "errors"
 "fmt"
 "io"
 "os"
 "path/filepath"
 "strconv"
 "strings"
 "syscall"
 "time"
)

const performanceFileMaxBytes = 4 * 1024 * 1024

func ValidCaddyLogPath(path string) bool {
 if !filepath.IsAbs(path) || filepath.Clean(path)!=path ||
  !strings.HasPrefix(path,"/var/log/caddy/") || len(path)>240 {return false}
 resolved,err:=filepath.EvalSymlinks(path)
 if err==nil && !strings.HasPrefix(resolved,"/var/log/caddy/"){return false}
 return true
}

func fileIdentity(file *os.File)(string,error){
 info,err:=file.Stat()
 if err!=nil{return "",err}
 stat,ok:=info.Sys().(*syscall.Stat_t)
 if !ok{return "",fmt.Errorf("unsupported file metadata")}
 return fmt.Sprintf("%d:%d",stat.Dev,stat.Ino),nil
}

func (m *Manager) CollectConfiguredPerformance(ctx context.Context)error{
 source,ok,err:=m.store.Setting("performance.source")
 if err!=nil{return err}
 if !ok||source==""||source=="journal"{return m.CollectPerformanceJournal(ctx)}
 if source!="file"{return fmt.Errorf("unsupported metrics source %q",source)}
 path,ok,err:=m.store.Setting("performance.file_path")
 if err!=nil{return err}
 if !ok || !ValidCaddyLogPath(path){return fmt.Errorf("invalid Caddy access-log file path")}
 return m.CollectPerformanceFile(ctx,path)
}

func (m *Manager) CollectPerformanceFile(ctx context.Context,path string)error{
 if !ValidCaddyLogPath(path){return fmt.Errorf("unsafe Caddy log path")}
 return m.collectPerformanceFile(ctx,path)
}

func (m *Manager) collectPerformanceFile(ctx context.Context,path string)error{
 file,err:=os.Open(path)
 if err!=nil{return m.setPerformanceFileError(ctx,err)}
 defer func(){_ = file.Close()}()
 id,err:=fileIdentity(file)
 if err!=nil{return err}
 var prev string
 err=m.store.DB().QueryRowContext(ctx,`SELECT cursor FROM http_perf_cursor WHERE driver='file'`).Scan(&prev)
 if err!=nil && !errors.Is(err,sql.ErrNoRows){return err}
 previousID:=""
 var position,generation int64
 if parts:=strings.Split(prev,":");len(parts)>=3 {
  previousID=parts[0]+":"+parts[1]
  position,_=strconv.ParseInt(parts[2],10,64)
  if len(parts)>=4{generation,_=strconv.ParseInt(parts[3],10,64)}
 }
 gap:=int64(0)
 switched:=false
 if previousID!="" && previousID!=id {
  rotated,err:=os.Open(path+".1")
  if err==nil{
   rotatedID,idErr:=fileIdentity(rotated)
   if idErr==nil && rotatedID==previousID {
    file.Close()
    file=rotated
    id=rotatedID
    switched=true
   } else {_ = rotated.Close()}
  }
  if !switched{
   // File was rotated and no longer available. Report the gap instead of
   // silently pretending the stream remained continuous.
   gap++
   position=0
   generation=0
  }
 }
 info,err:=file.Stat()
 if err!=nil{return err}
 if position>info.Size(){
  position=0
  generation++ // same inode, new file content: prevent offset-key collisions
  gap++ // copytruncate or another unrecoverable discontinuity
 }
 if _,err=file.Seek(position,io.SeekStart);err!=nil{return err}
 sites,err:=m.Sites()
 if err!=nil{return err}
 associated:=map[string]int64{}
 for _,site:=range sites{if site.AppID>0{associated[strings.ToLower(site.Domain)]=site.AppID}}
 type point struct{key string;p PerformancePoint}
 var records []point
 read:=bufio.NewReaderSize(io.LimitReader(file,performanceFileMaxBytes),64*1024)
 var scanned,malformed int64
 bytesRead:=int64(0)
 reachedEOF:=false
 for scanned<performancePollLimit && bytesRead<performanceFileMaxBytes {
  line,readErr:=read.ReadBytes('\n')
  if readErr==io.EOF {reachedEOF=true;break} // never commit partial rows
  if readErr!=nil{return m.setPerformanceFileError(ctx,readErr)}
  offset:=position
  position+=int64(len(line))
  bytesRead+=int64(len(line))
  scanned++
  if len(line)>1024*1024{malformed++;continue}
  entry,ok:=parseCaddyLog(strings.TrimSpace(string(line)))
  if !ok{malformed++;continue}
  if entry.Kind!="access"{continue}
  if entry.Time.IsZero() || entry.Time.Before(time.Now().AddDate(0,0,-90)) ||
   entry.Time.After(time.Now().Add(time.Minute)){malformed++;continue}
  var raw struct {
   Duration *float64 `json:"duration"`
   RequestID string `json:"request_id"`
   RespHeaders map[string][]string `json:"resp_headers"`
  }
  if json.Unmarshal([]byte(entry.Raw),&raw)!=nil||raw.Duration==nil||
   *raw.Duration<0||*raw.Duration>86400{malformed++;continue}
  domain:=strings.ToLower(strings.TrimSpace(entry.Domain))
  if strings.Contains(domain,":"){domain,_,_=strings.Cut(domain,":")}
  if !domainRE.MatchString(domain){continue}
  requestID:=""
  if allowedRequestID.MatchString(raw.RequestID){requestID=raw.RequestID}
  var timing []string
  for header,values:=range raw.RespHeaders{
   if strings.EqualFold(header,"Server-Timing"){timing=append(timing,values...)}
  }
  key:=fmt.Sprintf("file:%s:%d",id,offset)
  if generation>0{key+=fmt.Sprintf(":g%d",generation)}
  records=append(records,point{
   key:key,
   p:PerformancePoint{Time:entry.Time,Domain:domain,AppID:associated[domain],
    Method:entry.Method,Route:cleanPerformanceRoute(entry.URI),
    Protocol:entry.Protocol,Status:entry.Status,DurationMS:entry.DurationMS,
    Size:entry.Size,RequestID:requestID,ServerTimings:parseServerTiming(timing)},
  })
 }
 if ctx.Err()!=nil{return ctx.Err()}
 if switched && reachedEOF{
  // We have read through the old inode; begin the new file next poll.
  newFile,err:=os.Open(path)
  if err==nil{
   nextID,idErr:=fileIdentity(newFile)
   _=newFile.Close()
   if idErr==nil{ id=nextID;position=0;generation=0 }
  }
 }
 backlog:=false
 if !switched || !reachedEOF{
  if remaining,statErr:=file.Stat();statErr==nil&&position<remaining.Size(){backlog=true}
 }
 checkpoint:=fmt.Sprintf("%s:%d:%d",id,position,generation)
 tx,err:=m.store.DB().BeginTx(ctx,nil)
 if err!=nil{return err}
 defer tx.Rollback()
 for _,row:=range records{
  timing,_:=json.Marshal(row.p.ServerTimings)
  inserted,err:=tx.ExecContext(ctx,`INSERT OR IGNORE INTO http_perf_requests
   (source_key,time_ns,app_id,domain,method,route,protocol,status,duration_ms,response_bytes,request_id,server_timings)
   VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
   row.key,row.p.Time.UnixNano(),row.p.AppID,row.p.Domain,row.p.Method,row.p.Route,
   row.p.Protocol,row.p.Status,row.p.DurationMS,row.p.Size,row.p.RequestID,string(timing))
  if err!=nil{return err}
  count,_:=inserted.RowsAffected()
  if count==0{continue}
  _,err=tx.ExecContext(ctx,`INSERT INTO http_perf_rollup
   (hour_ns,domain,app_id,method,route,requests,total_ms,errors_5xx,slow_500,max_ms)
   VALUES(?,?,?,?,?,1,?,?,?,?)
   ON CONFLICT(hour_ns,domain,app_id,method,route) DO UPDATE SET
   requests=requests+1,total_ms=total_ms+excluded.total_ms,
   errors_5xx=errors_5xx+excluded.errors_5xx,
   slow_500=slow_500+excluded.slow_500,max_ms=MAX(max_ms,excluded.max_ms)`,
   row.p.Time.Truncate(time.Hour).UnixNano(),row.p.Domain,row.p.AppID,row.p.Method,row.p.Route,
   row.p.DurationMS,boolInt(row.p.Status>=500),boolInt(row.p.DurationMS>=500),row.p.DurationMS)
  if err!=nil{return err}
 }
 _,err=tx.ExecContext(ctx,`INSERT INTO http_perf_cursor
 (driver,cursor,scanned,parse_errors,dropped,last_success_ns,last_error,backlog)
 VALUES('file',?,?,?,?,?,'',?)
 ON CONFLICT(driver) DO UPDATE SET
 cursor=excluded.cursor,scanned=scanned+excluded.scanned,
 parse_errors=parse_errors+excluded.parse_errors,dropped=dropped+excluded.dropped,
 last_success_ns=excluded.last_success_ns,last_error='',backlog=excluded.backlog`,
 checkpoint,scanned,malformed,gap,time.Now().UnixNano(),boolInt(backlog))
 if err!=nil{return err}
 return tx.Commit()
}

func (m *Manager) setPerformanceFileError(ctx context.Context,source error)error{
 msg:=source.Error()
 if len(msg)>300{msg=msg[:300]}
 _,_ = m.store.DB().ExecContext(ctx,`INSERT INTO http_perf_cursor(driver,last_error)
 VALUES('file',?) ON CONFLICT(driver) DO UPDATE SET last_error=excluded.last_error`,msg)
 return source
}
