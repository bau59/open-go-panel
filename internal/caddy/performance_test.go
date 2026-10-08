package caddy

import (
 "math"
 "testing"
)

func TestCleanPerformanceRouteDropsQueryAndNormalizesIDs(t *testing.T){
 cases:=map[string]string{
 "/users/123?token=secret":"/users/{id}",
 "/users/456":"/users/{id}",
 "/orders/550e8400-e29b-41d4-a716-446655440000?password=x":"/orders/{id}",
 "/api/health?sessionid=abc":"/api/health",
 }
 for input,want:=range cases{if got:=cleanPerformanceRoute(input);got!=want{t.Errorf("%q => %q, want %q",input,got,want)}}
}
func TestPerformancePercentilesMeasured(t *testing.T){
 values:=[]float64{1,2,3,4,5}
 if percentile(values,.5)!=3{t.Fatal("median incorrect")}
 if math.Abs(percentile(values,.95)-4.8)>1e-9{t.Fatalf("p95=%g",percentile(values,.95))}
 if percentile(nil,.99)!=0{t.Fatal("empty percentile incorrect")}
}

func TestParseServerTimingMetrics(t *testing.T){
 results:=parseServerTiming([]string{"db;dur=8.2, redis;dur=1.5, jet;dur=4.1"})
 if len(results)!=3 || results[0].Name!="db" || results[0].DurationMS!=8.2 {
  t.Fatalf("unexpected Server-Timing: %#v",results)
 }
}
func TestRejectUnmeasuredServerTiming(t *testing.T){
 results:=parseServerTiming([]string{"db;desc=unknown, bad;dur=-1, inf;dur=NaN"})
 if len(results)!=0{t.Fatalf("unmeasured timings accepted: %#v",results)}
}
