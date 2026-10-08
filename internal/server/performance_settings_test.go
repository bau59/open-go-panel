package server

import (
 "strings"
 "testing"
)

func TestPerformanceThresholdBoundaries(t *testing.T) {
 limits:=defaultPerformanceThresholds()
 for _,tc:=range []struct{duration float64; want string}{
  {0,"Fast"}, {99.99,"Fast"}, {100,"Normal"},
  {499.99,"Normal"}, {500,"Slow"}, {1000,"Slow"}, {1000.01,"Very slow"},
 } {
  name,_:=limits.classify(tc.duration)
  if name!=tc.want {t.Errorf("%.2fms => %s; want %s",tc.duration,name,tc.want)}
 }
}

func TestPerformanceThresholdValidation(t *testing.T) {
 for _,v:=range []string{"0,500,1000","100,100,1000","500,100,1000","100,500,999999","oops","100,500"} {
  if _,valid:=parsePerformanceThresholds(v);valid{t.Errorf("accepted invalid thresholds %q",v)}
 }
 got,valid:=parsePerformanceThresholds("100,500,1000")
 if !valid||got.serialize()!="100,500,1000"{t.Fatalf("unexpected thresholds: %+v (%v)",got,valid)}
}

func TestPerformanceThresholdFormHasApplicationScope(t *testing.T) {
 content:=performanceThresholdForm(defaultPerformanceThresholds(),7,[]performanceAppOption{{ID:7,Name:"web"}})
 for _,part:=range []string{`name="app_id"`,`value="7" selected`,`name="fast_ms"`,`name="ordinary_ms"`,`name="slow_ms"`}{
  if !strings.Contains(content,part){t.Errorf("missing %s",part)}
 }
}
