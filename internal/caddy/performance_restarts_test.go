package caddy

import (
 "testing"
 "time"
)

func TestSystemdPropertiesAndEventTime(t *testing.T){
 data:=[]byte("ExecMainStartTimestamp=Thu 2026-10-08 17:25:00 UTC\nActiveExitTimestamp=Thu 2026-10-08 17:24:59 UTC\nResult=exit-code\n")
 props:=parseSystemdProperties(data)
 if props["Result"]!="exit-code"{t.Errorf("unexpected result: %+v",props)}
 event:=parseSystemdEventTime(props["ExecMainStartTimestamp"])
 if event.IsZero()||event.UTC().Format("2006-01-02 15:04:05")!="2026-10-08 17:25:00"{
  t.Errorf("incorrect recorded timestamp: %s",event)
 }
 if !parseSystemdEventTime("n/a").IsZero(){t.Fatal("nonexistent timestamp should not be recorded")}
}

func TestOnlyRealStartupEventsCount(t *testing.T){
 if validSystemdEventTime(time.Time{}){t.Fatal("zero start is not real")}
 if validSystemdEventTime(time.Now().Add(5*time.Minute)){t.Fatal("future event should not count")}
}
