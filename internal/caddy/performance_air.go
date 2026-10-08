package caddy

import (
 "bufio"
 "context"
 "database/sql"
 "encoding/json"
 "fmt"
 "os/exec"
 "regexp"
 "strconv"
 "strings"
 "time"
)

var airANSIEscape = regexp.MustCompile("\x1b\\[[0-9;]*[a-zA-Z]")

// An Air child-run event is recorded only after observing Air's run marker in
// the journal of an app explicitly configured with RunMode=go-air.
// It is labelled separately from a real systemd process start.
func (m *Manager) ObserveAirRuns(ctx context.Context, targets []ServiceTarget) error {
 for i,target:=range targets {
  if i>=15{break} // keep per-cycle process creation tightly bounded
  if err:=ctx.Err();err!=nil{return err}
  if target.AppID<=0{continue}
  driver:=fmt.Sprintf("air:%d",target.AppID)
  var cursor string
  err:=m.store.DB().QueryRowContext(ctx,"SELECT cursor FROM http_perf_cursor WHERE driver=?",driver).Scan(&cursor)
  if err!=nil && err!=sql.ErrNoRows{return err}
  service:=fmt.Sprintf("open-go-panel-app-%d.service",target.AppID)
  namespace:=fmt.Sprintf("ogp-app-%d",target.AppID)
  args:=[]string{"-o","json","--no-pager","-u",service,"--namespace="+namespace}
  if cursor!=""{args=append(args,"--after-cursor",cursor)}else{args=append(args,"--since","7 days ago")}
  cmd:=exec.CommandContext(ctx,"journalctl",args...)
  stdout,err:=cmd.StdoutPipe()
  if err!=nil{continue}
  if err=cmd.Start();err!=nil{continue}
  type record struct{
   Cursor string `json:"__CURSOR"`
   Message string `json:"MESSAGE"`
   Timestamp string `json:"__REALTIME_TIMESTAMP"`
  }
  var collected []int64
  var last string
  scanned:=0
  parser:=bufio.NewScanner(stdout)
  parser.Buffer(make([]byte,16*1024),512*1024)
  limited:=false
  for parser.Scan(){
   var msg record
   if json.Unmarshal(parser.Bytes(),&msg)!=nil || msg.Cursor==""{continue}
   last=msg.Cursor
   scanned++
   cleaned:=strings.TrimSpace(airANSIEscape.ReplaceAllString(msg.Message,""))
   // Air's own log format is typically "[hh:mm:ss] running...".
   if cleaned=="running..." || strings.HasSuffix(cleaned,"] running..."){
    micros,err:=strconv.ParseInt(msg.Timestamp,10,64)
    if err==nil && micros>0{collected=append(collected,micros*1000)}
   }
   if scanned>=200{limited=true;break}
  }
  parseErr:=parser.Err()
  if limited{_ = cmd.Process.Kill()}
  _ = stdout.Close()
  waitErr:=cmd.Wait()
  if parseErr!=nil{return parseErr}
  if waitErr!=nil&&!limited{
   // Missing optional app namespace is not an error for Caddy or the panel.
   continue
  }
  if last==""{continue}
  tx,err:=m.store.DB().BeginTx(ctx,nil)
  if err!=nil{return err}
  for _,startedNS:=range collected{
   if startedNS>time.Now().Add(time.Minute).UnixNano(){continue}
   _,err=tx.ExecContext(ctx,`INSERT OR IGNORE INTO http_perf_restarts
    (app_id,started_ns,service,source,reason) VALUES(?,?,?,'air-log','unknown')`,
    target.AppID,startedNS,service)
   if err!=nil{break}
  }
  if err==nil{
   _,err=tx.ExecContext(ctx,`INSERT INTO http_perf_cursor(driver,cursor,scanned,last_success_ns)
    VALUES(?,?,?,?) ON CONFLICT(driver) DO UPDATE SET
    cursor=excluded.cursor,scanned=scanned+excluded.scanned,last_success_ns=excluded.last_success_ns`,
    driver,last,int64(scanned),time.Now().UnixNano())
  }
  if err!=nil{_ = tx.Rollback();return err}
  if err=tx.Commit();err!=nil{return err}
 }
 return nil
}
