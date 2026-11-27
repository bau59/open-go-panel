package app

import (
    "context"
    "errors"
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    "regexp"
    "sort"
    "strings"
    "time"
)

type ServiceDependency struct {
    Unit string `json:"unit"`
    Required bool `json:"required"`
}
type SystemService struct { Name, State string }
const dependencyFilename = "50-ogp-dependencies.conf"
var serviceUnitName = regexp.MustCompile(`^[A-Za-z0-9_:][A-Za-z0-9_.:@-]{0,246}\.service$`)

func validServiceUnit(name string)bool {
    return serviceUnitName.MatchString(name)&&!strings.HasSuffix(name,"@.service")
}

func SystemServices(ctx context.Context)([]SystemService,error){
    ctx,cancel:=context.WithTimeout(ctx,5*time.Second);defer cancel()
    units:=map[string]string{}
    files,err:=exec.CommandContext(ctx,"systemctl","list-unit-files","--type=service","--no-legend","--no-pager","--plain").Output()
    if err!=nil{return nil,fmt.Errorf("list installed services: %w",err)}
    for _,line:=range strings.Split(string(files),"\n"){
        f:=strings.Fields(line);if len(f)>=2&&validServiceUnit(f[0]){units[f[0]]="not loaded / "+f[1]}
    }
    active,err:=exec.CommandContext(ctx,"systemctl","list-units","--all","--type=service","--no-legend","--no-pager","--plain").Output()
    if err!=nil{return nil,fmt.Errorf("list loaded services: %w",err)}
    for _,line:=range strings.Split(string(active),"\n"){
        f:=strings.Fields(line);if len(f)>=4&&validServiceUnit(f[0]){units[f[0]]=f[2]+" / "+f[3]}
    }
    out:=make([]SystemService,0,len(units))
    for name,status:=range units{out=append(out,SystemService{name,status})}
    sort.Slice(out,func(i,j int)bool{return out[i].Name<out[j].Name})
    return out,nil
}

