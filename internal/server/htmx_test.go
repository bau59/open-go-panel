package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPanelAssetsAreEmbeddedAndExternal(t *testing.T) {
	if len(baseStyles) < 40000 || !strings.Contains(baseStyles, "--bg:#f5f7fc") {
		t.Fatal("external stylesheet missing baseline or light theme")
	}
	if !strings.Contains(htmxScript, "var htmx=") || !strings.Contains(panelScript, "htmx:afterSwap") {
		t.Fatal("embedded HTMX runtime or page lifecycle script missing")
	}
	head := pageHead("Overview")
	for _, link := range []string{
		"href=\"/assets/panel.css\"",
		"src=\"/assets/htmx.min.js\"",
		"src=\"/assets/panel.js\"",
		"\"historyCacheSize\":0",
	} {
		if !strings.Contains(head, link) {
			t.Fatalf("missing external asset: %q", link)
		}
	}
	if strings.Contains(head, "<style>") || strings.Contains(head, baseStyles) {
		t.Fatal("global stylesheet must not be repeated in every HTML response")
	}
	mux := http.NewServeMux()
	registerUIAssets(mux)
	for _, tc := range []struct{ path, mime string }{
		{"/assets/panel.css", "text/css"},
		{"/assets/htmx.min.js", "text/javascript"},
		{"/assets/panel.js", "text/javascript"},
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", tc.path, nil))
		if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Type"), tc.mime) {
			t.Errorf("asset %s: HTTP %d, MIME %q", tc.path, rec.Code, rec.Header().Get("Content-Type"))
		}
		if rec.Body.Len() < 100 {
			t.Errorf("asset %s empty", tc.path)
		}
	}
}

func TestHTMXPageDoesNotBoostLogin(t *testing.T) {
	full := pageHead("Overview") + "<body>" + appHeader("overview") + "<main>content</main></body></html>"
	enhanced := htmxPage(full)
	for _, attr := range []string{"hx-boost=\"true\"", "hx-target=\"body\"", "hx-history=\"false\""} {
		if !strings.Contains(enhanced, attr) {
			t.Errorf("missing panel navigation attribute %s", attr)
		}
	}
	if strings.Contains(htmxPage(loginPage("")), "hx-boost=\"true\"") {
		t.Fatal("login form must not be boosted")
	}
}

func TestHTMXPreservesPostRedirectGet(t *testing.T) {
	handler := htmxNavigation(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/apps/12#cron-jobs", http.StatusSeeOther)
	}))
	boosted := httptest.NewRequest("POST", "/apps/12/cron", strings.NewReader("enabled=1"))
	boosted.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	boosted.Header.Set("HX-Request", "true")
	boosted.Header.Set("HX-Boosted", "true")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, boosted)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("boosted redirect HTTP=%d, want 204", rec.Code)
	}
	var location struct{ Path, Target, Select, Swap string }
	if err := json.Unmarshal([]byte(rec.Header().Get("HX-Location")), &location); err != nil {
		t.Fatal(err)
	}
	if location.Path != "/apps/12#cron-jobs" || location.Target != "body" ||
		location.Select != "body" || location.Swap != "innerHTML" {
		t.Fatalf("incorrect navigation: %+v", location)
	}
	native := httptest.NewRequest("POST", "/apps/12/cron", nil)
	nativeRec := httptest.NewRecorder()
	handler.ServeHTTP(nativeRec, native)
	if nativeRec.Code != 303 || nativeRec.Header().Get("Location") != "/apps/12#cron-jobs" {
		t.Fatal("native forms must retain original redirects")
	}
}

func TestHTMXDoesNotChangeUploadErrors(t *testing.T) {
	handler := htmxNavigation(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "test error", http.StatusBadRequest)
	}))
	for _, mime := range []string{"application/x-www-form-urlencoded", "multipart/form-data; boundary=abc"} {
		req := httptest.NewRequest("POST", "/upload", nil)
		req.Header.Set("HX-Request", "true")
		req.Header.Set("HX-Boosted", "true")
		req.Header.Set("Content-Type", mime)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), "test error") {
			t.Fatalf("form error changed for %s", mime)
		}
	}
}
