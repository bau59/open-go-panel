package caddy

import (
 "context"
 "time"
)

type PerformanceRollup struct {
 Hour time.Time
 Requests int64
 Errors5xx int64
 Slow500 int64
 AverageMS float64
}

// Stored hourly rollups preserve exact request counts, errors and total time
// after detail retention expires. Exact P50/P95/P99 cannot be recovered from
// the rollups and must never be invented.
func (m *Manager) PerformanceRollups(ctx context.Context,f PerformanceFilter)([]PerformanceRollup,error){
 query:=`SELECT hour_ns,SUM(requests),SUM(total_ms),SUM(errors_5xx),SUM(slow_500)
 FROM http_perf_rollup WHERE hour_ns>=? AND hour_ns<=?`
 args:=[]any{f.Since.UnixNano(),f.Until.UnixNano()}
 if f.Domain!=""{query+=" AND domain=?";args=append(args,f.Domain)}
 if f.AppID>0{query+=" AND app_id=?";args=append(args,f.AppID)}
 if f.Method!=""{query+=" AND method=?";args=append(args,f.Method)}
 if f.Route!=""{query+=" AND instr(lower(route),lower(?))>0";args=append(args,f.Route)}
 query+=" GROUP BY hour_ns ORDER BY hour_ns LIMIT 1000"
 rows,err:=m.store.DB().QueryContext(ctx,query,args...)
 if err!=nil{return nil,err}
 defer rows.Close()
 var result []PerformanceRollup
 for rows.Next(){
  var r PerformanceRollup
  var hour int64
  var totalMS float64
  if err:=rows.Scan(&hour,&r.Requests,&totalMS,&r.Errors5xx,&r.Slow500);err!=nil{return nil,err}
  r.Hour=time.Unix(0,hour)
  if r.Requests>0{r.AverageMS=totalMS/float64(r.Requests)}
  result=append(result,r)
 }
 return result,rows.Err()
}
