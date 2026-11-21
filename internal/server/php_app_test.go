package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	panelapp "github.com/bau59/open-go-panel/internal/app"
)

func TestPHPDocumentRoot(t *testing.T) {
	root := t.TempDir()
	app := panelapp.App{Root: root, Type: "php"}
	if got, err := phpDocumentRoot(app, ""); err != nil || got != root {
		t.Fatalf("auto root without public/: %q, %v", got, err)
	}
	public := filepath.Join(root, "public")
	if err := os.Mkdir(public, 0755); err != nil { t.Fatal(err) }
	if got, err := phpDocumentRoot(app, ""); err != nil || got != public {
		t.Fatalf("auto root with public/: %q, %v", got, err)
	}
	if _, err := phpDocumentRoot(app, "../etc"); err == nil {
		t.Fatal("document root may only be app root or public/")
	}
}

func TestPHPLogsLink(t *testing.T) {
	app := panelapp.App{ID: 42, Type: "php", Name: "example", User: "test", Root: "/home/test/example"}
	for _, page := range []string{appsPage([]panelapp.App{app}, nil, ""), appPage(app, "active", "", "")} {
		if !strings.Contains(page, `/apps/42/php-logs`) { t.Fatal("PHP error log link missing") }
	}
}

func TestPHPAppOption(t *testing.T) {
	page := appsPage(nil, nil, "")
	if !strings.Contains(page, `value="php"`) {
		t.Fatal("PHP app type missing from the creation form")
	}
}
