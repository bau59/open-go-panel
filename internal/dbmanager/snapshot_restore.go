package dbmanager

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// Snapshot passwords produced by Open Go Panel are URL-safe random strings.
// Reject other formats rather than interpolating arbitrary SQL credentials.
var snapshotPasswordRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func ValidateSnapshotDatabase(item Database) error {
	if !databaseNameRE.MatchString(item.Name) || !snapshotPasswordRE.MatchString(item.Password) {
		return errors.New("invalid snapshot database name or password")
	}
	switch item.Engine {
	case "mysql":
		if !mysqlUserRE.MatchString(item.User) || strings.EqualFold(item.User,"root") || strings.EqualFold(item.Name,"mysql") || strings.EqualFold(item.Name,"sys") || strings.EqualFold(item.Name,"performance_schema") || strings.EqualFold(item.Name,"information_schema") {
			return errors.New("invalid managed MySQL account")
		}
	case "postgres":
		if !postgresUserRE.MatchString(item.User) || strings.EqualFold(item.User,"postgres") || strings.EqualFold(item.Name,"postgres") || strings.EqualFold(item.Name,"template0") || strings.EqualFold(item.Name,"template1") {
			return errors.New("invalid managed PostgreSQL account")
		}
	default:
		return errors.New("unsupported database engine")
	}
	return nil
}

func snapshotSQL(ctx context.Context, item Database, sql string, output bool) ([]byte,error) {
	var cmd *exec.Cmd
	if item.Engine=="mysql"{
		cmd=exec.CommandContext(ctx,"mysql","--protocol=socket","-uroot","--batch","--skip-column-names")
	}else{
		cmd=exec.CommandContext(ctx,"runuser","-u","postgres","--","psql","-X","-A","-t","-v","ON_ERROR_STOP=1","-d","postgres")
	}
	cmd.Stdin=strings.NewReader(sql)
	cmd.Stderr=io.Discard
	if output{
		data,err:=cmd.Output()
		if err!=nil{return nil,fmt.Errorf("query %s: %w",item.Engine,err)}
		return data,nil
	}
	cmd.Stdout=io.Discard
	if err:=cmd.Run();err!=nil{return nil,fmt.Errorf("configure %s: %w",item.Engine,err)}
	return nil,nil
}

// CheckSnapshotDatabase checks that neither a database nor a login would be
// overwritten. It does not make any database changes.
func CheckSnapshotDatabase(ctx context.Context,item Database)error{
	if err:=ValidateSnapshotDatabase(item);err!=nil{return err}
	sql:=""
	if item.Engine=="mysql"{
		sql=fmt.Sprintf("SELECT (SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME='%s')+(SELECT COUNT(*) FROM mysql.user WHERE User='%s');",item.Name,item.User)
	}else{
		sql=fmt.Sprintf("SELECT (SELECT COUNT(*) FROM pg_database WHERE datname='%s')+(SELECT COUNT(*) FROM pg_roles WHERE rolname='%s');",item.Name,item.User)
	}
	data,err:=snapshotSQL(ctx,item,sql,true)
	if err!=nil{return err}
	if strings.TrimSpace(string(data))!="0"{return fmt.Errorf("database or user already exists: %s",item.Name)}
	return nil
}

// RestoreSnapshotDatabase provisions one absent managed database with its
// original credentials and imports the verified dump using that database user.
// The caller runs the fresh-host preflight for all databases before mutation.
func RestoreSnapshotDatabase(ctx context.Context,item Database,filename string)error{
	if err:=CheckSnapshotDatabase(ctx,item);err!=nil{return err}
	f,err:=os.Open(filename)
	if err!=nil{return err}
	defer f.Close()
	gz,err:=gzip.NewReader(f)
	if err!=nil{return err}
	defer gz.Close()
	createdDB,createdUser:=false,false
	succeeded:=false
	defer func(){
		if succeeded{return}
		cleanup,cancel:=context.WithCancel(context.Background())
		defer cancel()
		if item.Engine=="mysql"{
			if createdDB{_,_=snapshotSQL(cleanup,item,fmt.Sprintf("DROP DATABASE `%s`;",item.Name),false)}
			if createdUser{_,_=snapshotSQL(cleanup,item,fmt.Sprintf("DROP USER '%s'@'localhost';",item.User),false)}
		}else{
			if createdDB{_,_=snapshotSQL(cleanup,item,fmt.Sprintf("DROP DATABASE \"%s\";",item.Name),false)}
			if createdUser{_,_=snapshotSQL(cleanup,item,fmt.Sprintf("DROP ROLE \"%s\";",item.User),false)}
		}
	}()
	var cmd *exec.Cmd
	if item.Engine=="mysql"{
		if _,err:=snapshotSQL(ctx,item,fmt.Sprintf("CREATE DATABASE `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;",item.Name),false);err!=nil{return err}
		createdDB=true
		if _,err:=snapshotSQL(ctx,item,fmt.Sprintf("CREATE USER '%s'@'localhost' IDENTIFIED BY '%s';",item.User,item.Password),false);err!=nil{return err}
		createdUser=true
		if _,err:=snapshotSQL(ctx,item,fmt.Sprintf("GRANT ALL PRIVILEGES ON `%s`.* TO '%s'@'localhost';",item.Name,item.User),false);err!=nil{return err}
		// A private defaults file avoids exposing the password in argv.
		creds,err:=os.CreateTemp("","ogp-restore-mysql-*")
		if err!=nil{return err}
		defer os.Remove(creds.Name())
		_,writeErr:=fmt.Fprintf(creds,"[client]\nuser=%s\npassword=%s\n",item.User,item.Password)
		closeErr:=creds.Close()
		if writeErr!=nil{return writeErr}
		if closeErr!=nil{return closeErr}
		cmd=exec.CommandContext(ctx,"mysql","--defaults-file="+creds.Name(),"--protocol=socket",item.Name)
	}else{
		if _,err:=snapshotSQL(ctx,item,fmt.Sprintf("CREATE ROLE \"%s\" LOGIN PASSWORD '%s';",item.User,item.Password),false);err!=nil{return err}
		createdUser=true
		if _,err:=snapshotSQL(ctx,item,fmt.Sprintf("CREATE DATABASE \"%s\" OWNER \"%s\";",item.Name,item.User),false);err!=nil{return err}
		createdDB=true
		cmd=exec.CommandContext(ctx,"psql","-X","-w","-h","127.0.0.1","-U",item.User,"-v","ON_ERROR_STOP=1","-d",item.Name)
		cmd.Env=append(os.Environ(),"PGPASSWORD="+item.Password)
	}
	cmd.Stdin=gz
	cmd.Stdout=io.Discard
	cmd.Stderr=io.Discard
	if err:=cmd.Run();err!=nil{return fmt.Errorf("restore %s: %w",item.Name,err)}
	succeeded=true
	return nil
}
