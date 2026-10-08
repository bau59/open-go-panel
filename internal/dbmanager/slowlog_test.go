package dbmanager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParsePostgresSlowQueriesOnlySelectedDatabase(t *testing.T) {
	log := strings.Join([]string{
		"2026-10-08 10:00:00 UTC [111] app@first LOG:  duration: 1250.350 ms  statement: SELECT * FROM orders",
		"2026-10-08 10:01:00 UTC [222] app@second LOG:  duration: 9000.000 ms  statement: SELECT secret FROM accounts",
		"2026-10-08 10:02:00 UTC [111] app@first LOG:  duration: 2200.000 ms  statement: UPDATE orders SET status='done'",
		"2026-10-08 10:03:00 UTC [111] app@first ERROR: syntax error near SELECT",
	}, "\n")
	entries := parsePostgresSlowQueries(log, "first", 100)
	if len(entries) != 2 {
		t.Fatalf("expected 2 slow queries, got %d: %#v", len(entries), entries)
	}
	if entries[0].DurationMS != 2200 || !strings.Contains(entries[0].Detail, "UPDATE") {
		t.Fatalf("expected newest query first, got %#v", entries[0])
	}
	for _, item := range entries {
		if strings.Contains(item.Detail, "secret") {
			t.Fatal("cross-database query leaked into slow log")
		}
	}
}

func TestReadLogTailIsBoundedAndDropsPartialLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "postgresql-main.log")
	if err := os.WriteFile(path, []byte("partially read line\nsecond full line\n"), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil { t.Fatal(err) }
	defer file.Close()
	b, err := readLogTail(file, 29)
	if err != nil { t.Fatal(err) }
	if strings.Contains(string(b), "partially read") || !strings.Contains(string(b), "second full line") {
		t.Fatalf("unexpected log tail: %q", string(b))
	}
}
