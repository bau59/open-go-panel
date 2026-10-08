package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestClosePanelRequiresSessionAndCSRF(t *testing.T) {
	calls := 0
	handler := New(Config{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		AdminUser: "admin",
		AdminPassword: "secret",
		ClosePanel: func(context.Context) error {
			calls++
			return nil
		},
	})
	cookie, token := loginAndGetCSRF(t, handler)

	newRequest := func(value string, authenticated bool) *http.Request {
		form := url.Values{}
		if value != "" {
			form.Set(csrfFormField, value)
		}
		req := httptest.NewRequest(http.MethodPost, "/panel/close", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if authenticated {
			req.AddCookie(cookie)
		}
		return req
	}
	for _, tc := range []struct {
		name string
		request *http.Request
		want int
	}{
		{name:"unauthenticated", request:newRequest(token, false), want:http.StatusSeeOther},
		{name:"missing token", request:newRequest("", true), want:http.StatusForbidden},
		{name:"valid token", request:newRequest(token, true), want:http.StatusOK},
	} {
		t.Run(tc.name,func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, tc.request)
			if rec.Code != tc.want {
				t.Fatalf("close panel response = %d, want %d",rec.Code,tc.want)
			}
			if tc.want==http.StatusOK {
				if !strings.Contains(rec.Body.String(), "sudo systemctl start open-go-panel.service") {
					t.Fatal("missing SSH recovery instructions")
				}
			}
		})
	}
	if calls != 1 {
		t.Fatalf("scheduled shutdown %d times, want 1",calls)
	}
}
