package caddy

import (
 "bufio"
 "context"
 "encoding/json"
 "fmt"
 "io"
 "math"
 "os"
 "os/exec"
 "strings"
 "time"
)

const (
 reportMaxScanned = 50000
 reportFileTailBytes int64 = 32 * 1024 * 1024
)

func (m *Manager) QueryPerformanceFromLogs(ctx context.Context, f PerformanceFilter, source, path string) (PerformanceResult,error) {
 if f.Since.IsZero() || f.Until.IsZero() || !f.Since.Before(f.Until) || f.Until.Sub(f.Since)>31*24*time.Hour {
  return PerformanceResult{},fmt.Errorf("invalid report interval")
 }
 if f.ThresholdMS<=0 {f.ThresholdMS=500}
 if f.Page<1 {f.Page=1}
 if f.PerPage<=0||f.PerPage>200 {f.PerPage=50}
 records:=make([]PerformancePoint,0,1024)
 scanned:=0
 truncated:=false
 add:=func(raw string){
  scanned++
  entry,ok:=parseCaddyLog(raw)
  if !ok||entry.Kind!="access"||entry.Time.Before(f.Since)||entry.Time.After(f.Until){return}
  var timing struct {
   Duration *float64 `json:"duration"`
   RequestID string `json:"request_id"`
   RespHeaders map[string][]string `json:"resp_headers"`
  }
  if json.Unmarshal([]byte(entry.Raw),&timing)!=nil||timing.Duration==nil||
    math.IsNaN(*timing.Duration)||math.IsInf(*timing.Duration,0)||
    *timing.Duration<0 || *timing.Duration>86400{return}
  route:=cleanPerformanceRoute(entry.URI)
  if f.Domain!="" && !strings.EqualFold(entry.Domain,f.Domain){return}
  if f.Method!="" && !strings.EqualFold(entry.Method,f.Method){return}
  if f.Route!="" && !strings.Contains(strings.ToLower(route),strings.ToLower(f.Route)){return}
  if f.Status>0 && entry.Status/100!=f.Status/100{return}
  if f.SlowOnly && entry.DurationMS<f.ThresholdMS{return}
  if len(records)>=performanceMaxRecords {truncated=true;return}
  var timingValues []string
  for name,vals:=range timing.RespHeaders {if strings.EqualFold(name,"Server-Timing"){timingValues=append(timingValues,vals...)}}
  requestID:=""
  if allowedRequestID.MatchString(timing.RequestID){requestID=timing.RequestID}
  records=append(records,PerformancePoint{
   Time:entry.Time,Domain:entry.Domain,Method:entry.Method,Route:route,
   Protocol:entry.Protocol,Status:entry.Status,DurationMS:entry.DurationMS,
   Size:entry.Size,RequestID:requestID,ServerTimings:parseServerTiming(timingValues),
  })
 }
 switch source{
 case "", "journal":
  cmd:=exec.CommandContext(ctx,"journalctl","-u","caddy.service","-o","json","-r","--no-pager",
   "--since",f.Since.Local().Format("2006-01-02 15:04:05"),
   "--until",f.Until.Local().Format("2006-01-02 15:04:05"))
  out,err:=cmd.StdoutPipe();if err!=nil{return PerformanceResult{},err}
  var stderr strings.Builder
  cmd.Stderr=&stderr
  if err=cmd.Start();err!=nil{return PerformanceResult{},err}
  scanner:=bufio.NewScanner(out)
  scanner.Buffer(make([]byte,64*1024),2*1024*1024)
  bounded:=false
  for scanner.Scan(){
   var row journalEnvelope
   if json.Unmarshal(scanner.Bytes(),&row)==nil{add(row.Message)}
   if scanned>=reportMaxScanned || truncated {bounded=true;truncated=true;break}
  }
  scanErr:=scanner.Err()
  if bounded && cmd.Process!=nil{_ = cmd.Process.Kill()}
  _ = out.Close()
  waitErr:=cmd.Wait()
  if scanErr!=nil{return PerformanceResult{},scanErr}
  if ctx.Err()!=nil{return PerformanceResult{},ctx.Err()}
  if waitErr!=nil&&!bounded{
   return PerformanceResult{},fmt.Errorf("read Caddy journal: %w: %s",waitErr,strings.TrimSpace(stderr.String()))
  }
 case "file":
  if !ValidCaddyLogPath(path){return PerformanceResult{},fmt.Errorf("Caddy JSON file must be under /var/log/caddy/")}
  file,err:=os.Open(path);if err!=nil{return PerformanceResult{},err}
  defer file.Close()
  info,err:=file.Stat();if err!=nil{return PerformanceResult{},err}
  from:=info.Size()-reportFileTailBytes
  if from<0{from=0}else{truncated=true}
  if _,err=file.Seek(from,io.SeekStart);err!=nil{return PerformanceResult{},err}
  scanner:=bufio.NewScanner(io.LimitReader(file,reportFileTailBytes))
  scanner.Buffer(make([]byte,64*1024),2*1024*1024)
  if from>0 {scanner.Scan()} // Ignore first potentially partial JSONL entry.
  for scanner.Scan(){
   add(scanner.Text())
   if scanned>=reportMaxScanned||truncated&&len(records)>=performanceMaxRecords{truncated=true;break}
  }
  if err=scanner.Err();err!=nil{return PerformanceResult{},err}
  if ctx.Err()!=nil{return PerformanceResult{},ctx.Err()}
 default:
  return PerformanceResult{},fmt.Errorf("unknown Caddy log source")
 }
 return buildPerformanceResult(records,f,truncated),nil
}
