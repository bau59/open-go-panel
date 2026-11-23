package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
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


// These patterns apply only to server-rendered element start tags.
// Exclusions are decided before HTMX processes the DOM, rather than changing
// attributes after boost handlers have already been registered.
var panelAnchorTag = regexp.MustCompile(`(?i)<a\b[^>]*>`)
var panelFormTag = regexp.MustCompile(`(?i)<form\b[^>]*>`)
var panelHref = regexp.MustCompile(`(?i)\bhref="([^"]*)"`)
var panelAction = regexp.MustCompile(`(?i)\baction="([^"]*)"`)

func shouldKeepNativeLink(href string) bool {
	if href == "" || strings.HasPrefix(href, "#") {
		return true
	}
	parsed, err := url.Parse(href)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || !strings.HasPrefix(parsed.Path, "/") {
		return true
	}
	path := parsed.Path
	return path == "/terminal" ||
		strings.HasPrefix(path, "/db-admin/") || path == "/db-admin" ||
		strings.HasPrefix(path, "/assets/") ||
		strings.HasPrefix(path, "/backup/users/") ||
		path == "/backup/panel.json" || path == "/backup/software.sh" || path == "/backup/docker.json" ||
		strings.HasSuffix(path, "/download") || strings.Contains(path, "/download/") ||
		strings.HasSuffix(path, "/archive")
}

func shouldKeepNativeForm(tag string) bool {
	if strings.Contains(strings.ToLower(tag), "multipart/form-data") {
		return true
	}
	match := panelAction.FindStringSubmatch(tag)
	if len(match) != 2 {
		return false
	}
	parsed, err := url.Parse(match[1])
	if err != nil {
		return true
	}
	path := parsed.Path
	return path == "/logout" || path == "/panel/close" || path == "/login" ||
		strings.HasPrefix(path, "/db-admin/") ||
		strings.HasSuffix(path, "/files")
}

// Full-page server responses are unchanged for non-HTMX clients. Native link
// and form behavior remains available when JavaScript is disabled.
func htmxPage(body string) string {
	if !strings.Contains(body, "class=\"topbar-wrap\"") {
		return body
	}
	const old = "<body>"
	const enhanced = `<body hx-boost="true" hx-target="body" hx-swap="innerHTML" hx-history="false">`
	body = strings.Replace(body, old, enhanced, 1)
	body = panelAnchorTag.ReplaceAllStringFunc(body, func(tag string) string {
		if strings.Contains(strings.ToLower(tag), "hx-boost=") {
			return tag
		}
		if strings.Contains(strings.ToLower(tag), " download") {
			return strings.TrimSuffix(tag, ">") + ` hx-boost="false">`
		}
		match := panelHref.FindStringSubmatch(tag)
		if len(match) != 2 || !shouldKeepNativeLink(match[1]) {
			return tag
		}
		return strings.TrimSuffix(tag, ">") + ` hx-boost="false">`
	})
	return panelFormTag.ReplaceAllStringFunc(body, func(tag string) string {
		if strings.Contains(tag, "hx-boost=") || !shouldKeepNativeForm(tag) {
			return tag
		}
		return strings.TrimSuffix(tag, ">") + ` hx-boost="false">`
	})
}
