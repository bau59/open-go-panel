package server

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (m *snapshotManager) appFromRequest(r *http.Request)(int64,error){
	id,err:=strconv.ParseInt(r.PathValue("app"),10,64)
	if err!=nil||id<=0{return 0,errors.New("invalid application")}
	if _,err:=m.cfg.Apps.Get(id);err!=nil{return 0,err}
	return id,nil
}

func (m *snapshotManager) appRemote(id int64)(string,error){
	if m.cfg.Databases==nil{return "",errors.New("database manager unavailable")}
	_,root,err:=m.cfg.Databases.YandexStorage();if err!=nil{return "",err}
	a,err:=m.cfg.Apps.Get(id);if err!=nil{return "",err}
	if !backupUsername.MatchString(a.User)||!snapshotSegment.MatchString(a.Name){return "",errors.New("invalid project path")}
	return root+"/projects/"+a.User+"/"+a.Name,nil
}

func (m *snapshotManager) registerAppSnapshotRoutes(mux *http.ServeMux,store *sessionStore,post func(string,func(*http.Request)error)){
	post("/backup/apps/{app}/policy",func(r *http.Request)error{
		id,err:=m.appFromRequest(r);if err!=nil{return err}
		if m.status().State=="Running"{return errors.New("a backup operation is running")}
		p,err:=parseSnapshotPolicy(r);if err!=nil{return err}
		return m.savePolicyFor(m.appPolicyKey(id),p)
	})
	post("/backup/apps/{app}/create",func(r *http.Request)error{
		id,err:=m.appFromRequest(r);if err!=nil{return err}
		return m.start("Create application snapshot",func(ctx context.Context)error{return m.appLocal(ctx,id,false)})
	})
	post("/backup/apps/{app}/cloud",func(r *http.Request)error{
		id,err:=m.appFromRequest(r);if err!=nil{return err}
		return m.start("Create cloud application snapshot",func(ctx context.Context)error{return m.appCloud(ctx,id,false)})
	})
	post("/backup/apps/{app}/upload",func(r *http.Request)error{
		id,err:=m.appFromRequest(r);if err!=nil{return err}
		stamp:=r.FormValue("id");if !validSnapshotID(stamp){return errors.New("invalid snapshot ID")}
		return m.start("Upload application snapshot",func(ctx context.Context)error{
			root:=appSnapshotRoot(id)
			idx,err:=readSnapshotIndex(filepath.Join(root,stamp),stamp);if err!=nil{return err}
			if idx.AppID!=id{return errors.New("application snapshot mismatch")}
			remote,err:=m.appRemote(id);if err!=nil{return err}
			return m.uploadAt(ctx,idx,root,remote)
		})
	})
	post("/backup/apps/{app}/restore-files",func(r *http.Request)error{
		id,err:=m.appFromRequest(r);if err!=nil{return err}
		stamp:=r.FormValue("id")
		if !validSnapshotID(stamp)||r.FormValue("confirm")!="RESTORE"{return errors.New("type RESTORE to confirm file recovery")}
		return m.start("Restore application files",func(ctx context.Context)error{
			a,err:=m.cfg.Apps.Get(id);if err!=nil{return err}
			root:=filepath.Join(appSnapshotRoot(id),stamp)
			idx,err:=readSnapshotIndex(root,stamp);if err!=nil{return err}
			if idx.AppID!=id||idx.Owner!=a.User||idx.Application!=a.Name{return errors.New("application snapshot mismatch")}
			name:="files/"+a.User+".tar.gz"
			actual,err:=snapshotFileInfo(root,name);if err!=nil{return err}
			verified:=false
			for _,item:=range idx.Files{if item==actual{verified=true;break}}
			if !verified{return errors.New("application archive failed integrity check")}
			if _,err:=createAppSnapshot(ctx,m.cfg,id);err!=nil{return fmt.Errorf("cannot create safety snapshot: %w",err)}
			return m.cfg.Apps.RestoreSnapshotFiles(ctx,id,filepath.Join(root,name))
		})
	})
	post("/backup/apps/{app}/restore-database",func(r *http.Request)error{
		id,err:=m.appFromRequest(r);if err!=nil{return err}
		stamp,name:=r.FormValue("id"),r.FormValue("file")
		if !validSnapshotID(stamp)||!validSnapshotPath(name)||!strings.HasPrefix(name,"databases/")||r.FormValue("confirm")!="RESTORE"{return errors.New("select a database archive and type RESTORE")}
		return m.start("Restore application database",func(ctx context.Context)error{
			if m.cfg.Databases==nil{return errors.New("database manager unavailable")}
			a,err:=m.cfg.Apps.Get(id);if err!=nil{return err}
			root:=filepath.Join(appSnapshotRoot(id),stamp)
			idx,err:=readSnapshotIndex(root,stamp);if err!=nil{return err}
			if idx.AppID!=id||idx.Owner!=a.User||idx.Application!=a.Name{return errors.New("application snapshot mismatch")}
			actual,err:=snapshotFileInfo(root,name);if err!=nil{return err}
			verified:=false
			for _,item:=range idx.Files{if item==actual{verified=true;break}}
			if !verified{return errors.New("database archive failed integrity check")}
			bindings,err:=m.cfg.Databases.AttachmentsForApp(id);if err!=nil{return err}
			for _,binding:=range bindings{
				db,err:=m.cfg.Databases.Get(binding.DatabaseID);if err!=nil{return err}
				if name!="databases/"+db.Engine+"/"+db.Name+".sql.gz"{continue}
				dest:=filepath.Join("/var/lib/open-go-panel/backups/databases",db.Engine,db.Name)
				if err:=os.MkdirAll(dest,0700);err!=nil{return err}
				// Reuse the managed SQL restore and its safety rollback. A
				// hard link avoids copying the entire verified gzip archive.
				link:=filepath.Join(dest,"project-restore-"+stamp+".sql.gz")
				if err:=os.Link(filepath.Join(root,name),link);err!=nil{return err}
				defer os.Remove(link)
				return m.cfg.Databases.RestoreProjectSnapshot(ctx,db.ID,link)
			}
			return errors.New("database is not attached to this application")
		})
	})
	post("/backup/apps/{app}/retrieve",func(r *http.Request)error{
		id,err:=m.appFromRequest(r);if err!=nil{return err}
		stamp:=r.FormValue("id");if !validSnapshotID(stamp){return errors.New("invalid snapshot ID")}
		return m.start("Download application snapshot",func(ctx context.Context)error{
			remote,err:=m.appRemote(id);if err!=nil{return err}
			return m.downloadAt(ctx,stamp,appSnapshotRoot(id),remote,id)
		})
	})
	mux.Handle("GET /backup/apps/{app}/cloud-list",requireAuth(store,http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		id,err:=m.appFromRequest(r);if err!=nil{http.NotFound(w,r);return}
		remote,err:=m.appRemote(id);if err!=nil{http.Error(w,err.Error(),400);return}
		ctx,cancel:=context.WithTimeout(r.Context(),25*time.Second);defer cancel()
		inventory,err:=m.cloudInventoryAt(ctx,remote)
		var out strings.Builder
		out.WriteString(renderCloudInventory(inventory,err,id))
		if r.URL.Query().Get("fragment")=="1"&&r.Header.Get("HX-Request")=="true"{
			writeHTML(w,m.cfg.Logger,200,out.String());return
		}
		writeHTML(w,m.cfg.Logger,200,pageHead("Application cloud snapshots")+"<body>"+appHeader("backup")+`<main class="shell"><section class="panel panel-pad"><div class="section-title"><h1>Application cloud snapshots</h1><a class="secondary" href="/backup">Back</a></div>`+out.String()+"</section></main></body></html>")
	})))
	mux.Handle("GET /backup/apps/{app}/{id}/download/{file...}",requireAuth(store,http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		id,err:=m.appFromRequest(r);if err!=nil{http.NotFound(w,r);return}
		stamp,name:=r.PathValue("id"),r.PathValue("file")
		if !validSnapshotID(stamp)||!validSnapshotPath(name){http.NotFound(w,r);return}
		root:=filepath.Join(appSnapshotRoot(id),stamp)
		idx,err:=readSnapshotIndex(root,stamp);if err!=nil||idx.AppID!=id{http.NotFound(w,r);return}
		found:=false
		for _,f:=range idx.Files{if f.Path==name{found=true;break}}
		if !found{http.NotFound(w,r);return}
		f,err:=os.Open(filepath.Join(root,filepath.FromSlash(name)));if err!=nil{http.NotFound(w,r);return};defer f.Close()
		info,err:=f.Stat();if err!=nil||!info.Mode().IsRegular(){http.NotFound(w,r);return}
		if err:=http.NewResponseController(w).SetWriteDeadline(time.Time{});err!=nil&&!errors.Is(err,http.ErrNotSupported){http.Error(w,"download unavailable",503);return}
		w.Header().Set("Content-Disposition",fmt.Sprintf("attachment; filename=%q",filepath.Base(name)))
		w.Header().Set("Content-Type","application/gzip")
		w.Header().Set("Cache-Control","no-store")
		http.ServeContent(w,r,filepath.Base(name),info.ModTime(),f)
	})))
}

