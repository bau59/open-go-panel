package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/bau59/open-go-panel/internal/adminer"
	"github.com/bau59/open-go-panel/internal/app"
	panelcaddy "github.com/bau59/open-go-panel/internal/caddy"
	"github.com/bau59/open-go-panel/internal/dbmanager"
	"github.com/bau59/open-go-panel/internal/linuxuser"
	"github.com/bau59/open-go-panel/internal/security"
	"github.com/bau59/open-go-panel/internal/state"
)

func TestHealthIsPublic(t *testing.T) {
	handler := newTestHandler()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d", http.StatusOK, rec.Code)
	}

	if rec.Body.String() != "ok\n" {
		t.Fatalf("unexpected body: %q", rec.Body.String())
	}
}

func TestDashboardRequiresLogin(t *testing.T) {
	handler := newTestHandler()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected %d, got %d", http.StatusSeeOther, rec.Code)
	}

	if location := rec.Header().Get("Location"); location != "/login" {
		t.Fatalf("expected redirect to /login, got %q", location)
	}
}

func TestLoginCreatesSession(t *testing.T) {
	handler := newTestHandler()

	form := url.Values{
		"username": {"admin"},
		"password": {"secret"},
	}

	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected %d, got %d", http.StatusSeeOther, rec.Code)
	}

	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected session cookie")
	}

	dashboardReq := httptest.NewRequest(http.MethodGet, "/", nil)
	dashboardReq.AddCookie(cookies[0])
	dashboardRec := httptest.NewRecorder()

	handler.ServeHTTP(dashboardRec, dashboardReq)

	if dashboardRec.Code != http.StatusOK {
		t.Fatalf("expected %d, got %d", http.StatusOK, dashboardRec.Code)
	}
}


func TestFullRouteRegistrationDoesNotPanic(t *testing.T) {
	store, err := state.Open(t.TempDir() + "/panel.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	users := linuxuser.New(logger)
	handler := New(Config{
		Logger:        logger,
		AdminUser:     "admin",
		AdminPassword: "secret",
		Users:         users,
		Apps:          app.New(store, t.TempDir()+"/apps.json", users),
		Caddy:         panelcaddy.New(store, t.TempDir()+"/caddy-sites.json"),
		Security:      security.New(),
		Databases:     dbmanager.New(store, t.TempDir()+"/databases.json"),
		Adminer:       adminer.New(),
	})

	for _, path := range []string{
		"/caddy",
		"/security",
		"/databases",
		"/db-admin/",
		"/apps/1",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("%s: expected auth redirect, got %d", path, rec.Code)
		}
		if got := rec.Header().Get("Location"); got != "/login" {
			t.Fatalf("%s: expected /login redirect, got %q", path, got)
		}
	}
}

func newTestHandler() http.Handler {
	return New(Config{
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		AdminUser:     "admin",
		AdminPassword: "secret",
	})
}
