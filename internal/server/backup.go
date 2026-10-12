package server

import (
 "archive/tar"
 "compress/gzip"
 "context"
 "encoding/json"
 "errors"
 "fmt"
 "html"
 "io"
 "net/http"
 "os"
 "os/exec"
 "path/filepath"
 "regexp"
 "sort"
 "strings"
 "time"
)

var backupUsername = regexp.MustCompile("^[a-z_][a-z0-9_-]{0,31}$")

func registerBackupRoutes(mux *http.ServeMux, store *sessionStore, cfg Config) {
 mux.Handle("GET /backup", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  users, err := cfg.Users.List(r.Context())
  if err != nil { http.Error(w, err.Error(), 500); return }
  var rows strings.Builder
  for _, u := range users {
   name := html.EscapeString(u.Username)
   fmt.Fprintf(&rows, "<tr><td><strong>%s</strong></td><td><code>%s</code></td><td><a class=\"secondary\" href=\"/backup/users/%s/config\">Config JSON</a> <a class=\"button\" href=\"/backup/users/%s/archive\">Download archive</a></td></tr>",name,html.EscapeString(u.Home),name,name)
  }
  body := "<section class=\"page-head\"><div><h1>Backup &amp; export</h1><p class=\"muted\">On-demand downloads. No scheduled jobs or server-side archives.</p></div></section>" +
   "<div class=\"grid cards\" style=\"margin-bottom:20px\"><div class=\"panel\"><h2>Panel configuration</h2><p class=\"muted\">Managed users, apps, database bindings and domain configuration.</p><a class=\"button\" href=\"/backup/panel.json\">Download JSON</a></div>" +
   "<div class=\"panel\"><h2>Installed software</h2><p class=\"muted\">Ubuntu APT package installation commands.</p><a class=\"button\" href=\"/backup/software.sh\">Download shell script</a></div>" +
   "<div class=\"panel\"><h2>Docker configurations</h2><p class=\"muted\">Container inspect metadata and image references (not volumes).</p><a class=\"button\" href=\"/backup/docker.json\">Download JSON</a></div></div>" +
   "<section class=\"panel\"><h2>User backups</h2><p class=\"muted\">One tar.gz per user: home directory, app settings and attached database dumps. Downloads can include credentials.</p><div class=\"table-scroll\"><table><thead><tr><th>User</th><th>Home</th><th>Export</th></tr></thead><tbody>"+rows.String()+"</tbody></table></div></section>"
  writeHTML(w,cfg.Logger,200,pageHead("Backup & export")+"<body>"+appHeader("backup")+"<main class=\"shell\">"+body+"</main></body></html>")
 })))

 mux.Handle("GET /backup/panel.json", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  users,err:=cfg.Users.List(r.Context()); if err!=nil {http.Error(w,err.Error(),500);return}
  apps,err:=cfg.Apps.List(); if err!=nil {http.Error(w,err.Error(),500);return}
  manifest:=map[string]any{"format":"open-go-panel-export-v1","exported_at":time.Now().UTC(),"users":users,"apps":apps}
  if cfg.Databases!=nil {
   dbs,e:=cfg.Databases.List();if e!=nil {http.Error(w,e.Error(),500);return}
   manifest["databases"]=dbs
   bindings:=map[int64]any{}
   for _,a:=range apps {b,e:=cfg.Databases.AttachmentsForApp(a.ID);if e!=nil {http.Error(w,e.Error(),500);return};bindings[a.ID]=b}
   manifest["database_bindings"]=bindings
  }
  // Domain/proxy settings and other panel state are saved in the panel's persistent files.
  w.Header().Set("Content-Type","application/json")
  w.Header().Set("Content-Disposition", "attachment; filename=\"panel-config.json\"")
  _=json.NewEncoder(w).Encode(manifest)
 })))

 mux.Handle("GET /backup/users/{username}/config", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  user,apps,dbs,bindings,err:=userBackupData(r.Context(),cfg,r.PathValue("username"))
  if err!=nil {http.Error(w,err.Error(),http.StatusBadRequest);return}
  w.Header().Set("Content-Type","application/json")
  w.Header().Set("Content-Disposition",fmt.Sprintf("attachment; filename=\"%s-config.json\"",user))
  _=json.NewEncoder(w).Encode(map[string]any{"format":"open-go-panel-user-v1","username":user,"apps":apps,"databases":dbs,"database_bindings":bindings})
 })))

 mux.Handle("GET /backup/users/{username}/archive", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  user,apps,dbs,bindings,err:=userBackupData(r.Context(),cfg,r.PathValue("username"))
  if err!=nil {http.Error(w,err.Error(),400);return}
  home:="/home/"+user
  info,err:=os.Lstat(home)
  if err!=nil||!info.IsDir(){http.Error(w,"user home directory unavailable",400);return}
  w.Header().Set("Content-Type","application/gzip")
  w.Header().Set("Content-Disposition",fmt.Sprintf("attachment; filename=\"%s-backup.tar.gz\"",user))
  w.Header().Set("Cache-Control","no-store")
  gz:=gzip.NewWriter(w);defer gz.Close()
  tw:=tar.NewWriter(gz);defer tw.Close()
  meta,_:=json.MarshalIndent(map[string]any{"format":"open-go-panel-user-v1","username":user,"apps":apps,"databases":dbs,"database_bindings":bindings,"exported_at":time.Now().UTC()},"","  ")
  if err:=writeTarBytes(tw,"manifest.json",meta);err!=nil {cfg.Logger.Error("backup manifest failed","err",err);return}
  // Preserve relative paths, ownership metadata and symlinks without dereferencing them.
  if err:=writeTarTree(r.Context(),tw,home,"home/"+user);err!=nil {cfg.Logger.Error("backup home failed","user",user,"err",err);return}
  for _,db:=range dbs {
   var cmd *exec.Cmd
   switch db.Engine {
   case "mysql":
    cmd=exec.CommandContext(r.Context(),"mysqldump","--protocol=socket","-uroot","--single-transaction","--quick","--routines","--triggers","--events","--",db.Name)
   case "postgres":
    cmd=exec.CommandContext(r.Context(),"runuser","-u","postgres","--","pg_dump","--no-owner","--no-privileges",db.Name)
   default: continue
   }
   out,err:=cmd.StdoutPipe();if err!=nil {cfg.Logger.Error("database export pipe failed","db",db.Name,"err",err);return}
   var stderr strings.Builder;cmd.Stderr=&stderr
   if err:=cmd.Start();err!=nil {cfg.Logger.Error("database export start failed","db",db.Name,"err",err);return}
   // Tar requires known size. Spool each dump to a temporary file, then stream to the client.
   tmp,err:=os.CreateTemp("","ogp-db-export-*.sql")
   if err!=nil { _=cmd.Process.Kill();_=cmd.Wait();return }
   size,copyErr:=io.Copy(tmp,out)
   waitErr:=cmd.Wait()
   if copyErr!=nil||waitErr!=nil {cfg.Logger.Error("database export failed","db",db.Name,"copy",copyErr,"process",waitErr,"stderr",stderr.String());tmp.Close();os.Remove(tmp.Name());return}
   if _,err:=tmp.Seek(0,0);err==nil {err=writeTarReader(tw,"databases/"+db.Engine+"/"+db.Name+".sql",size,tmp)}
   tmp.Close();os.Remove(tmp.Name())
   if err!=nil {cfg.Logger.Error("database archive failed","db",db.Name,"err",err);return}
  }
 })))

 mux.Handle("GET /backup/software.sh", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  cmd:=exec.CommandContext(r.Context(),"apt-mark","showmanual")
  data,err:=cmd.Output();if err!=nil {http.Error(w,err.Error(),500);return}
  var packages []string
  for _,p:=range strings.Fields(string(data)){if regexp.MustCompile("^[a-zA-Z0-9][a-zA-Z0-9+._:-]*$").MatchString(p){packages=append(packages,p)}}
  sort.Strings(packages)
  var b strings.Builder
  b.WriteString("#!/bin/sh\nset -eu\n# Generated by Open Go Panel; review before running on a matching Ubuntu release.\napt-get update\napt-get install -y")
  for _,p:=range packages {b.WriteString(" ");b.WriteString(p)}
  b.WriteString("\n")
  w.Header().Set("Content-Type","text/x-shellscript; charset=utf-8")
  w.Header().Set("Content-Disposition","attachment; filename=\"install-packages.sh\"")
  _,_=io.WriteString(w,b.String())
 })))

 mux.Handle("GET /backup/docker.json", requireAuth(store,http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  ids,err:=exec.CommandContext(r.Context(),"docker","ps","-aq").Output()
  if err!=nil {http.Error(w,"docker is unavailable: "+err.Error(),503);return}
  output:=[]json.RawMessage{}
  if len(strings.TrimSpace(string(ids)))>0 {
   args:=append([]string{"inspect"},strings.Fields(string(ids))...)
   data,e:=exec.CommandContext(r.Context(),"docker",args...).Output()
   if e!=nil {http.Error(w,e.Error(),500);return}
   if e:=json.Unmarshal(data,&output);e!=nil {http.Error(w,e.Error(),500);return}
  }
  w.Header().Set("Content-Type","application/json")
  w.Header().Set("Content-Disposition","attachment; filename=\"docker-config.json\"")
  _=json.NewEncoder(w).Encode(map[string]any{"format":"open-go-panel-docker-v1","containers":output})
 })))
}