func (m *snapshotManager) renderAppSnapshots()string{
	if m.cfg.Apps==nil{return ""}
	apps,err:=m.cfg.Apps.List()
	if err!=nil{return "<p class=\"snapshot-error\">Cannot list applications.</p>"}
	var out strings.Builder
	out.WriteString(`<section class="panel panel-pad"><h2>Application backup policies</h2><p class="note">Independent schedules for each project. Archives include application files, attached databases and configuration. A zero day interval disables that schedule.</p>`)
	if len(apps)==0{out.WriteString(`<p class="note">No managed applications.</p>`)}
	for _,a:=range apps{
		p:=m.policyFor(m.appPolicyKey(a.ID))
		fmt.Fprintf(&out,`<details class="snapshot-entry"><summary><strong>%s / %s</strong><span>Local: %s · Cloud: %s</span></summary><div class="snapshot-entry-files"><form method="post" action="/backup/apps/%d/policy" class="snapshot-policy"><div><h3>Local</h3><label>Every N days<input name="local_days" type="number" min="0" max="365" value="%d" required></label><label>Hour (UTC)<input name="local_hour" type="number" min="0" max="23" value="%d" required></label><label>Keep versions<input name="local_keep" type="number" min="1" max="100" value="%d" required></label></div><div><h3>Cloud</h3><label>Every N days<input name="cloud_days" type="number" min="0" max="365" value="%d" required></label><label>Hour (UTC)<input name="cloud_hour" type="number" min="0" max="23" value="%d" required></label><label>Keep versions (0 = unlimited)<input name="cloud_keep" type="number" min="0" max="100" value="%d" required></label></div><button class="button">Save project schedules</button></form>`,html.EscapeString(a.User),html.EscapeString(a.Name),snapshotIntervalLabel(p.LocalDays),snapshotIntervalLabel(p.CloudDays),a.ID,p.LocalDays,p.LocalHour,p.LocalKeep,p.CloudDays,p.CloudHour,p.CloudKeep)
		fmt.Fprintf(&out,`<div class="snapshot-actions"><form method="post" action="/backup/apps/%d/create"><button class="secondary">Local backup now</button></form><form method="post" action="/backup/apps/%d/cloud"><button class="secondary">Cloud backup now</button></form><a class="secondary" href="/backup/apps/%d/cloud-list" hx-boost="false" hx-get="/backup/apps/%d/cloud-list?fragment=1" hx-target="#app-cloud-%d" hx-swap="innerHTML">Refresh cloud copies</a></div>`,a.ID,a.ID,a.ID,a.ID,a.ID)
		fmt.Fprintf(&out,`<h3>Cloud copies</h3><div id="app-cloud-%d" hx-get="/backup/apps/%d/cloud-list?fragment=1" hx-trigger="intersect once" hx-swap="innerHTML"><p class="note">Open this section to load cloud copies. Cloud-only snapshots are removed from local storage after upload.</p></div><h3>Local copies</h3>`,a.ID,a.ID)
		list,listErr:=snapshotsAt(appSnapshotRoot(a.ID))
		if listErr!=nil{fmt.Fprintf(&out,`<p class="snapshot-error">%s</p>`,html.EscapeString(listErr.Error()))}
		if listErr==nil&&len(list)==0{out.WriteString(`<p class="note">No local copies.</p>`)}
		for _,idx:=range list{
			if idx.AppID!=a.ID||idx.Owner!=a.User||idx.Application!=a.Name{continue}
			fmt.Fprintf(&out,`<details class="snapshot-entry"><summary><strong>%s</strong><span>%d archives</span></summary><div class="snapshot-entry-files">`,html.EscapeString(idx.CreatedAt.Format("2006-01-02 15:04 UTC")),len(idx.Files))
			for _,f:=range idx.Files{
				link:=fmt.Sprintf("/backup/apps/%d/%s/download/%s",a.ID,idx.ID,f.Path)
				fmt.Fprintf(&out,`<div class="snapshot-row"><code>%s</code><span>%.1f MiB</span><a class="secondary" href="%s">Download</a></div>`,html.EscapeString(f.Path),float64(f.Size)/(1024*1024),html.EscapeString(link))
				if strings.HasPrefix(f.Path,"databases/"){
					fmt.Fprintf(&out,`<details class="snapshot-restore"><summary class="secondary">Restore this database</summary><p class="note">Replaces the currently attached database after creating a safety backup. Other databases and files are unchanged.</p><form method="post" action="/backup/apps/%d/restore-database"><input type="hidden" name="id" value="%s"><input type="hidden" name="file" value="%s"><label>Type RESTORE<input name="confirm" required autocomplete="off"></label><button class="danger">Restore database</button></form></details>`,a.ID,html.EscapeString(idx.ID),html.EscapeString(f.Path))
				}
			}
			fmt.Fprintf(&out,`<form method="post" action="/backup/apps/%d/upload"><input type="hidden" name="id" value="%s"><button class="secondary">Upload this snapshot</button></form><details class="snapshot-restore"><summary class="secondary">Restore files</summary><p class="note">Creates a safety snapshot before replacing project files. Database and application settings are unchanged. PHP 7.4 containers require full server recovery.</p><form method="post" action="/backup/apps/%d/restore-files"><input type="hidden" name="id" value="%s"><label>Type RESTORE<input name="confirm" required autocomplete="off"></label><button class="danger">Restore files</button></form></details></div></details>`,a.ID,html.EscapeString(idx.ID),a.ID,html.EscapeString(idx.ID))
		}
		out.WriteString("</div></details>")
	}
	out.WriteString("</section>")
	return out.String()
}

func snapshotIntervalLabel(days int)string{
	if days==0{return "Off"}
	if days==1{return "Daily"}
	return fmt.Sprintf("Every %d days",days)
}
