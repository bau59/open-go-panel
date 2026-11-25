package server

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func registerSnapshotRoutes(mux *http.ServeMux,store *sessionStore,cfg Config)*snapshotManager{
	if cfg.State==nil{return nil}
	ctx:=cfg.BackupContext
	if ctx==nil{ctx=context.Background()}
	m:=&snapshotManager{cfg:cfg,ctx:ctx}
	if cfg.BackupContext!=nil{go m.runSchedule()}
	redirect:=func(w http.ResponseWriter,r *http.Request,err error){
		if err!=nil{http.Redirect(w,r,"/backup?error="+url.QueryEscape(err.Error()),http.StatusSeeOther);return}
		http.Redirect(w,r,"/backup",http.StatusSeeOther)
	}
	post:=func(path string,fn func(*http.Request)error){
		mux.Handle("POST "+path,requireAuth(store,http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
			if err:=r.ParseForm();err!=nil{http.Error(w,"invalid form",http.StatusBadRequest);return}
			redirect(w,r,fn(r))
		})))
	}
	post("/backup/snapshots/policy",func(r *http.Request)error{
		if m.status().State=="Running"{return fmt.Errorf("wait for the active snapshot operation")}
		values:=make([]int,5)
		for i,key:=range []string{"local_days","local_hour","local_keep","cloud_days","cloud_hour"}{
			n,err:=strconv.Atoi(r.FormValue(key));if err!=nil{return fmt.Errorf("invalid %s",key)}
			values[i]=n
		}
		return m.savePolicy(snapshotPolicy{LocalDays:values[0],LocalHour:values[1],LocalKeep:values[2],CloudDays:values[3],CloudHour:values[4]})
	})
	post("/backup/snapshots/create",func(r *http.Request)error{
		return m.start("Create local snapshot",func(ctx context.Context)error{return m.local(ctx,false)})
	})
	post("/backup/snapshots/cloud",func(r *http.Request)error{
		return m.start("Create cloud snapshot",func(ctx context.Context)error{return m.cloud(ctx,false)})
	})
	post("/backup/snapshots/upload",func(r *http.Request)error{
		id:=r.FormValue("id")
		if !validSnapshotID(id){return fmt.Errorf("invalid snapshot ID")}
		return m.start("Upload "+id,func(ctx context.Context)error{
			idx,err:=readSnapshotIndex(filepath.Join(snapshotRoot,id),id);if err!=nil{return err}
			return m.upload(ctx,idx)
		})
	})
	post("/backup/snapshots/retrieve",func(r *http.Request)error{
		id:=r.FormValue("id")
		if !validSnapshotID(id){return fmt.Errorf("invalid snapshot ID")}
		return m.start("Retrieve "+id,func(ctx context.Context)error{return m.download(ctx,id)})
	})
	mux.Handle("GET /backup/snapshots/cloud-list",requireAuth(store,http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		ctx,cancel:=context.WithTimeout(r.Context(),25*time.Second)
		defer cancel()
		list,err:=m.cloudList(ctx)
		if err!=nil{http.Error(w,err.Error(),http.StatusBadGateway);return}
		var out strings.Builder
		out.WriteString("<h3>Cloud restore points</h3>")
		if len(list)==0{out.WriteString("<p class=\"note\">No completed snapshots.</p>")}
		for _,idx:=range list{
			fmt.Fprintf(&out,`<form method="post" action="/backup/snapshots/retrieve" class="snapshot-row"><input type="hidden" name="id" value="%s"><span><strong>%s</strong><small>%d archives</small></span><button class="secondary">Download &amp; verify</button></form>`,html.EscapeString(idx.ID),html.EscapeString(idx.CreatedAt.Format("2006-01-02 15:04 UTC")),len(idx.Files))
		}
		writeHTML(w,cfg.Logger,200,pageHead("Cloud restore points")+"<body>"+appHeader("backup")+`<main class="shell"><section class="panel panel-pad"><div class="section-title"><h1>Cloud restore points</h1><a class="secondary" href="/backup">Back to backups</a></div>`+out.String()+"</section></main></body></html>")
	})))
	mux.Handle("GET /backup/snapshots/{id}/download/{file...}",requireAuth(store,http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		id,name:=r.PathValue("id"),r.PathValue("file")
		if !validSnapshotID(id)||!validSnapshotPath(name){http.NotFound(w,r);return}
		idx,err:=readSnapshotIndex(filepath.Join(snapshotRoot,id),id)
		if err!=nil{http.NotFound(w,r);return}
		found:=false
		for _,f:=range idx.Files{if f.Path==name{found=true;break}}
		if !found{http.NotFound(w,r);return}
		f,err:=os.Open(filepath.Join(snapshotRoot,id,filepath.FromSlash(name)))
		if err!=nil{http.NotFound(w,r);return}
		defer f.Close()
		info,err:=f.Stat();if err!=nil||!info.Mode().IsRegular(){http.NotFound(w,r);return}
		if err:=http.NewResponseController(w).SetWriteDeadline(time.Time{});err!=nil{http.Error(w,"download unavailable",http.StatusServiceUnavailable);return}
		w.Header().Set("Content-Disposition",fmt.Sprintf("attachment; filename=%q",filepath.Base(name)))
		w.Header().Set("Content-Type","application/gzip")
		w.Header().Set("Cache-Control","no-store")
		http.ServeContent(w,r,filepath.Base(name),info.ModTime(),f)
	})))
	return m
}