func userBackupData(ctx context.Context,cfg Config,username string)(string,[]any,[]backupDatabase,map[int64]any,error){
 if !backupUsername.MatchString(username){return "",nil,nil,nil,errors.New("invalid username")}
 users,err:=cfg.Users.List(ctx);if err!=nil{return "",nil,nil,nil,err}
 found:=false
 for _,u:=range users {if u.Username==username && filepath.Clean(u.Home)=="/home/"+username {found=true;break}}
 if !found{return "",nil,nil,nil,errors.New("user is not managed by panel")}
 all,err:=cfg.Apps.List();if err!=nil{return "",nil,nil,nil,err}
 apps:=[]any{};dbs:=[]backupDatabase{};bindings:=map[int64]any{};seen:=map[int64]bool{}
 for _,a:=range all {
  if a.User!=username {continue}
  apps=append(apps,a)
  if cfg.Databases==nil {continue}
  bs,e:=cfg.Databases.AttachmentsForApp(a.ID);if e!=nil{return "",nil,nil,nil,e}
  bindings[a.ID]=bs
  for _,att:=range bs{
   if seen[att.DatabaseID]{continue}
   d,e:=cfg.Databases.Get(att.DatabaseID);if e!=nil{return "",nil,nil,nil,e}
   dbs=append(dbs,backupDatabase{ID:d.ID,Engine:d.Engine,Name:d.Name,User:d.User})
   seen[d.ID]=true
  }
 }
 return username,apps,dbs,bindings,nil
}
type backupDatabase struct {ID int64 `json:"id"`;Engine string `json:"engine"`;Name string `json:"name"`;User string `json:"user"`}

