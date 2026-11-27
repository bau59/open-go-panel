package server

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bau59/open-go-panel/internal/app"
	"github.com/bau59/open-go-panel/internal/dbmanager"
	"github.com/bau59/open-go-panel/internal/linuxuser"
	"github.com/bau59/open-go-panel/internal/phpfpm"
	"github.com/bau59/open-go-panel/internal/state"
	"github.com/bau59/open-go-panel/internal/terminal"
)

const livePanelDB = "/var/lib/open-go-panel/panel.db"

var managedUnitRE=regexp.MustCompile(`^open-go-panel-(app-[0-9]+\.service|cron-[0-9]+\.(service|timer))$`)
var managedDependencyRE=regexp.MustCompile(`^open-go-panel-app-[0-9]+\.service\.d/50-ogp-dependencies\.confpackage server

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bau59/open-go-panel/internal/app"
	"github.com/bau59/open-go-panel/internal/dbmanager"
	"github.com/bau59/open-go-panel/internal/linuxuser"
	"github.com/bau59/open-go-panel/internal/phpfpm"
	"github.com/bau59/open-go-panel/internal/state"
	"github.com/bau59/open-go-panel/internal/terminal"
)

const livePanelDB = "/var/lib/open-go-panel/panel.db"

var managedUnitRE=regexp.MustCompile(`^open-go-panel-(app-[0-9]+\.service|cron-[0-9]+\.(service|timer))$`)
)
var journalConfigRE=regexp.MustCompile(`^etc/systemd/journald@open-go-panel-[A-Za-z0-9_-]+\.conf$`)
var phpConfigRE=regexp.MustCompile(`^etc/php/[0-9]+\.[0-9]+/(fpm/pool\.d/ogp-app-[0-9]+\.conf|mods-available/[A-Za-z0-9_-]*ioncube[A-Za-z0-9_-]*\.ini|fpm/conf\.d/[A-Za-z0-9_-]*ioncube[A-Za-z0-9_-]*\.ini)$`)
var groupNameRE=regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

func snapshotSystemPath(name string)bool{
	for _,prefix:=range []string{"etc/open-go-panel","etc/caddy","var/lib/open-go-panel/runners","var/lib/open-go-panel/env","usr/local/lib/open-go-panel/ioncube"}{
		if name==prefix||strings.HasPrefix(name,prefix+"/"){return true}
	}
	if strings.HasPrefix(name,"etc/systemd/system/"){
		relative:=strings.TrimPrefix(name,"etc/systemd/system/")
		return managedUnitRE.MatchString(relative)||managedDependencyRE.MatchString(relative)
	}
	return name=="etc/mysql/mysql.conf.d/99-open-go-panel.cnf"||journalConfigRE.MatchString(name)||phpConfigRE.MatchString(name)
}

func snapshotConfigEntry(name string)bool{
	switch name{
	case "panel.db","accounts.json","units.json","running-commands.json","packages.txt","docker.json":return true
	}
	return strings.HasPrefix(name,"system/")&&snapshotSystemPath(strings.TrimPrefix(name,"system/"))
}

func cleanArchiveName(name string)(string,error){
	name=strings.TrimSuffix(strings.TrimPrefix(name,"./"),"/")
	if name==""||name=="."||filepath.IsAbs(name)||strings.ContainsAny(name,"\\\x00"){return "",errors.New("invalid archive path")}
	for _,part:=range strings.Split(name,"/"){if part==""||part=="."||part==".."{return "",errors.New("invalid archive path")}}
	return name,nil
}

// Configuration is extracted only into a private empty staging directory.
// Symbolic links are installed last and never used as archive parents.
func extractSnapshotConfig(ctx context.Context,archive,dir string)error{
	f,err:=os.Open(archive);if err!=nil{return err};defer f.Close()
	gz,err:=gzip.NewReader(f);if err!=nil{return err};defer gz.Close()
	tr:=tar.NewReader(gz)
	type link struct{path,target string}
	var links []link
	seen:=map[string]bool{}
	for{
		if err:=ctx.Err();err!=nil{return err}
		h,err:=tr.Next()
		if errors.Is(err,io.EOF){break}
		if err!=nil{return err}
		name,err:=cleanArchiveName(h.Name)
		if err!=nil{return err}
		if !snapshotConfigEntry(name)||seen[name]{return fmt.Errorf("unexpected configuration entry: %s",name)}
		if managedDependencyRE.MatchString(strings.TrimPrefix(name,"system/etc/systemd/system/"))&&h.Typeflag!=tar.TypeReg&&h.Typeflag!=tar.TypeRegA{return errors.New("managed dependency drop-in must be a regular file")}
		seen[name]=true
		dst:=filepath.Join(dir,filepath.FromSlash(name))
		if err:=os.MkdirAll(filepath.Dir(dst),0700);err!=nil{return err}
		switch h.Typeflag{
		case tar.TypeDir:
			if !strings.HasPrefix(name,"system/"){return errors.New("invalid metadata directory")}
			if err:=os.MkdirAll(dst,0700);err!=nil{return err}
		case tar.TypeReg,tar.TypeRegA:
			out,err:=os.OpenFile(dst,os.O_CREATE|os.O_EXCL|os.O_WRONLY,0600)
			if err!=nil{return err}
			_,copyErr:=io.CopyN(out,tr,h.Size)
			closeErr:=out.Close()
			if copyErr!=nil{return copyErr};if closeErr!=nil{return closeErr}
			if strings.HasPrefix(name,"system/"){
				if err:=os.Chmod(dst,os.FileMode(h.Mode)&0777);err!=nil{return err}
			}
		case tar.TypeSymlink:
			if !strings.HasPrefix(name,"system/")||h.Linkname==""||strings.ContainsRune(h.Linkname,0){return errors.New("invalid configuration symlink")}
			links=append(links,link{dst,h.Linkname})
		default:return fmt.Errorf("unsupported configuration entry: %s",name)
		}
	}
	if _,err:=io.Copy(io.Discard,gz);err!=nil{return err}
	for _,l:=range links{
		if err:=os.Symlink(l.target,l.path);err!=nil{return err}
	}
	for _,required:=range []string{"panel.db","accounts.json","units.json","running-commands.json"}{
		if !seen[required]{return fmt.Errorf("missing snapshot metadata: %s",required)}
	}
	return nil
}

func readRestoreJSON(dir,name string,out any)error{
	f,err:=os.Open(filepath.Join(dir,name));if err!=nil{return err};defer f.Close()
	return json.NewDecoder(io.LimitReader(f,8<<20)).Decode(out)
}

func checkFreshServer(ctx context.Context,cfg Config)error{
	users,err:=cfg.Users.List(ctx)
	if err!=nil{return err}
	if len(users)>0{return errors.New("restore requires a fresh server with no managed Linux users")}
	for _,table:=range []string{"apps","databases","domains","standalone_domains","background_commands"}{
		var n int
		if err:=cfg.State.DB().QueryRowContext(ctx,"SELECT COUNT(*) FROM "+table).Scan(&n);err!=nil{return err}
		if n!=0{return fmt.Errorf("restore requires an empty panel: %s is not empty",table)}
	}
	return nil
}

func validateRestoreAccount(a snapshotAccount)error{
	if !backupUsername.MatchString(a.Username)||a.Home!="/home/"+a.Username||!groupNameRE.MatchString(a.PrimaryGroup)||!filepath.IsAbs(a.Shell)||strings.ContainsAny(a.Shell,"\r\n\x00"){return errors.New("invalid managed account metadata")}
	uid,err:=strconv.Atoi(a.UID);if err!=nil||uid<1000||uid>=2147483647{return errors.New("invalid managed UID")}
	gid,err:=strconv.Atoi(a.GID);if err!=nil||gid<1||gid>=2147483647{return errors.New("invalid managed GID")}
	fields:=strings.Split(a.Shadow,":")
	if len(fields)!=9||fields[0]!=a.Username||strings.ContainsAny(a.Shadow,"\r\n\x00"){return errors.New("invalid account password metadata")}
	for _,group:=range a.Groups{if !groupNameRE.MatchString(group){return errors.New("invalid supplementary group")}}
	return nil
}

func checkRestoreAccounts(accounts []snapshotAccount)error{
	names,ids:=map[string]bool{},map[string]bool{}
	groupIDs:=map[string]string{}
	for _,a:=range accounts{
		if err:=validateRestoreAccount(a);err!=nil{return err}
		if names[a.Username]||ids[a.UID]{return errors.New("duplicate managed account")}
		names[a.Username]=true;ids[a.UID]=true
		if old,ok:=groupIDs[a.GID];ok&&old!=a.PrimaryGroup{return errors.New("conflicting primary group IDs")};groupIDs[a.GID]=a.PrimaryGroup
		if _,err:=user.Lookup(a.Username);err==nil{return fmt.Errorf("Linux user already exists: %s",a.Username)}else{var unknown user.UnknownUserError;if !errors.As(err,&unknown){return err}}
		if _,err:=user.LookupId(a.UID);err==nil{return fmt.Errorf("Linux UID already in use: %s",a.UID)}else{var unknown user.UnknownUserIdError;if !errors.As(err,&unknown){return err}}
		if _,err:=os.Lstat(a.Home);err==nil{return fmt.Errorf("home already exists: %s",a.Home)}else if !errors.Is(err,os.ErrNotExist){return err}
		if g,err:=user.LookupGroup(a.PrimaryGroup);err==nil&&g.Gid!=a.GID{return fmt.Errorf("group ID differs: %s",a.PrimaryGroup)}
		if g,err:=user.LookupGroupId(a.GID);err==nil&&g.Name!=a.PrimaryGroup{return fmt.Errorf("group ID already used: %s",a.GID)}
	}
	return nil
}

func restoreCommand(ctx context.Context,stdin io.Reader,name string,args ...string)error{
	cmd:=exec.CommandContext(ctx,name,args...)
	cmd.Stdin=stdin
	cmd.Stdout=io.Discard
	cmd.Stderr=io.Discard
	if err:=cmd.Run();err!=nil{return fmt.Errorf("%s %s: %w",name,strings.Join(args," "),err)}
	return nil
}

func restoreAccount(ctx context.Context,a snapshotAccount,archive string)error{
	if _,err:=user.LookupGroup(a.PrimaryGroup);err!=nil{
		if err:=restoreCommand(ctx,nil,"groupadd","--gid",a.GID,a.PrimaryGroup);err!=nil{return err}
	}
	for _,g:=range a.Groups{
		if g==a.PrimaryGroup{continue}
		if _,err:=user.LookupGroup(g);err!=nil{return fmt.Errorf("required group missing: %s",g)}
	}
	groups:=[]string{"ogp-users"}
	seenGroups:=map[string]bool{"ogp-users":true,a.PrimaryGroup:true}
	for _,g:=range a.Groups{if !seenGroups[g]{groups=append(groups,g);seenGroups[g]=true}}
	// useradd assigns the archived UID/GID and the original supplementary
	// group names; no existing account is modified.
	args:=[]string{"--uid",a.UID,"--gid",a.PrimaryGroup,"--home-dir",a.Home,"--shell",a.Shell,"--create-home","--groups",strings.Join(groups,","),a.Username}
	if err:=restoreCommand(ctx,nil,"useradd",args...);err!=nil{return err}
	fields:=strings.Split(a.Shadow,":")
	if err:=restoreCommand(ctx,strings.NewReader(a.Username+":"+fields[1]+"\n"),"chpasswd","-e");err!=nil{return err}
	// Restore expiry and password aging from the original shadow record.
	aging:=[]string{}
	for i,flag:=range []string{"-d","-m","-M","-W","-I","-E"}{
		if fields[i+2]!=""{
			if _,err:=strconv.ParseInt(fields[i+2],10,64);err!=nil{return errors.New("invalid password aging metadata")}
			aging=append(aging,flag,fields[i+2])
		}
	}
	if len(aging)>0{
		if err:=restoreCommand(ctx,nil,"chage",append(aging,a.Username)...);err!=nil{return err}
	}
	f,err:=os.Open(archive);if err!=nil{return err};defer f.Close()
	// Extraction runs as the original user, not root. The user cannot
	// create privileged files or follow a symlink into protected host data.
	return restoreCommand(ctx,f,"runuser","-u",a.Username,"--","/usr/bin/tar","--acls","--xattrs","--no-same-owner","--same-permissions","-xzf","-","-C",a.Home)
}

func safeSystemParents(dst string)error{
	dir:=filepath.Dir(dst)
	var parents []string
	for dir!="/"&&dir!="."{parents=append(parents,dir);dir=filepath.Dir(dir)}
	for i:=len(parents)-1;i>=0;i--{
		info,err:=os.Lstat(parents[i])
		if errors.Is(err,os.ErrNotExist){
			if err:=os.Mkdir(parents[i],0755);err!=nil{return err};continue
		}
		if err!=nil{return err}
		if !info.IsDir()||info.Mode()&os.ModeSymlink!=0{return fmt.Errorf("unsafe configuration parent: %s",parents[i])}
	}
	return nil
}

func copyRestoreFile(src,dst string,mode os.FileMode)error{
	if err:=safeSystemParents(dst);err!=nil{return err}
	f,err:=os.Open(src);if err!=nil{return err};defer f.Close()
	tmp,err:=os.CreateTemp(filepath.Dir(dst),".ogp-restore-*");if err!=nil{return err}
	defer os.Remove(tmp.Name());defer tmp.Close()
	if _,err:=io.Copy(tmp,f);err!=nil{return err}
	if err:=tmp.Chmod(mode.Perm());err!=nil{return err}
	if err:=tmp.Sync();err!=nil{return err}
	if err:=tmp.Close();err!=nil{return err}
	return os.Rename(tmp.Name(),dst)
}

func installSnapshotConfig(dir string)error{
	root:=filepath.Join(dir,"system")
	return filepath.WalkDir(root,func(src string,entry os.DirEntry,err error)error{
		if err!=nil{return err}
		rel,err:=filepath.Rel(root,src);if err!=nil{return err}
		if rel=="."{return nil}
		rel=filepath.ToSlash(rel)
		if !snapshotSystemPath(rel){
			// Parent directories created during extraction are not themselves
			// managed configuration and must never have their modes changed.
			if entry.IsDir(){return nil};return fmt.Errorf("unexpected configuration path: %s",rel)
		}
		dst:="/"+rel
		if err:=safeSystemParents(dst);err!=nil{return err}
		info,err:=entry.Info();if err!=nil{return err}
		if info.IsDir(){
			if old,err:=os.Lstat(dst);err==nil{
				if !old.IsDir()||old.Mode()&os.ModeSymlink!=0{return fmt.Errorf("unsafe configuration destination: %s",dst)}
				return nil
			}else if !errors.Is(err,os.ErrNotExist){return err}
			return os.Mkdir(dst,0755)
		}
		if info.Mode()&os.ModeSymlink!=0{
			target,err:=os.Readlink(src);if err!=nil{return err}
			tmp,err:=os.MkdirTemp(filepath.Dir(dst),".ogp-restore-link-*");if err!=nil{return err};defer os.RemoveAll(tmp)
			link:=filepath.Join(tmp,"link")
			if err:=os.Symlink(target,link);err!=nil{return err}
			return os.Rename(link,dst)
		}
		if !info.Mode().IsRegular(){return errors.New("unsupported configuration file")}
		return copyRestoreFile(src,dst,info.Mode())
	})
}

// RunServerRestore runs as a transient root systemd job, outside the panel
// process. It refuses populated hosts and verifies the complete snapshot
// before changing the running server. A failed operation is not transactional
// across Linux accounts, files and database engines; use a clean VPS.
func RunServerRestore(ctx context.Context,id string,logger *slog.Logger) (err error){
	if os.Geteuid()!=0||!validSnapshotID(id){return errors.New("restore requires root and a valid snapshot ID")}
	root:=filepath.Join(snapshotRoot,id)
	idx,err:=readSnapshotIndex(root,id);if err!=nil{return err}
	if idx.AppID!=0{return errors.New("expected a full server snapshot")}
	verified:=map[string]bool{}
	for _,item:=range idx.Files{
		verified[item.Path]=true
		actual,err:=snapshotFileInfo(root,item.Path);if err!=nil{return err}
		if actual!=item{return fmt.Errorf("snapshot file failed integrity check: %s",item.Path)}
	}
	dir,err:=os.MkdirTemp(snapshotRoot,".restore-stage-*");if err!=nil{return err};defer os.RemoveAll(dir)
	if err:=extractSnapshotConfig(ctx,filepath.Join(root,"config.tar.gz"),dir);err!=nil{return err}
	var accounts []snapshotAccount
	var units []snapshotUnit
	var commands []string
	if err:=readRestoreJSON(dir,"accounts.json",&accounts);err!=nil{return err}
	if err:=readRestoreJSON(dir,"units.json",&units);err!=nil{return err}
	if err:=readRestoreJSON(dir,"running-commands.json",&commands);err!=nil{return err}
	if err:=checkRestoreAccounts(accounts);err!=nil{return err}
	for _,unit:=range units{if !managedUnitRE.MatchString(unit.Name){return errors.New("invalid managed unit name")}}
	for _,a:=range accounts{
		if !verified["files/"+a.Username+".tar.gz"]{return fmt.Errorf("missing home archive for %s",a.Username)}
	}
	saved,err:=state.Open(filepath.Join(dir,"panel.db"));if err!=nil{return err};defer saved.Close()
	var integrity string
	if err:=saved.DB().QueryRowContext(ctx,"PRAGMA integrity_check").Scan(&integrity);err!=nil{return err}
	if integrity!="ok"{return errors.New("invalid snapshot SQLite database")}
	users:=linuxuser.New(logger)
	apps:=app.New(saved,filepath.Join(dir,"nonexistent-apps.json"),users)
	databases:=dbmanager.New(saved,filepath.Join(dir,"nonexistent-databases.json"))
	appItems,err:=apps.List();if err!=nil{return err}
	dbItems,err:=databases.List();if err!=nil{return err}
	for _,item:=range dbItems{
		if err:=dbmanager.CheckSnapshotDatabase(ctx,item);err!=nil{return fmt.Errorf("database preflight %s: %w",item.Name,err)}
		if !verified["databases/"+item.Engine+"/"+item.Name+".sql.gz"]{return fmt.Errorf("missing dump for %s",item.Name)}
	}
	// Verify supplementary groups and runtimes before creating accounts.
	for _,a:=range accounts{
		for _,g:=range a.Groups{
			if g=="ogp-users"||g==a.PrimaryGroup{continue}
			if _,err:=user.LookupGroup(g);err!=nil{return fmt.Errorf("required group missing: %s",g)}
		}
	}
	for _,item:=range appItems{
		if item.Type=="php"&&item.Service.PHPVersion!=phpfpm.LegacyVersion{
			if _,err:=phpfpm.Detect();err!=nil{return err}
		}
		if item.Type=="php"&&item.Service.PHPVersion==phpfpm.LegacyVersion&&!phpfpm.LegacyImageInstalled(ctx){return errors.New("install the PHP 7.4 runtime before recovery")}
	}
	fresh,err:=state.Open(livePanelDB);if err!=nil{return err}
	freshCfg:=Config{Logger:logger,State:fresh,Users:users,Apps:app.New(fresh,"",users),Databases:dbmanager.New(fresh,"")}
	if err:=checkFreshServer(ctx,freshCfg);err!=nil{fresh.Close();return err}
	// Preserve the newly connected Yandex credentials and destination. The
	// credential file is outside the restored config and is never uploaded.
	for _,key:=range []string{"db.offsite_provider","db.backup_remote","db.offsite_yandex_scope"}{
		value,ok,err:=fresh.Setting(key);if err!=nil{fresh.Close();return err}
		if ok{if err:=saved.SetSetting(key,value);err!=nil{fresh.Close();return err}}
	}
	for _,key:=range []string{"snapshot.cloud_pending","snapshot.local_last","snapshot.cloud_last","db.offsite_last_date"}{
		if err:=saved.SetSetting(key,"");err!=nil{fresh.Close();return err}
	}
	if err:=fresh.Close();err!=nil{return err}
	// OAuth grants are host-local. Also clear them when restoring a snapshot.
	if _,err:=saved.DB().ExecContext(ctx,"DELETE FROM mcp_connections");err!=nil{return err}
	ready:=filepath.Join(dir,"ready.db")
	if _,err:=saved.DB().ExecContext(ctx,"VACUUM INTO ?",ready);err!=nil{return err}
	if err:=saved.Close();err!=nil{return err}
	logger.Info("restore preflight passed","snapshot",id)
	// Stop the independent SQLite client before replacing the panel database.
	mcpWasActive:=exec.CommandContext(ctx,"systemctl","is-active","--quiet","open-go-panel-mcp.service").Run()==nil
	if mcpWasActive{
		if err:=restoreCommand(ctx,nil,"systemctl","stop","open-go-panel-mcp.service");err!=nil{return err}
		defer func(){restartCtx,cancel:=context.WithTimeout(context.Background(),30*time.Second);defer cancel();if e:=restoreCommand(restartCtx,nil,"systemctl","start","open-go-panel-mcp.service");err==nil{err=e}}()
	}
	if err:=restoreCommand(ctx,nil,"systemctl","stop","open-go-panel.service");err!=nil{return err}
	defer func(){
		// A failed operation leaves the safety copy and journal available. Do
		// not hide a partial cross-system restore behind a success indicator.
		restartCtx,cancel:=context.WithTimeout(context.Background(),30*time.Second);defer cancel()
		if e:=restoreCommand(restartCtx,nil,"systemctl","start","open-go-panel.service");err==nil{err=e}
	}()
	fresh,err=state.Open(livePanelDB);if err!=nil{return err}
	freshCfg.State=fresh
	if err:=checkFreshServer(ctx,freshCfg);err!=nil{fresh.Close();return err}
	safety:=filepath.Join(snapshotRoot,"restore-safety-"+id)
	if err:=os.Mkdir(safety,0700);err!=nil{fresh.Close();return err}
	if err:=buildSnapshotConfig(ctx,freshCfg,safety);err!=nil{fresh.Close();return err}
	if err:=fresh.Close();err!=nil{return err}
	if err:=users.EnsureGroup(ctx);err!=nil{return err}
	for _,a:=range accounts{
		logger.Info("restore Linux user","user",a.Username)
		if err:=restoreAccount(ctx,a,filepath.Join(root,"files",a.Username+".tar.gz"));err!=nil{return err}
	}
	for _,item:=range dbItems{
		logger.Info("restore database","database",item.Name)
		if err:=dbmanager.RestoreSnapshotDatabase(ctx,item,filepath.Join(root,"databases",item.Engine,item.Name+".sql.gz"));err!=nil{return err}
	}
	if err:=installSnapshotConfig(dir);err!=nil{return err}
	// Fresh PHP pools are recreated with the restored account ownership.
	for _,item:=range appItems{
		if item.Type!="php"{continue}
		if item.Service.PHPVersion==phpfpm.LegacyVersion{
			if err:=phpfpm.EnsureLegacy(ctx,item.ID,item.User,item.Root,item.Port);err!=nil{return err}
		}else if err:=phpfpm.Ensure(ctx,item.ID,item.User);err!=nil{return err}
	}
	if err:=restoreCommand(ctx,nil,"caddy","validate","--config","/etc/caddy/Caddyfile","--adapter","caddyfile");err!=nil{return err}
	if err:=restoreCommand(ctx,nil,"systemctl","daemon-reload");err!=nil{return err}
	for _,unit:=range units{
		action:="disable";if unit.Enabled{action="enable"}
		if err:=restoreCommand(ctx,nil,"systemctl",action,unit.Name);err!=nil{return err}
	}
	// The live database is closed and the ready copy is self-contained.
	// Remove empty WAL/SHM artifacts from the stopped fresh installation.
	for _,suffix:=range []string{"-wal","-shm"}{if err:=os.Remove(livePanelDB+suffix);err!=nil&&!errors.Is(err,os.ErrNotExist){return err}}
	if err:=copyRestoreFile(ready,livePanelDB,0600);err!=nil{return err}
	if err:=restoreCommand(ctx,nil,"systemctl","reload","caddy.service");err!=nil{return err}
	for _,unit:=range units{
		if unit.Active&&strings.HasSuffix(unit.Name,".service")&&strings.Contains(unit.Name,"-cron-"){continue}
		if unit.Active{if err:=restoreCommand(ctx,nil,"systemctl","start",unit.Name);err!=nil{return err}}
	}
	current,err:=state.Open(livePanelDB);if err!=nil{return err};defer current.Close()
	manager:=terminal.NewBackgroundManager(current)
	for _,command:=range commands{
		if err:=manager.ResumeSnapshotCommand(ctx,command);err!=nil{return err}
	}
	logger.Info("managed server recovery completed","snapshot",id,"safety",safety)
	return nil
}
