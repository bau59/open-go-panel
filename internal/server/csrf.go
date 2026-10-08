package server

import (
	"bufio"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

const csrfFormField = "ogp_csrf"

// Templates in this app render their own POST forms. Injecting the token when
// the HTML response is written protects every native mutation route, including
// future forms, without requiring each template to remember a token field.
var postFormPattern = regexp.MustCompile(`(?i)<form\b[^>]*\bmethod\s*=\s*["']post["'][^>]*>`)

func csrfToken(r *http.Request) string {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return ""
	}
	// The session cookie is 256 random bits. A domain-separated digest gives
	// the form a secret token without disclosing the session bearer token.
	sum := sha256.Sum256([]byte("open-go-panel-csrf:\x00" + cookie.Value))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func injectCSRFForms(body, token string) string {
	if token == "" {
		return body
	}
	field := `<input type="hidden" name="` + csrfFormField + `" value="` + token + `">`
	return postFormPattern.ReplaceAllStringFunc(body, func(form string) string {
		return form + field
	})
}

func csrfTokenForWriter(w http.ResponseWriter) string {
	for {
		if carrier, ok := w.(*csrfResponseWriter); ok {
			return carrier.token
		}
		unwrapper, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return ""
		}
		w = unwrapper.Unwrap()
	}
}

type csrfResponseWriter struct {
	http.ResponseWriter
	token string
}

func (w *csrfResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *csrfResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Preserve the Hijacker interface for the authenticated terminal WebSocket.
func (w *csrfResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return hijacker.Hijack()
}

func csrfProtection(store *sessionStore, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authenticated := store.authenticated(r)
		if r.Method == http.MethodPost && r.URL.Path != "/login" && authenticated {
			if !sameOriginMutation(r) {
				http.Error(w, "cross-origin mutation refused", http.StatusForbidden)
				return
			}
			// Adminer is a third-party app behind a reverse proxy. Its HTML is
			// not rendered by writeHTML; retain Adminer's own form handling
			// while rejecting identifiable cross-origin browser requests.
			if !strings.HasPrefix(r.URL.Path, "/db-admin/") {
				if err := r.ParseForm(); err != nil {
					http.Error(w, "invalid form", http.StatusBadRequest)
					return
				}
				supplied := r.PostForm.Get(csrfFormField)
				expected := csrfToken(r)
				if subtle.ConstantTimeCompare([]byte(supplied), []byte(expected)) != 1 {
					http.Error(w, "invalid or missing CSRF token", http.StatusForbidden)
					return
				}
			}
		}

		token := ""
		if authenticated {
			token = csrfToken(r)
		}
		next.ServeHTTP(&csrfResponseWriter{ResponseWriter: w, token: token}, r)
	})
}

func sameOriginMutation(r *http.Request) bool {
	switch strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))) {
	case "cross-site", "same-site":
		return false
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// Kept as a compile-time assertion: the WebSocket and SSE interfaces must
// remain available when responses are wrapped.
var _ http.Flusher = (*csrfResponseWriter)(nil)
var _ http.Hijacker = (*csrfResponseWriter)(nil)
