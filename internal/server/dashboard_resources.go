package server

import (
 "context"
 "encoding/json"
 "fmt"
 "net/http"
 "os/exec"
 "strconv"
 "strings"
 "time"

 panelapp "github.com/bau59/open-go-panel/internal/app"
)

type dashboardUnitResource struct {
 Name string `json:"name"`
 Active bool `json:"active"`
 Memory string `json:"memory"`
 CPUTime string `json:"cpu_time"`
}

type dashboardContainerResource struct {
 Name string `json:"name"`
 Running bool `json:"running"`
 CPU string `json:"cpu"`
 Memory string `json:"memory"`
}

type dashboardResources struct {
 AppsTotal int `json:"apps_total"`
 AppsRunning int `json:"apps_running"`
 ContainersTotal int `json:"containers_total"`
 ContainersRunning int `json:"containers_running"`
 Units []dashboardUnitResource `json:"units"`
 Containers []dashboardContainerResource `json:"containers"`
 DockerError string `json:"docker_error,omitempty"`
 AppsError string `json:"apps_error,omitempty"`
}

// Uses one systemctl query for all managed units, rather than spawning a
// subprocess for every app on each dashboard page view.
func dashboardUnitStats(ctx context.Context, apps []panelapp.App) map[int64]dashboardUnitResource {
 result:=map[int64]dashboardUnitResource{}
 var ids []int64
 args:=[]string{"show","--property=Id,ActiveState,MemoryCurrent,CPUUsageNSec"}
 for _,a:=range apps {
  if a.Type=="static" {continue}
  if len(ids)>=60 {break}
  ids=append(ids,a.ID)
  args=append(args,fmt.Sprintf("open-go-panel-app-%d.service",a.ID))
 }
 if len(ids)==0{return result}
 stdout,err:=exec.CommandContext(ctx,"systemctl",args...).Output()
 if err!=nil{return result}
 for _,block:=range strings.Split(string(stdout),"\n\n"){
  vals:=map[string]string{}
  for _,line:=range strings.Split(block,"\n"){
   key,value,ok:=strings.Cut(strings.TrimSpace(line),"=")
   if ok {vals[key]=value}
  }
  var id int64
  if _,err:=fmt.Sscanf(vals["Id"],"open-go-panel-app-%d.service",&id);err!=nil{continue}
  item:=dashboardUnitResource{Active:vals["ActiveState"]=="active",Memory:"No data",CPUTime:"No data"}
  if value,err:=strconv.ParseUint(vals["MemoryCurrent"],10,64);err==nil&&value!=^uint64(0){
   item.Memory=formatBytes(value)
  }
  if value,err:=strconv.ParseUint(vals["CPUUsageNSec"],10,64);err==nil&&value!=^uint64(0){
   item.CPUTime=fmt.Sprintf("%.1f s total",float64(value)/1e9)
  }
  result[id]=item
 }
 return result
}

func (cfg Config) readDashboardResources(ctx context.Context) dashboardResources {
 result:=dashboardResources{}
 if cfg.Apps!=nil {
  apps,err:=cfg.Apps.List()
  if err!=nil {result.AppsError="Applications unavailable"} else {
   result.AppsTotal=len(apps)
   units:=dashboardUnitStats(ctx,apps)
   for _,a:=range apps{
    unit,ok:=units[a.ID]
    if !ok{unit=dashboardUnitResource{Memory:"No data",CPUTime:"No data"}}
    unit.Name=a.Name
    if unit.Active{result.AppsRunning++}
    if len(result.Units)<25 {result.Units=append(result.Units,unit)}
   }
  }
 }
 if cfg.Docker!=nil{
  containers,err:=cfg.Docker.Containers(ctx)
  if err!=nil {result.DockerError="Docker not available"} else{
   result.ContainersTotal=len(containers)
   for _,c:=range containers{
    if c.Running{result.ContainersRunning++}
   }
   stats,err:=cfg.Docker.Stats(ctx)
   if err!=nil{result.DockerError="Container resource statistics unavailable"}
   for _,c:=range containers{
    if len(result.Containers)>=25{break}
    item:=dashboardContainerResource{Name:c.Name,Running:c.Running,CPU:"No data",Memory:"No data"}
    if stat,ok:=stats[c.ID];ok {
     item.CPU=stat.CPU
     item.Memory=stat.Memory
    }
    result.Containers=append(result.Containers,item)
   }
  }
 }
 return result
}

func registerDashboardResources(mux *http.ServeMux,store *sessionStore,cfg Config) {
 mux.Handle("GET /dashboard/resources",requireAuth(store,http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  ctx,cancel:=context.WithTimeout(r.Context(),9*time.Second)
  defer cancel()
  resources:=cfg.readDashboardResources(ctx)
  w.Header().Set("Cache-Control","no-store")
  w.Header().Set("Content-Type","application/json; charset=utf-8")
  _=json.NewEncoder(w).Encode(resources)
 })))
}
