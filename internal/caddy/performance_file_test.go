package caddy

import (
 "context"
 "fmt"
 "os"
 "path/filepath"
 "strings"
 "testing"
 "time"

 "github.com/bau59/open-go-panel/internal/state"
)

func fixtureAccessLine(ts int64,uri string)string {
 return fmt.Sprintf(`{"level":"info","logger":"http.log.access","ts":%d.1,"request":{"host":"example.com","method":"GET","uri":%q,"proto":"HTTP/2.0"},"status":200,"duration":0.03535112,"size":9728,"resp_headers":{"Server-Timing":["db;dur=8.2"]}}`,ts,uri)+"\n"
}

func TestPerformanceFileResumeDedupAndRotation(t *testing.T){
 db,err:=state.Open(filepath.Join(t.TempDir(),"state.db"))
 if err!=nil{t.Fatal(err)}
 defer db.Close()
 m:=New(db,filepath.Join(t.TempDir(),"legacy.json"))
 path:=filepath.Join(t.TempDir(),"access.log")
 now:=time.Now().Unix()
 if err:=os.WriteFile(path,[]byte(fixtureAccessLine(now,"/users/123?token=s3cr3t")),0600);err!=nil{t.Fatal(err)}
 ctx:=context.Background()
 for i:=0;i<2;i++{
  if err:=m.collectPerformanceFile(ctx,path);err!=nil{t.Fatal(err)}
 }
 count:=func()int{
  var n int
  if err:=db.DB().QueryRow("SELECT count(*) FROM http_perf_requests").Scan(&n);err!=nil{t.Fatal(err)}
  return n
 }
 if got:=count();got!=1{t.Fatalf("expected one idempotent row, got %d",got)}
 var route string
 var ms float64
 if err:=db.DB().QueryRow("SELECT route,duration_ms FROM http_perf_requests").Scan(&route,&ms);err!=nil{t.Fatal(err)}
 if route!="/users/{id}" || ms<35.35 || ms>35.36{t.Fatalf("route=%q duration=%v",route,ms)}
 if err:=os.Rename(path,path+".1");err!=nil{t.Fatal(err)}
 if err:=os.WriteFile(path,[]byte(fixtureAccessLine(now+1,"/api/health")),0600);err!=nil{t.Fatal(err)}
 for i:=0;i<2;i++{
  if err:=m.collectPerformanceFile(ctx,path);err!=nil{t.Fatal(err)}
 }
 if got:=count();got!=2{t.Fatalf("expected new file ingested once after rotation, got %d",got)}
}

func TestPerformanceFileWaitsForCompleteLine(t *testing.T){
 db,err:=state.Open(filepath.Join(t.TempDir(),"state.db"))
 if err!=nil{t.Fatal(err)}
 defer db.Close()
 m:=New(db,filepath.Join(t.TempDir(),"legacy.json"))
 path:=filepath.Join(t.TempDir(),"access.log")
 incomplete:=strings.TrimSuffix(fixtureAccessLine(time.Now().Unix(),"/api/health"),"\n")
 if err:=os.WriteFile(path,[]byte(incomplete),0600);err!=nil{t.Fatal(err)}
 if err:=m.collectPerformanceFile(context.Background(),path);err!=nil{t.Fatal(err)}
 var count int
 if err:=db.DB().QueryRow("SELECT count(*) FROM http_perf_requests").Scan(&count);err!=nil{t.Fatal(err)}
 if count!=0{t.Fatal("partial JSONL line was ingested")}
 f,err:=os.OpenFile(path,os.O_APPEND|os.O_WRONLY,0600)
 if err!=nil{t.Fatal(err)}
 if _,err=f.WriteString("\n");err!=nil{t.Fatal(err)}
 _=f.Close()
 if err:=m.collectPerformanceFile(context.Background(),path);err!=nil{t.Fatal(err)}
 if err:=db.DB().QueryRow("SELECT count(*) FROM http_perf_requests").Scan(&count);err!=nil{t.Fatal(err)}
 if count!=1{t.Fatalf("expected completed line, got %d",count)}
}

func TestSensitivePerformanceRoutes(t *testing.T){
 for input,want:=range map[string]string{
  "/reset/top-secret":"/reset/{redacted}",
  "/accounts/verylongrandomtoken1234567890":"/accounts/{redacted}",
  "/users/456?password=secret":"/users/{id}",
 }{
  if got:=cleanPerformanceRoute(input);got!=want{t.Errorf("%q -> %q, want %q",input,got,want)}
 }
}

