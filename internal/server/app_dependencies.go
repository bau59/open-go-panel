package server

import (
    "context"
    "fmt"
    "html"
    "net/http"
    "strconv"
    "strings"

    panelapp "github.com/bau59/open-go-panel/internal/app"
)

func registerAppDependencyRoutes(mux *http.ServeMux,store *sessionStore,cfg Config){
    mux.Handle("POST /apps/{id}/dependencies",requireAuth(store,http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
        id,err:=strconv.ParseInt(r.PathValue("id"),10,64)
        if err!=nil||id<=0{http.Error(w,"invalid application",400);return}
        if err=r.ParseForm();err!=nil{http.Error(w,"invalid dependency form",400);return}
        units,modes:=r.PostForm["dependency_unit"],r.PostForm["dependency_mode"]
        if len(units)!=len(modes)||len(units)>129{http.Error(w,"invalid dependency rows",400);return}
        var deps []panelapp.ServiceDependency
        for i,unit:=range units{
            unit=strings.TrimSpace(unit)
            if unit==""{continue}
            if modes[i]!="required"&&modes[i]!="optional"{http.Error(w,"invalid dependency mode",400);return}
            deps=append(deps,panelapp.ServiceDependency{Unit:unit,Required:modes[i]=="required"})
        }
        if err=cfg.Apps.SetDependencies(r.Context(),id,deps);err!=nil{http.Error(w,err.Error(),400);return}
        cfg.State.Audit(r.Context(),"app.dependencies",fmt.Sprintf("app:%d",id),fmt.Sprintf("saved %d dependency rows; no application restart",len(deps)))
        http.Redirect(w,r,fmt.Sprintf("/apps/%d#service-dependencies",id),303)
    })))
}
func dependencyBlock(ctx context.Context,app panelapp.App,configured bool)string{
    if app.Type=="php"||app.Type=="static"{return ""}
    var out strings.Builder
    out.WriteString(`<section class="panel panel-pad app-card-wide" id="service-dependencies" data-panel-tab="settings"><div class="section-title"><div><h2>System service dependencies</h2><p class="note">Start after selected systemd services. Saving reloads systemd configuration without restarting the application.</p></div></div>`)
    if !configured{return out.String()+`<p class="note">Configure the application service first.</p></section>`}
    services,err:=panelapp.SystemServices(ctx)
    if err!=nil{return out.String()+`<p class="alert">`+html.EscapeString(err.Error())+`</p></section>`}
    states:=map[string]string{}
    fmt.Fprintf(&out,`<form method="post" action="/apps/%d/dependencies" data-dependency-form><datalist id="dependency-units">`,app.ID)
    for _,s:=range services{
        if s.Name==fmt.Sprintf("open-go-panel-app-%d.service",app.ID){continue}
        states[s.Name]=s.State
        fmt.Fprintf(&out,`<option value="%s" label="%s"></option>`,html.EscapeString(s.Name),html.EscapeString(s.State))
    }
    out.WriteString(`</datalist><div class="dependency-rows" data-dependency-rows>`)
    row:=func(d panelapp.ServiceDependency)string{
        state:=states[d.Unit]
        if state==""&&d.Unit!=""{state="not found"}
        mode:="required";if !d.Required{mode="optional"}
        return fmt.Sprintf(`<div class="dependency-row"><label>Service<input name="dependency_unit" list="dependency-units" placeholder="Search systemd service…" value="%s" autocomplete="off"></label><label>Dependency<select name="dependency_mode"><option value="required"%s>Required</option><option value="optional"%s>Optional</option></select></label><span class="note dependency-state" data-dependency-state>%s</span><button type="button" class="secondary compact-action" data-dependency-remove>Remove</button></div>`,html.EscapeString(d.Unit),selected(mode,"required"),selected(mode,"optional"),html.EscapeString(state))
    }
    for _,d:=range app.Service.Dependencies{out.WriteString(row(d))}
    blank:=row(panelapp.ServiceDependency{Required:true})
    if len(app.Service.Dependencies)==0{out.WriteString(blank)}
    out.WriteString(`</div><template data-dependency-template>`+blank+`</template><div class="actions" style="justify-content:flex-start;margin-top:12px"><button type="button" class="secondary" data-dependency-add>Add service</button><button class="button" type="submit">Save dependencies</button></div><p class="note">Required: Requires + After. Optional: Wants + After. A dependency must be a real service; templates require an instance name. Ordering uses systemd readiness, not arbitrary external health checks.</p></form></section>`)
    return out.String()
}
