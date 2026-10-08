package dbmanager

import (
	"strings"
	"testing"
)

func TestParseRedisSlowQueries(t *testing.T) {
	raw := []byte(`[[1,1760000000,125000,["GET","cache:key"],"127.0.0.1:54232","web"]]`)
	items, err := parseRedisSlowQueries(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].DurationUS != 125000 || items[0].Command != "GET cache:key" {
		t.Fatalf("unexpected Redis SLOWLOG: %+v", items)
	}
}

func TestParseRedisSlowQueriesRejectsMalformedResult(t *testing.T) {
	_, err := parseRedisSlowQueries([]byte(`[["not a normal slowlog"]]`))
	if err == nil || !strings.Contains(err.Error(), "format") {
		t.Fatalf("expected invalid format error; got %v", err)
	}
}
