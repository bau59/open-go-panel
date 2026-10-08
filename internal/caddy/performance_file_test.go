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
 return fmt.Sprintf(`{"level":"info","logger":"http.log.access","ts":%d.1,"request":{"host":"example.com","method":"GET","uri":%q,"proto":"HTTP/2.0"},"status":200,"duration":0.03535112,"size":9728,"resp_headers":{"Server-Timing":["db;dur=8.2"]}}\n`,ts,uri)
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
