package server

import (
	"strings"
	"testing"

	"github.com/bau59/open-go-panel/internal/dbmanager"
)

func TestSlowLogPageEscapesSQLAndLinksToDatabase(t *testing.T) {
	db := dbmanager.Database{ID: 17, Engine: "mysql", Name: "reports"}
	info := dbmanager.SlowLogInfo{Engine: "mysql", Enabled: true, Threshold: "1 second"}
	page := databaseSlowLogPage(db, info, []dbmanager.SlowQuery{
		{Time: "2026-10-08 10:00:00", DurationMS: 1200, Detail: "SELECT '<script>alert(1)</script>'"},
	}, "")
	for _, value := range []string{
		"Slow queries: reports",
		`action="/databases/17/slow-queries/settings"`,
		"&lt;script&gt;alert(1)&lt;/script&gt;",
	} {
		if !strings.Contains(page, value) { t.Errorf("missing %q", value) }
	}
	if strings.Contains(page, "<script>alert(1)</script>") {
		t.Fatal("slow query was inserted as unescaped HTML")
	}
}

func TestPostgresSlowLogPageAllowsDisabling(t *testing.T) {
	db := dbmanager.Database{ID: 18, Engine: "postgres", Name: "warehouse"}
	page := databaseSlowLogPage(db, dbmanager.SlowLogInfo{Engine: "postgres"}, nil, "")
	if !strings.Contains(page, `<option value="0">Disable for this database</option>`) {
		t.Fatal("PostgreSQL per-database disable option missing")
	}
}
