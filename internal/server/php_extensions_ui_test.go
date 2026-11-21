package server

import (
    "strings"
    "testing"

    "github.com/bau59/open-go-panel/internal/software"
)

func TestSoftwarePageDisplaysPHPVersionExtensions(t *testing.T) {
    page := softwarePage([]software.Item{
        {ID: "php", Name: "PHP + PHP-FPM", Installed: true, Version: "PHP-FPM 8.3"},
    }, software.Task{}, "8.3", []software.PHPExtension{
        {ID: "ioncube", Name: "ionCube Loader", Installed: false},
        {ID: "gd", Name: "GD", Installed: true},
        {ID: "redis", Name: "Redis", Installed: false},
    }, "")
    for _, want := range []string{
        "Manage PHP 8.3 extensions",
        "ionCube Loader",
        "Extensions are shared",
        `action="/software/php/extensions/ioncube"`,
        `action="/software/php/extensions/redis"`,
        "GD",
        "enabled",
    } {
        if !strings.Contains(page, want) {
            t.Errorf("missing PHP extensions UI %q", want)
        }
    }
    if strings.Contains(page, `action="/software/php/extensions/unknown"`) {
        t.Fatal("unexpected extension")
    }
}

func TestSoftwarePageDisablesExtensionInstallsWhileBusy(t *testing.T) {
    page := softwarePage([]software.Item{
        {ID: "php", Name: "PHP + PHP-FPM", Installed: true},
    }, software.Task{Running: true, SoftwareID: "php", Action: "install"}, "8.4",
        []software.PHPExtension{{ID: "ioncube", Name: "ionCube Loader"}}, "")
    if !strings.Contains(page, `action="/software/php/extensions/ioncube"`) ||
       !strings.Contains(page, `<button type="submit" class="secondary" disabled>`) {
        t.Fatal("PHP extension install button must be disabled during active software task")
    }
}

func TestPHPNotInstalledHasNoExtensionManagement(t *testing.T) {
    page := softwarePage([]software.Item{
        {ID: "php", Name: "PHP + PHP-FPM", Installed: false},
    }, software.Task{}, "", nil, "")
    if strings.Contains(page, "Manage PHP") {
        t.Fatal("cannot manage extensions before installing PHP-FPM")
    }
}
