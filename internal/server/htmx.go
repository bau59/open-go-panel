package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
)

// htmxNavigation preserves existing POST handlers and their PRG semantics.
// Only boosted, non-multipart forms are adapted; ordinary requests are passed
// through unchanged. Uploads, terminal websocket, SSE and downloads bypass it.
func htmxNavigation(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost ||
			!strings.EqualFold(r.Header.Get("HX-Request"), "true") ||
			!strings.EqualFold(r.Header.Get("HX-Boosted"), "true") ||
			strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "multipart/form-data") {
			next.ServeHTTP(w, r)
			return
		}

		rec := httptest.NewRecorder()
		next.ServeHTTP(rec, r)
		resp := rec.Result()
		defer resp.Body.Close()

		// Preserve cookies, security headers and all ordinary status codes.
		for key, values := range resp.Header {
			if strings.EqualFold(key, "Content-Length") {
				continue
			}
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}

		if resp.StatusCode == http.StatusSeeOther {
			location := resp.Header.Get("Location")
			parsed, err := url.Parse(location)
			if err == nil && !parsed.IsAbs() &&
				parsed.Host == "" && strings.HasPrefix(location, "/") &&
				!strings.HasPrefix(location, "//") {
				// Authentication and panel lifecycle require a full navigation.
				if parsed.Path == "/login" || parsed.Path == "/logout" {
					w.Header().Set("HX-Redirect", location)
				} else {
					payload, err := json.Marshal(map[string]string{
						"path": location,
						"target": "body",
						"select": "body",
						"swap": "innerHTML",
					})
					if err != nil {
						http.Error(w, "encode navigation", http.StatusInternalServerError)
						return
					}
					w.Header().Set("HX-Location", string(payload))
				}
				w.Header().Del("Location")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			// A non-local or malformed redirect must not be sent into a body swap.
			w.Header().Set("HX-Refresh", "true")
			w.Header().Del("Location")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	})
}

// Full-page server responses are unchanged for non-HTMX clients.
// Enhance only authenticated panel pages, not login and standalone views.
func htmxPage(body string) string {
	if !strings.Contains(body, "class=\"topbar-wrap\"") {
		return body
	}
	const old = "<body>"
	const enhanced = `<body hx-boost="true" hx-target="body" hx-select="body" hx-swap="innerHTML" hx-history="false">`
	return strings.Replace(body, old, enhanced, 1)
}
