package server

import (
    "context"
    "fmt"
    "html"
    "net/http"
    "strconv"
    "strings"
    "time"
)

func registerDatabaseCloudBackupRoutes(mux *http.ServeMux,store *sessionStore,cfg Config){
    mux.Handle("GET /backup/databases/{id}/cloud",requireAuth(store,http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
        id,err:=strconv.ParseInt(r.PathValue("id"),10,64)
        if err!=nil||id<=0{http.NotFound(w,r);return}
        db,err:=cfg.Databases.Get(id);if err!=nil{http.NotFound(w,r);return}
        ctx,cancel:=context.WithTimeout(r.Context(),25*time.Second);defer cancel()
        list,err:=cfg.Databases.CloudBackups(ctx,id)
        var out strings.Builder
        fmt.Fprintf(&out,`<main class="shell"><div class="backup-page"><section class="page-head"><div><h1>Cloud SQL copies</h1><p class="sub">%s · %s</p></div><a class="secondary" href="/backup?section=databases&amp;database=%d">Local copies</a></section>%s<section class="panel panel-pad"><p class="note">Retrieve a copy to local backup history before restoring it. Existing files and database contents are never overwritten by retrieval.</p>`,html.EscapeString(db.Name),html.EscapeString(db.Engine),id,backupNavigation("databases"))
        if err!=nil{out.WriteString(`<p class="alert">`+html.EscapeString(err.Error())+`</p><a class="secondary" href="/backup?section=storage">Storage settings</a>`)}else if len(list)==0{out.WriteString(`<p class="note">No completed cloud copies.</p>`)}
        for _,item:=range list{
            date:=item.Name
            if !item.CreatedAt.IsZero(){date=item.CreatedAt.UTC().Format("2006-01-02 15:04:05 UTC")}
            fmt.Fprintf(&out,`<div class="backup-row"><div class="db-backup-file"><strong>%s</strong><div class="note">%s · %s</div></div><form method="post" action="/backup/databases/%d/cloud/retrieve"><input type="hidden" name="name" value="%s"><button class="secondary">Download to server</button></form></div>`,html.EscapeString(date),formatBytes(uint64(item.Size)),html.EscapeString(item.Name),id,html.EscapeString(item.Name))
        }
        out.WriteString("</section></div></main>")
        writeHTML(w,cfg.Logger,200,pageHead("Cloud SQL copies")+"<body>"+appHeader("backup")+out.String()+"</body></html>")
    })))
    mux.Handle("POST /backup/databases/{id}/cloud/retrieve",requireAuth(store,http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
        id,err:=strconv.ParseInt(r.PathValue("id"),10,64)
        if err!=nil||id<=0{http.NotFound(w,r);return}
        if err:=r.ParseForm();err!=nil{http.Error(w,"invalid form",400);return}
        message:=""
        if err:=cfg.Databases.StartCloudBackupDownload(id,r.FormValue("name"));err!=nil{message=err.Error()}
        backupRedirect(w,r,"databases",message)
    })))
}
