package server

import (
    "strings"
    "testing"
    "time"

    "github.com/bau59/open-go-panel/internal/dbmanager"
)

func TestBackupSectionsSeparateStorageAndSQL(t *testing.T) {
    data:=databasePageData{}
    storage:=backupStorageSection(data)
    sql:=databaseBackupSections(data)
    database:=databasesPage(data)
    if !strings.Contains(storage,`name="oauth_token"`)||!strings.Contains(storage,"/databases/backups/offsite/test"){t.Fatal("missing storage connection controls")}
    for _,page:=range []string{sql,database}{
        if strings.Contains(page,`name="oauth_token"`){t.Fatal("duplicated storage connection controls")}
    }
    if !strings.Contains(sql,"/databases/backups/schedule")||!strings.Contains(sql,"/databases/backups/offsite/schedule"){t.Fatal("SQL schedules missing")}
    if strings.Contains(database,"/databases/backups/schedule"){t.Fatal("SQL schedule remains on database page")}
    if !strings.Contains(backupNavigation("storage"),`aria-current="page" href="/backup?section=storage"`){t.Fatal("storage tab not selected")}
}

func TestSQLBackupRowsExplicitLocation(t *testing.T){
    row:=databaseBackupRows(dbmanager.Database{ID:1},[]dbmanager.Backup{{Path:"/tmp/20261125T010203.000000000Z.sql.gz",CreatedAt:time.Date(2026,11,25,1,2,3,0,time.UTC),Size:500}},false,true)
    for _,part:=range []string{"2026-11-25 01:02:03 UTC","Upload to cloud"," disabled",`name="return_to" value="backup"`,"/databases/1/restore","/databases/1/backup/delete"}{
        if !strings.Contains(row,part){t.Errorf("missing %s",part)}
    }
}
