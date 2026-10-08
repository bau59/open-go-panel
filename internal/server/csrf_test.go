package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func loginAndGetCSRF(t *testing.T, handler http.Handler) (*http.Cookie, string) {
	t.Helper()
	form := url.Values{"username": {"admin"}, "password": {"secret"}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login status = %d", rec.Code)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("login produced no session cookie")
	}
	page := httptest.NewRequest(http.MethodGet, "/", nil)
	page.AddCookie(cookies[0])
	pageRec := httptest.NewRecorder()
	handler.ServeHTTP(pageRec, page)
	if pageRec.Code != http.StatusOK {
		t.Fatalf("dashboard status = %d", pageRec.Code)
	}
	match := regexp.MustCompile(`name="ogp_csrf" value="([^"]+)"`).FindStringSubmatch(pageRec.Body.String())
	if len(match) != 2 {
		t.Fatal("dashboard POST form missing CSRF token")
	}
	if strings.Contains(pageRec.Body.String(), cookies[0].Value) {
		t.Fatal("dashboard exposed raw bearer session token")
	}
	return cookies[0], match[1]
}

func TestCSRFMiddlewareProtectsMutations(t *testing.T) {
	handler := New(Config{
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		AdminUser:     "admin",
		AdminPassword: "secret",
	})
	cookie, token := loginAndGetCSRF(t, handler)

	for _, tc := range []struct {
		name   string
		values url.Values
		origin string
		site   string
		host   string
		want   int
	}{
		{name: "missing token", values: url.Values{}, want: http.StatusForbidden},
		{name: "wrong token", values: url.Values{csrfFormField: {"not-a-token"}}, want: http.StatusForbidden},
		{name: "cross origin missing token", values: url.Values{}, origin: "https://external.example", site: "cross-site", want: http.StatusForbidden},
		{name: "cross origin wrong token", values: url.Values{csrfFormField: {"invalid"}}, origin: "https://external.example", site: "cross-site", want: http.StatusForbidden},
		{name: "reverse proxy rewritten host", values: url.Values{csrfFormField: {token}}, origin: "https://panel.example.com", host: "127.0.0.1:8443", site: "same-origin", want: http.StatusSeeOther},
		{name: "browser origin differs from proxy host", values: url.Values{csrfFormField: {token}}, origin: "https://panel.example.com", host: "internal.panel.local", want: http.StatusSeeOther},
		{name: "valid token", values: url.Values{csrfFormField: {token}}, want: http.StatusSeeOther},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/logout", strings.NewReader(tc.values.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.site != "" {
				req.Header.Set("Sec-Fetch-Site", tc.site)
			}
			if tc.host != "" {
				req.Host = tc.host
			}
			req.AddCookie(cookie)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("POST logout status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestCSRFFormInjection(t *testing.T) {
	const page = `<form method="post" action="/users"><button>Save</button></form>
<form action="/apps" method='POST'><button>Deploy</button></form>
<form method="get" action="/search"></form>`
	secured := injectCSRFForms(page, "token123")
	if strings.Count(secured, `name="ogp_csrf" value="token123"`) != 2 {
		t.Fatalf("injected tokens = %d, want 2", strings.Count(secured, `name="ogp_csrf" value="token123"`))
	}
	if !strings.Contains(secured, `<form method="get" action="/search"></form>`) {
		t.Fatal("GET form was modified")
	}
}

func TestCSRFSameOriginPolicy(t *testing.T) {
	for _, tc := range []struct {
		name   string
		origin string
		site   string
		want   bool
	}{
		{name: "same origin", origin: "https://panel.example.com", site: "same-origin", want: true},
		{name: "cross origin", origin: "https://another.example.com", want: false},
		{name: "same-site sibling origin", site: "same-site", want: false},
		{name: "cross site", site: "cross-site", want: false},
		{name: "CLI no browser headers", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "https://panel.example.com/logout", nil)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.site != "" {
				req.Header.Set("Sec-Fetch-Site", tc.site)
			}
			if got := sameOriginMutation(req); got != tc.want {
				t.Fatalf("sameOriginMutation = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAdminerPostStillChecksOrigin(t *testing.T) {
	handler := New(Config{
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		AdminUser:     "admin",
		AdminPassword: "secret",
	})
	cookie, token := loginAndGetCSRF(t, handler)

	for _, tc := range []struct {
		name   string
		origin string
		site   string
		want   int
	}{
		{name: "cross origin", origin: "https://evil.example", want: http.StatusForbidden},
		{name: "cross site", site: "cross-site", want: http.StatusForbidden},
		{name: "same site sibling", site: "same-site", want: http.StatusForbidden},
		{name: "same origin", origin: "http://example.com", site: "same-origin", want: http.StatusMethodNotAllowed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			form := url.Values{csrfFormField: {token}}
			req := httptest.NewRequest(http.MethodPost, "http://example.com/db-admin/login", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Origin", tc.origin)
			if tc.site != "" {
				req.Header.Set("Sec-Fetch-Site", tc.site)
			}
			req.AddCookie(cookie)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("Adminer request status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}
