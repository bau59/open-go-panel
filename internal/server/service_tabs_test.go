package server

import (
    "strings"
    "testing"

    panelapp "github.com/bau59/open-go-panel/internal/app"
)

func TestAppSectionTabsPreserveFormsAndAnchors(t *testing.T) {
    page:=appPage(panelapp.App{ID:42, Name:"demo",Type:"go",User:"demo",Root:"/home/demo/apps/demo"}, "active", "", "",
        `<section id="chatgpt-access" data-panel-tab="chatgpt"><form method="post" action="/apps/42/chatgpt/network"><input type="checkbox" name="mysql" value="1"></form></section>`)
    for _,part:=range []string{
        `data-panel-key="app-42"`,
        `data-panel-tabs="overview,deploy,databases,cron,chatgpt,settings"`,
        `href="#service-settings"`,
        `id="service-settings"`,
        `action="/apps/42/service"`,
        `action="/apps/42/chatgpt/network"`,
        `action="/apps/42/delete"`,
    } { if !strings.Contains(page,part){t.Errorf("missing %s",part)} }
    if strings.Contains(page,`data-panel-tab="chatgpt" hidden`){t.Fatal("forms must remain visible without JavaScript")}
}

func TestSectionTabStylesDoNotResizeNativeCheckboxes(t *testing.T) {
    for _,part:=range []string{
        `input:is([type="checkbox"],[type="radio"]):not(.switch input)`,
        `[data-panel-tab][hidden]{display:none!important}`,
        `.mcp-copy-row`,
        `.service-tabs button.active`,
    } {if !strings.Contains(baseStyles,part){t.Errorf("missing %s",part)}}
}