type unitEdges struct { ID,LoadState string; After,Requires,Wants []string }
func readUnitEdges(ctx context.Context,name string)(unitEdges,error){
    var u unitEdges
    out,err:=exec.CommandContext(ctx,"systemctl","show","--property=Id,LoadState,After,Requires,Wants,Requisite,BindsTo","--",name).Output()
    props:=map[string]string{}
    for _,line:=range strings.Split(string(out),"\n"){k,v,ok:=strings.Cut(line,"=");if ok{props[k]=v}}
    if props["Id"]==""||props["LoadState"]==""{return u,fmt.Errorf("inspect %s: %v",name,err)}
    u.ID=props["Id"];u.LoadState=props["LoadState"];u.After=strings.Fields(props["After"]);u.Requires=append(strings.Fields(props["Requires"]),strings.Fields(props["Requisite"])...);u.Requires=append(u.Requires,strings.Fields(props["BindsTo"])...);u.Wants=strings.Fields(props["Wants"])
    return u,nil
}
type edgeReader func(context.Context,string)(unitEdges,error)
func normalizeDependencies(ctx context.Context,self string,deps []ServiceDependency,read edgeReader)([]ServiceDependency,error){
    if len(deps)>128{return nil,errors.New("at most 128 service dependencies")}
    cache:=map[string]unitEdges{}
    load:=func(name string)(unitEdges,error){
        if v,ok:=cache[name];ok{return v,nil}
        if len(cache)>4096{return unitEdges{},errors.New("dependency graph too large to verify")}
        v,err:=read(ctx,name);if err==nil{cache[name]=v;cache[v.ID]=v};return v,err
    }
    normalized:=map[string]bool{}
    for _,d:=range deps{
        if !validServiceUnit(d.Unit){return nil,fmt.Errorf("invalid service name: %q",d.Unit)}
        u,err:=load(d.Unit);if err!=nil{return nil,err}
        if u.LoadState!="loaded"{return nil,fmt.Errorf("service %s is not available (%s)",d.Unit,u.LoadState)}
        if !validServiceUnit(u.ID){return nil,fmt.Errorf("invalid canonical service: %q",u.ID)}
        if u.ID==self{return nil,errors.New("application cannot depend on itself")}
        normalized[u.ID]=normalized[u.ID]||d.Required
    }
    // A new ordering edge must not reach self through existing After edges.
    // Also reject dependency cycles through Requires/Wants, including aliases.
    for name:=range normalized {
        for _,ordering:=range []bool{true,false}{
            visited:=map[string]bool{}
            pending:=[]string{name}
            for len(pending)>0{
                v:=pending[len(pending)-1];pending=pending[:len(pending)-1]
                if v==self{return nil,fmt.Errorf("dependency cycle involving %s",name)}
                if visited[v]{continue};visited[v]=true
                u,err:=load(v);if err!=nil{return nil,err}
                if u.ID==self{return nil,fmt.Errorf("dependency cycle involving %s",name)}
                if ordering{pending=append(pending,u.After...)}else{pending=append(pending,u.Requires...);pending=append(pending,u.Wants...)}
            }
        }
    }
    result:=make([]ServiceDependency,0,len(normalized))
    for name,required:=range normalized{result=append(result,ServiceDependency{name,required})}
    sort.Slice(result,func(i,j int)bool{return result[i].Unit<result[j].Unit})
    return result,nil
}
func dependencyContent(deps []ServiceDependency)[]byte{
    var after,requires,wants []string
    for _,d:=range deps{
        after=append(after,d.Unit)
        if d.Required{requires=append(requires,d.Unit)}else{wants=append(wants,d.Unit)}
    }
    if len(after)==0{return nil}
    var b strings.Builder
    b.WriteString("# Managed by Open Go Panel\n[Unit]\nAfter="+strings.Join(after," ")+"\n")
    if len(requires)>0{b.WriteString("Requires="+strings.Join(requires," ")+"\n")}
    if len(wants)>0{b.WriteString("Wants="+strings.Join(wants," ")+"\n")}
    return []byte(b.String())
}
func(m *Manager)dependencyPath(id int64)string{return filepath.Join(m.serviceDir,serviceName(id)+".d",dependencyFilename)}
func(m *Manager)removeDependencyFile(id int64)error{
    err:=os.Remove(m.dependencyPath(id));if errors.Is(err,os.ErrNotExist){return nil};return err
}
func writeDependencyFile(path string,data []byte)error{
    if len(data)==0{
        err:=os.Remove(path);if errors.Is(err,os.ErrNotExist){return nil};return err
    }
    dir:=filepath.Dir(path)
    if err:=os.MkdirAll(dir,0755);err!=nil{return err}
    f,err:=os.CreateTemp(dir,".ogp-dependencies-");if err!=nil{return err}
    defer os.Remove(f.Name());defer f.Close()
    if err=f.Chmod(0644);err!=nil{return err}
    if _,err=f.Write(data);err!=nil{return err}
    if err=f.Sync();err!=nil{return err}
    if err=f.Close();err!=nil{return err}
    return os.Rename(f.Name(),path)
}
func(m *Manager)SetDependencies(ctx context.Context,id int64,deps []ServiceDependency)error{
    m.mu.Lock();defer m.mu.Unlock()
    apps,err:=m.load();if err!=nil{return err}
    index:=-1
    for i:=range apps{if apps[i].ID==id{index=i;break}}
    if index<0{return fmt.Errorf("app %d not found",id)}
    if apps[index].Type=="php"||apps[index].Type=="static"{return errors.New("application has no dedicated systemd service")}
    unit:=serviceName(id)
    if _,err:=os.Stat(filepath.Join(m.serviceDir,unit));err!=nil{return errors.New("configure the application service first")}
    check,cancel:=context.WithTimeout(ctx,25*time.Second);defer cancel()
    normalized,err:=normalizeDependencies(check,unit,deps,readUnitEdges);if err!=nil{return err}
    path:=m.dependencyPath(id)
    previous,err:=os.ReadFile(path);if err!=nil&&!errors.Is(err,os.ErrNotExist){return err}
    if err=writeDependencyFile(path,dependencyContent(normalized));err!=nil{return err}
    rollback:=func(cause error)error{
        restoreCtx,cancel:=context.WithTimeout(context.Background(),15*time.Second);defer cancel()
        e:=writeDependencyFile(path,previous)
        if e==nil{e=systemctl(restoreCtx,"daemon-reload")}
        return errors.Join(cause,e)
    }
    out,err:=exec.CommandContext(check,"systemd-analyze","verify",filepath.Join(m.serviceDir,unit)).CombinedOutput()
    if err!=nil{return rollback(fmt.Errorf("invalid systemd configuration: %w: %s",err,strings.TrimSpace(string(out))))}
    if err=systemctl(check,"daemon-reload");err!=nil{return rollback(err)}
    apps[index].Service.Dependencies=normalized
    if err=m.save(apps);err!=nil{return rollback(err)}
    return nil
}
