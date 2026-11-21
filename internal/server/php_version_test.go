package server

import (
    "context"
    "strings"
    "testing"

    panelapp "github.com/bau59/open-go-panel/internal/app"
    "github.com/bau59/open-go-panel/internal/software"
)

func TestPHPVersionSelectionUI(t *testing.T) {
    app := panelapp.App{ID: 7, Name: "legacy", Type: "php"}
    page := phpVersionBlock(app)
    for _, want := range []string{"/apps/7/php-version", "PHP 7.4", "PHP-FPM pool", "Apply PHP version"} {
        if !strings.Contains(page, want) { t.Errorf("missing %q", want) }
    }
    app.Service.PHPVersion = "7.4"
    selected := phpVersionBlock(app)
    if !strings.Contains(selected, `value="7.4" selected`) &&
       !strings.Contains(selected, `value="7.4" selected disabled`) {
        t.Fatal("stored PHP version not selected")
    }
}

func TestPHPVersionSwitchRejectsNonPHPApp(t *testing.T) {
    err := switchPHPVersion(context.Background(), Config{}, panelapp.App{ID: 3, Type: "static"}, "7.4")
    if err == nil { t.Fatal("non-PHP app accepted PHP runtime switch") }
}

func TestSoftwarePagePHP74HasExtensionsAndLocalArchive(t *testing.T) {
    page := softwarePage([]software.Item{
        {ID: "php74", Name: "PHP 7.4", Installed: true, Version: "7.4"},
    }, software.Task{}, "", nil, "", []software.PHPExtension{
        {ID: "ioncube", Name: "ionCube Loader"},
        {ID: "gd", Name: "GD", Installed: true},
    })
    for _, want := range []string{
        "Manage PHP 7.4 extensions",
        `action="/software/php/7.4/extensions/ioncube"`,
        `action="/software/php/7.4/ioncube/upload"`,
        "Upload and enable Loader",
        "PHP 8.3 is unaffected",
    } {
        if !strings.Contains(page, want) { t.Errorf("missing %q", want) }
    }
}
