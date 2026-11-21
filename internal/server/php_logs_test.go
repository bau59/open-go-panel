package server

import (
    "strings"
    "testing"

    panelapp "github.com/bau59/open-go-panel/internal/app"
)

func TestPHPErrorLogsPageLegacyPoolOffersEnable(t *testing.T) {
    page := phpErrorLogsPage(panelapp.App{ID: 42, Name: "test PHP", Type: "php"}, false, "", "")
    for _, want := range []string{
        "Setup needed", "Enable PHP error log",
        "action=\"/apps/42/php-logs/enable\"",
        "href=\"/apps/42/caddy-logs\"",
    } {
        if !strings.Contains(page, want) { t.Errorf("missing %q", want) }
    }
}

func TestPHPErrorLogsPageEscapesLogContent(t *testing.T) {
    page := phpErrorLogsPage(panelapp.App{ID: 17, Name: "<app>", Type: "php"}, true,
        "PHP Fatal error: <script>alert(\"x\")</script>", "")
    if strings.Contains(page, "<script>alert") || strings.Contains(page, "<app>") {
        t.Fatal("unescaped app name or PHP log content")
    }
    for _, want := range []string{"PHP errors", "Refresh", "Enabled", "&lt;script&gt;"} {
        if !strings.Contains(page, want) { t.Errorf("missing %q", want) }
    }
}

func TestPHPErrorLogsPageEscapesErrorMessage(t *testing.T) {
    page := phpErrorLogsPage(panelapp.App{ID: 5, Name: "demo", Type: "php"}, false, "", "<script>alert(1)</script>")
    if strings.Contains(page, "<script>alert(1)") {
        t.Fatal("unescaped error message")
    }
}
