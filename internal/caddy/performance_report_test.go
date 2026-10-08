package caddy

import (
 "testing"
 "time"
)

func TestOnDemandReportExactMeasurementsAndFilters(t *testing.T){
 now:=time.Now().UTC().Truncate(time.Second)
 points:=[]PerformancePoint{
  {Time:now,Domain:"a.example.com",Method:"GET",Route:"/users/{id}",Status:200,DurationMS:35.35112,Size:9500},
  {Time:now.Add(time.Second),Domain:"a.example.com",Method:"GET",Route:"/users/{id}",Status:500,DurationMS:1050,Size:0},
 }
 result:=buildPerformanceResult(points,PerformanceFilter{
  Since:now.Add(-time.Minute),Until:now.Add(time.Minute),ThresholdMS:500,Page:1,PerPage:50,
 },false)
 if result.Total!=2||result.Slow!=1||result.Errors!=1{t.Fatalf("incorrect report totals: %+v",result)}
 if result.P50<542.67||result.P50>542.68{t.Fatalf("median incorrect: %f",result.P50)}
 if len(result.Routes)!=1||result.Routes[0].Count!=2{t.Fatalf("route grouping lost: %+v",result.Routes)}
 if len(result.Rows)!=2{t.Fatalf("request rows missing: %+v",result.Rows)}
}

func TestReportTruncationIsExplicit(t *testing.T){
 result:=buildPerformanceResult(nil,PerformanceFilter{Page:1,PerPage:50},true)
 if !result.Truncated{t.Fatal("capped report must expose incomplete sample")}
}

func TestPerformanceSourceIsConstrained(t *testing.T){
 for _,path:=range []string{"/tmp/log.json","relative.log","/var/log/caddy/../passwd"}{
  if ValidCaddyLogPath(path){t.Errorf("accepted unsafe log path %q",path)}
 }
}