func (m *snapshotManager) render(r *http.Request)string{
	if m==nil{return ""}
	p:=m.policy()
	task:=m.status()
	list,err:=localSnapshots()
	var out strings.Builder
	if msg:=r.URL.Query().Get("error");msg!=""{fmt.Fprintf(&out,`<p class="snapshot-error">%s</p>`,html.EscapeString(msg))}
	if err!=nil{fmt.Fprintf(&out,`<p class="snapshot-error">%s</p>`,html.EscapeString(err.Error()))}
	cloudStatus:="Not configured"
	remote:="Configure on the Databases page"
	if m.cfg.Databases!=nil{
		if _,root,err:=m.cfg.Databases.YandexStorage();err==nil{cloudStatus="Configured";remote=root+"/server/"}
	}
	localLast:=m.setting("local_last");if localLast==""{localLast="Not run"}
	cloudLast:=m.setting("cloud_last");if cloudLast==""{cloudLast="Not run"}
	fmt.Fprintf(&out,`<div class="snapshot-metrics"><article class="metric"><span>Local restore points</span><strong>%d</strong></article><article class="metric"><span>Last scheduled local</span><strong>%s</strong></article><article class="metric"><span>Last scheduled cloud</span><strong>%s</strong></article><article class="metric"><span>Yandex Disk</span><strong>%s</strong></article></div>`,len(list),html.EscapeString(localLast),html.EscapeString(cloudLast),cloudStatus)
	if task.State!=""{
		fmt.Fprintf(&out,`<div class="snapshot-task"><strong>%s</strong><span>%s</span><span>%s</span><a class="secondary" href="/backup">Refresh status</a></div>`,html.EscapeString(task.State),html.EscapeString(task.Kind),html.EscapeString(task.Message))
	}
	out.WriteString(`<div class="snapshot-grid"><section class="panel panel-pad"><h2>Backup policy</h2><p class="note">Independent local and cloud schedules. Set days to zero to disable. Times are UTC.</p><form method="post" action="/backup/snapshots/policy" class="snapshot-policy">`)
	fmt.Fprintf(&out,`<div><h3>Local snapshots</h3><label>Every N days<input type="number" min="0" max="365" name="local_days" value="%d" required></label><label>Hour (UTC)<input type="number" min="0" max="23" name="local_hour" value="%d" required></label><label>Keep versions<input type="number" min="1" max="100" name="local_keep" value="%d" required></label></div><div><h3>Cloud snapshots</h3><label>Every N days<input type="number" min="0" max="365" name="cloud_days" value="%d" required></label><label>Hour (UTC)<input type="number" min="0" max="23" name="cloud_hour" value="%d" required></label><p class="note">Cloud-only runs do not change local retention.</p></div><button class="button">Save schedules</button></form></section>`,p.LocalDays,p.LocalHour,p.LocalKeep,p.CloudDays,p.CloudHour)
	fmt.Fprintf(&out,`<section class="panel panel-pad"><h2>Yandex Disk storage</h2><p class="note">OAuth connection, 900 MiB parts, SHA-256 checksums.</p><div class="snapshot-fact"><span>Remote directory</span><code>%s</code></div><div class="snapshot-actions"><a class="secondary" href="/databases#db-offsite-backups">Connection settings</a><a class="secondary" href="/backup/snapshots/cloud-list">Find cloud restore points</a></div><form method="post" action="/backup/snapshots/cloud"><button class="button">Create cloud snapshot</button></form><p class="note">Archives contain credentials. Restrict access to the selected Disk folder. Cloud retention is managed on the Disk.</p></section></div>`,html.EscapeString(remote))
	out.WriteString(`<section class="panel panel-pad"><div class="section-title"><div><h2>Server restore points</h2><p class="note">Panel SQLite state, managed user homes, credentials, app commands and schedules, Caddy and managed SQL dumps. Docker configuration only; volumes and unmanaged system data are not included.</p></div><form method="post" action="/backup/snapshots/create"><button class="button">Create local snapshot</button></form></div>`)
	if len(list)==0{out.WriteString(`<p class="note">No local snapshots yet.</p>`)}
	for _,idx:=range list{
		fmt.Fprintf(&out,`<details class="snapshot-entry"><summary><strong>%s</strong><span>%d archives</span></summary><div class="snapshot-entry-files">`,html.EscapeString(idx.CreatedAt.Format("2006-01-02 15:04 UTC")),len(idx.Files))
		for _,f:=range idx.Files{
			link:="/backup/snapshots/"+idx.ID+"/download/"+f.Path
			fmt.Fprintf(&out,`<div class="snapshot-row"><code>%s</code><span>%.1f MiB</span><a class="secondary" href="%s">Download</a></div>`,html.EscapeString(f.Path),float64(f.Size)/(1024*1024),html.EscapeString(link))
		}
		fmt.Fprintf(&out,`<form method="post" action="/backup/snapshots/upload"><input type="hidden" name="id" value="%s"><button class="button">Upload this snapshot</button></form></div></details>`,html.EscapeString(idx.ID))
	}
	out.WriteString(`<p class="note">Cloud recovery verifies and stages the archives locally. Applying a server snapshot requires an offline recovery procedure; it does not overwrite the live server.</p></section>`)
	return out.String()
}
