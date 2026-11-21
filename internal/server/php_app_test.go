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

func TestPHPVersionSelectorIsPerSite(t *testing.T) {
    sys := panelapp.App{ID: 20, Type: "php", Name: "modern", Root: "/home/web/apps/modern", User: "web"}
    legacy := panelapp.App{ID: 21, Type: "php", Name: "old", Root: "/home/web/apps/old", User: "web", Port: 9410}
    legacy.Service.PHPVersion = "7.4"
    normalPage := appPage(sys, "active", "", "")
    legacyPage := appPage(legacy, "active", "", "")
    for _, wanted := range []string{
        "action=\"/apps/20/php-version\"", "PHP 7.4 + ionCube",
        "System PHP-FPM (latest installed)",
    } {
        if !strings.Contains(normalPage, wanted) { t.Errorf("missing %s", wanted) }
    }
    for _, wanted := range []string{
        "action=\"/apps/21/php-version\"",
        "PHP 7.4 · isolated Docker",
        "127.0.0.1:9410",
        "value=\"7.4\" selected",
    } {
        if !strings.Contains(legacyPage, wanted) { t.Errorf("missing %s", wanted) }
    }
}
