package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
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

func newTestHandler() http.Handler {
	return New(Config{
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		AdminUser:     "admin",
		AdminPassword: "secret",
	})
}
