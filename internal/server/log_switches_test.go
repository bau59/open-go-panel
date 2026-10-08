package server

import (
 "context"
 "path/filepath"
 "strings"
 "testing"

 panelcaddy "github.com/bau59/open-go-panel/internal/caddy"
 "github.com/bau59/open-go-panel/internal/state"
)

func TestLogSwitchesPanelHasRealSourceControls(t *testing.T){
 store,err:=state.Open(filepath.Join(t.TempDir(),"panel.db"))
 if err!=nil{t.Fatal(err)}
 defer store.Close()
 cfg:=Config{State:store,Caddy:panelcaddy.New(store,filepath.Join(t.TempDir(),"caddy.json"))}
 page:=logSwitchesPanel(context.Background(),cfg)
 for _,action:=range []string{
  "/log-retention/caddy/toggle",
  "/log-retention/audit/toggle",
  "/log-retention/database/mysql/toggle",
  "/log-retention/database/postgres/toggle",
  "/log-retention/database/redis/toggle",
  "/log-retention/optional/disable",
  "/log-retention/performance/clear",
 }{
  if !strings.Contains(page,action){t.Errorf("missing action %s",action)}
 }
 if !currentAuditLogging(cfg){t.Fatal("audit must be enabled by default")}
 if err:=store.SetSetting("logs.audit_enabled","0");err!=nil{t.Fatal(err)}
 if currentAuditLogging(cfg){t.Fatal("disabled audit is ignored")}
}
