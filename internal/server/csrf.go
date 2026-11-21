package server

import (
	"bufio"
	"crypto/sha256"
	"crypto/subtle"
    "errors"
	"encoding/base64"
	"net"
	"net/http"
    "time"

    "github.com/bau59/open-go-panel/internal/uploadlimits"
	"net/url"
	"regexp"
	"strings"
)

const csrfFormField = "ogp_csrf"
const fileUploadBodyLimit = uploadlimits.MultipartBodyBytes
const uploadTimeout = time.Hour

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

// The generic file manager permits large uploads, but an ionCube package
// must stay small even though CSRF middleware parses multipart data first.
func multipartLimitForPath(path string) int64 {
    if strings.HasPrefix(path, "/software/php/") && strings.HasSuffix(path, "/ioncube/upload") {
        return (64 << 20) + (1 << 20) // archive + multipart overhead
    }
    return fileUploadBodyLimit
}

func csrfProtection(store *sessionStore, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authenticated := store.authenticated(r)
		if r.Method == http.MethodPost && r.URL.Path != "/login" && authenticated {
			// Adminer renders its own forms, without our session-bound CSRF
			// token. Retain the Origin/Fetch Metadata gate for Adminer.
			if strings.HasPrefix(r.URL.Path, "/db-admin/") {
				if !sameOriginMutation(r) {
					http.Error(w, "cross-origin mutation refused", http.StatusForbidden)
					return
				}
			} else {
				// Native forms carry a CSRF token tied to the authenticated
				// session. Verifying that token is sufficient even when a
				// reverse proxy rewrites Host or Origin differs from the
				// upstream address. Strict Host/Origin equality incorrectly
				// blocked legitimate SSH tunnels and reverse proxies.
				var parseErr error
				if strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "multipart/form-data;") {
					// The HTTP server uses short deadlines for ordinary requests.
					// Override them only for authenticated multipart uploads so
					// 1 GiB uploads can complete over slower connections.
					deadline := time.Now().Add(uploadTimeout)
					controller := http.NewResponseController(w)
					if err := controller.SetReadDeadline(deadline); err != nil && !errors.Is(err, http.ErrNotSupported) {
						http.Error(w, "cannot set upload read deadline", http.StatusServiceUnavailable)
						return
					}
					if err := controller.SetWriteDeadline(deadline); err != nil && !errors.Is(err, http.ErrNotSupported) {
						http.Error(w, "cannot set upload write deadline", http.StatusServiceUnavailable)
						return
					}
					// ParseMultipartForm streams large parts to disk. Cap the
					// entire body and clean up the temporary parts after use.
					r.Body = http.MaxBytesReader(w, r.Body, multipartLimitForPath(r.URL.Path))
					parseErr = r.ParseMultipartForm(8 << 20)
					if r.MultipartForm != nil { defer r.MultipartForm.RemoveAll() }
				} else {
					parseErr = r.ParseForm()
				}
				if parseErr != nil {
					http.Error(w, "invalid form or upload too large (max 1 GiB per file)", http.StatusBadRequest)
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
