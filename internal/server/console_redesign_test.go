package server

import (
 "strings"
 "testing"
 "github.com/bau59/open-go-panel/internal/systeminfo"
)

func TestNavigationGroupsKeepEveryPage(t *testing.T){
 page:=appHeader("performance")
 for _,path:=range []string{
  "/","/apps","/users","/databases","/caddy","/performance",
  "/log-retention","/security","/software","/docker","/terminal","/activity",
 }{
  if !strings.Contains(page,`href="`+path+`"`){t.Errorf("navigation missing %q",path)}
 }
 for _,label:=range []string{"Infrastructure","Observability"}{
  if !strings.Contains(page,`<summary>`+label+`</summary>`){t.Errorf("missing menu %s",label)}
 }
 if !strings.Contains(page,`<details class="current" open><summary>Observability`){
  t.Fatal("current nested navigation is not highlighted")
 }
}

func TestDashboardDockerMemoryUnits(t *testing.T){
 for _,item:=range []struct{raw string;want uint64}{
  {"1.5MiB / 512MiB",1572864},
  {"2GiB / 4GiB",2147483648},
  {"512KiB / 4GiB",524288},
  {"12B / 3GiB",12},
  {"bogus",0},
 }{
  if actual:=parseDockerUsedBytes(item.raw);actual!=item.want{
   t.Errorf("%q: got %d, want %d",item.raw,actual,item.want)
  }
 }
}

func TestDashboardLoadsResourcesAfterInitialPage(t *testing.T){
 page:=dashboardPage(systeminfo.Info{},0,0,0)
 for _,value:=range []string{
  "Runtime resources","/dashboard/resources",
  "Apps running","Docker containers",
  "Process memory","Container resources",
 }{
  if !strings.Contains(page,value){t.Errorf("missing dashboard element %s",value)}
 }
}

func TestGlassNavigationMarkup(t *testing.T) {
	page := appHeader("caddy")
	for _, part := range []string{
		`<nav class="nav" aria-label="Main navigation">`,
		`<details class="current" open><summary>Infrastructure</summary>`,
		`aria-current="page" href="/caddy"`,
		`<span class="nav-icon" aria-hidden="true"><svg viewBox="0 0 24 24">`,
		`<span class="brand-copy">`,
		`action="/logout"`,
		`action="/panel/close"`,
	} {
		if !strings.Contains(page, part) {
			t.Errorf("navigation missing %q", part)
		}
	}
	if strings.Contains(page, `<details class="current" open><summary>Observability`) {
		t.Fatal("unrelated navigation group must remain collapsed")
	}
}

func TestGlassThemeIncludesMobileLayout(t *testing.T) {
	for _, part := range []string{
		"color-scheme:dark",
		".topbar-wrap{position:fixed",
		"@media(max-width:1050px)",
		".shell.terminal-shell",
		"@media(prefers-reduced-motion:reduce)",
	} {
		if !strings.Contains(baseStyles, part) {
			t.Errorf("theme missing %q", part)
		}
	}
}
