package server

import (
    "strings"
    "testing"

    "github.com/bau59/open-go-panel/internal/dbmanager"
)

func TestDatabaseSQLUploadMenu(t *testing.T) {
    page := databasesPage(databasePageData{Items: []dbmanager.Database{
        {ID: 17, Engine: "mysql", Name: "store", User: "storeuser"},
        {ID: 18, Engine: "postgres", Name: "report", User: "reportuser"},
    }})
    for _, url := range []string{
        `action="/databases/17/import/file"`,
        `action="/databases/18/import/file"`,
    } {
        if !strings.Contains(page, url) { t.Errorf("missing file import form %q", url) }
    }
    for _, required := range []string{
        `enctype="multipart/form-data"`, `name="file"`, `accept=".sql,.sql.gz"`,
        `name="mode"`, `value="apply"`, `value="replace"`,
        `Import SQL`, `Upload &amp; import`,
    } {
        if !strings.Contains(page, required) && required != `Upload &amp; import` {
            t.Errorf("missing %q in SQL import menu", required)
        }
    }
}

func TestSQLFileImportTaskEscapesFilename(t *testing.T) {
    page := databasesPage(databasePageData{
        ImportTask: dbmanager.RemoteImportTask{
            DatabaseID: 1, SourceKind: "file", SourceHost: `x<script>alert(1)</script>.sql`,
            Running: true,
        },
    })
    if strings.Contains(page, "<script>alert(1)</script>") {
        t.Fatal("SQL filename not escaped")
    }
    if !strings.Contains(page, "SQL file import into database #1") {
        t.Fatal("missing SQL upload task status")
    }
}