func TestPerformanceFileCopyTruncateDoesNotReuseOldOffset(t *testing.T){
 db,err:=state.Open(filepath.Join(t.TempDir(),"state.db"))
 if err!=nil{t.Fatal(err)}
 defer db.Close()
 manager:=New(db,filepath.Join(t.TempDir(),"legacy.json"))
 path:=filepath.Join(t.TempDir(),"access.log")
 stamp:=time.Now().Unix()
 original:=fixtureAccessLine(stamp,"/v1/orders/1")+fixtureAccessLine(stamp+1,"/v1/orders/2")
 if err:=os.WriteFile(path,[]byte(original),0600);err!=nil{t.Fatal(err)}
 if err:=manager.collectPerformanceFile(context.Background(),path);err!=nil{t.Fatal(err)}
 if err:=os.Truncate(path,0);err!=nil{t.Fatal(err)}
 if err:=os.WriteFile(path,[]byte(fixtureAccessLine(stamp+2,"/health")),0600);err!=nil{t.Fatal(err)}
 if err:=manager.collectPerformanceFile(context.Background(),path);err!=nil{t.Fatal(err)}
 var rows,loss int64
 if err:=db.DB().QueryRow("SELECT count(*) FROM http_perf_requests").Scan(&rows);err!=nil{t.Fatal(err)}
 if err:=db.DB().QueryRow("SELECT dropped FROM http_perf_cursor WHERE driver='file'").Scan(&loss);err!=nil{t.Fatal(err)}
 if rows!=3||loss!=1{t.Fatalf("copytruncate lost new line or was not reported: rows=%d gap=%d",rows,loss)}
}

func TestPerformanceFileBatchCursorNoLoss(t *testing.T){
 db,err:=state.Open(filepath.Join(t.TempDir(),"state.db"))
 if err!=nil{t.Fatal(err)}
 defer db.Close()
 manager:=New(db,filepath.Join(t.TempDir(),"legacy.json"))
 path:=filepath.Join(t.TempDir(),"access.log")
 now:=time.Now().Unix()
 var data strings.Builder
 for i:=0;i<1050;i++{
  data.WriteString(fixtureAccessLine(now,"/page/"+fmt.Sprintf("%d",i)))
 }
 if err:=os.WriteFile(path,[]byte(data.String()),0600);err!=nil{t.Fatal(err)}
 for i:=0;i<3;i++{
  if err:=manager.collectPerformanceFile(context.Background(),path);err!=nil{t.Fatal(err)}
 }
 var count int
 if err:=db.DB().QueryRow("SELECT count(*) FROM http_perf_requests").Scan(&count);err!=nil{t.Fatal(err)}
 if count!=1050{t.Fatalf("expected 1050 unique requests after batch resume, got %d",count)}
 result,err:=manager.QueryPerformance(context.Background(),PerformanceFilter{
  Since:time.Now().Add(-time.Hour),Until:time.Now().Add(time.Hour),Page:1,
 })
 if err!=nil{t.Fatal(err)}
 if result.Total!=1050 || result.Average<35.35 || result.Average>35.36{
  t.Fatalf("unexpected saved history: count=%d avg=%f",result.Total,result.Average)
 }
}

func TestPerformanceRetentionPrunesOldRequestsAndRollups(t *testing.T){
 db,err:=state.Open(filepath.Join(t.TempDir(),"state.db"))
 if err!=nil{t.Fatal(err)}
 defer db.Close()
 manager:=New(db,filepath.Join(t.TempDir(),"legacy.json"))
 old:=time.Now().Add(-40*24*time.Hour).UnixNano()
 _,err=db.DB().Exec(`INSERT INTO http_perf_requests
 (source_key,time_ns,domain,method,route,status,duration_ms,response_bytes)
 VALUES('expired',?,'example.com','GET','/old',200,42.5,20)`,old)
 if err!=nil{t.Fatal(err)}
 _,err=db.DB().Exec(`INSERT INTO http_perf_rollup
 (hour_ns,domain,method,route,requests,total_ms)
 VALUES(?,'example.com','GET','/old',1,42.5)`,old)
 if err!=nil{t.Fatal(err)}
 if err:=manager.PrunePerformance(context.Background());err!=nil{t.Fatal(err)}
 var count int
 for _,table:=range []string{"http_perf_requests","http_perf_rollup"}{
  if err:=db.DB().QueryRow("SELECT count(*) FROM "+table).Scan(&count);err!=nil{t.Fatal(err)}
  if count!=0{t.Fatalf("expected expired %s to be removed, got %d",table,count)}
 }
}
