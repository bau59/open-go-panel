package server

import (
 "strings"
 "testing"
 "net/url"

 "github.com/bau59/open-go-panel/internal/caddy"
)

func TestPerformancePageDoesNotInventBackendMetrics(t *testing.T) {
 f:=caddy.PerformanceFilter{ThresholdMS:500,Page:1}
 page:=performancePage("1h",url.Values{"period":{"1h"}},f,caddy.PerformanceResult{},"")
 for _,value:=range []string{"Performance","P50","P95","P99","No data","Query strings and client IP"}{
  if !strings.Contains(page,value){t.Errorf("missing %s",value)}
 }
}

func TestLogRetentionPageExplainsSharedJournalLimit(t *testing.T){
 page:=logRetentionPage(256,7,30,128,"")
 for _,name:=range []string{
  "System journal (shared limit)","Panel audit trail","Redis SLOWLOG memory buffer",
  "PostgreSQL slow queries","MySQL slow queries","name=\"max_mb\"",
  "name=\"max_days\"","name=\"days\"","name=\"length\"",
 } {if !strings.Contains(page,name){t.Errorf("missing %s",name)}}
}
