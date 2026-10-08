package dbmanager

import (
 "context"
 "fmt"
 "os/exec"
 "strings"
)

type OptionalLogState struct {
 MySQL string
 Postgres string
 Redis string
}

func (m *Manager) OptionalLoggingState(ctx context.Context) OptionalLogState{
 state:=OptionalLogState{MySQL:"Unavailable",Postgres:"Unavailable",Redis:"Unavailable"}
 if out,err:=exec.CommandContext(ctx,"mysql","--batch","--skip-column-names","--protocol=socket","-uroot",
  "-e","SELECT @@GLOBAL.slow_query_log;").Output();err==nil{
  if strings.TrimSpace(string(out))=="1"{state.MySQL="Enabled"}else{state.MySQL="Disabled"}
 }
 if out,err:=exec.CommandContext(ctx,"runuser","-u","postgres","--",
  "psql","-X","-At","-d","postgres","-c","SHOW log_min_duration_statement").Output();err==nil{
  if strings.TrimSpace(string(out))=="-1"{state.Postgres="Disabled (global)"}else{state.Postgres="Enabled (global)"}
 }
 if out,err:=exec.CommandContext(ctx,"redis-cli","--raw","CONFIG","GET","slowlog-log-slower-than").Output();err==nil{
  lines:=strings.Split(strings.TrimSpace(string(out)),"\n")
  if len(lines)>0&&strings.TrimSpace(lines[len(lines)-1])=="-1"{state.Redis="Disabled"}else{state.Redis="Enabled"}
 }
 return state
}

// Change only optional slow-query instrumentation; essential engine error
// logging remains available to operators.
func (m *Manager) SetOptionalLogging(ctx context.Context,engine string,enabled bool)error{
 switch engine{
 case "mysql":
  mode:="OFF"
  if enabled{mode="ON"}
  out,err:=exec.CommandContext(ctx,"mysql","--protocol=socket","-uroot",
   "-e","SET PERSIST slow_query_log = "+mode+";").CombinedOutput()
  if err!=nil{return fmt.Errorf("MySQL slow log: %w: %s",err,strings.TrimSpace(string(out)))}
  return nil
 case "postgres":
  duration:="-1"
  if enabled{duration="1000ms"}
  statement:="ALTER SYSTEM SET log_min_duration_statement = '"+duration+"'"
  out,err:=exec.CommandContext(ctx,"runuser","-u","postgres","--",
   "psql","-X","-v","ON_ERROR_STOP=1","-d","postgres","-c",statement).CombinedOutput()
  if err!=nil{return fmt.Errorf("PostgreSQL duration log: %w: %s",err,strings.TrimSpace(string(out)))}
  out,err=exec.CommandContext(ctx,"runuser","-u","postgres","--",
   "psql","-X","-v","ON_ERROR_STOP=1","-d","postgres","-c","SELECT pg_reload_conf()").CombinedOutput()
  if err!=nil{return fmt.Errorf("PostgreSQL setting saved but reload failed: %w: %s",err,strings.TrimSpace(string(out)))}
  return nil
 case "redis":
  threshold:="-1"
  if enabled{threshold="10000"}
  out,err:=exec.CommandContext(ctx,"redis-cli","CONFIG","SET",
   "slowlog-log-slower-than",threshold).CombinedOutput()
  if err!=nil{return fmt.Errorf("Redis SLOWLOG: %w: %s",err,strings.TrimSpace(string(out)))}
  if out,err=exec.CommandContext(ctx,"redis-cli","CONFIG","REWRITE").CombinedOutput();err!=nil {
   return fmt.Errorf("Redis setting applied in memory but not persisted across restart: %w: %s",err,strings.TrimSpace(string(out)))
  }
  return nil
 default:
  return fmt.Errorf("unsupported optional log type")
 }
}