func writeTarBytes(tw *tar.Writer,name string,data []byte)error{return writeTarReader(tw,name,int64(len(data)),strings.NewReader(string(data)))}
func writeTarReader(tw *tar.Writer,name string,size int64,r io.Reader)error{
 if err:=tw.WriteHeader(&tar.Header{Name:name,Mode:0600,Size:size,ModTime:time.Now()});err!=nil{return err}
 _,err:=io.CopyN(tw,r,size);return err
}
func writeTarTree(ctx context.Context,tw *tar.Writer,root,prefix string)error{
 return filepath.Walk(root,func(path string,info os.FileInfo,walkErr error)error{
  if walkErr!=nil{return walkErr}
  if err:=ctx.Err();err!=nil{return err}
  rel,err:=filepath.Rel(root,path);if err!=nil{return err}
  if rel=="."{rel=""}
  name:=filepath.ToSlash(filepath.Join(prefix,rel))
  link:=""
  if info.Mode()&os.ModeSymlink!=0{link,err=os.Readlink(path);if err!=nil{return err}}
  // Sockets cannot be preserved in tar; their runtime state is not useful in a backup.
  if info.Mode()&os.ModeSocket!=0{return nil}
  hdr,err:=tar.FileInfoHeader(info,link);if err!=nil{return err}
  hdr.Name=name
  if info.IsDir(){hdr.Name+="/"}
  if err:=tw.WriteHeader(hdr);err!=nil{return err}
  if !info.Mode().IsRegular(){return nil}
  f,err:=os.Open(path);if err!=nil{return err};defer f.Close()
  _,err=io.Copy(tw,f);return err
 })
}
