package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/bau59/open-go-panel/internal/linuxuser"
)

const (
	sessionCookieName = "ogp_session"
	sessionTTL        = 24 * time.Hour
)

type Config struct {
	Logger        *slog.Logger
	AdminUser     string
	AdminPassword string
	Users         *linuxuser.Manager
}

type sessionStore struct {
	mu       sync.Mutex
	sessions map[string]time.Time
}

func New(cfg Config) http.Handler {
	store := &sessionStore{
		sessions: make(map[string]time.Time),
	}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)

		if _, err := w.Write([]byte("ok\n")); err != nil {
			cfg.Logger.Error("write health response failed", "err", err)
		}
	})

	mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
		if store.authenticated(r) {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}

		writeHTML(w, cfg.Logger, http.StatusOK, loginPage(""))
	})

	mux.HandleFunc("POST /login", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			writeHTML(w, cfg.Logger, http.StatusBadRequest, loginPage("Invalid request"))
			return
		}

		userOK := secureEqual(r.FormValue("username"), cfg.AdminUser)
		passwordOK := secureEqual(r.FormValue("password"), cfg.AdminPassword)

		if !userOK || !passwordOK {
			cfg.Logger.Warn("login failed", "remote_addr", r.RemoteAddr)
			writeHTML(w, cfg.Logger, http.StatusUnauthorized, loginPage("Invalid username or password"))
			return
		}

		token, err := store.create()
		if err != nil {
			cfg.Logger.Error("create session failed", "err", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}

		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookieName,
			Value:    token,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
			MaxAge:   int(sessionTTL.Seconds()),
		})

		cfg.Logger.Info("login successful", "remote_addr", r.RemoteAddr)
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})

	mux.HandleFunc("POST /logout", func(w http.ResponseWriter, r *http.Request) {
		store.destroy(r)

		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookieName,
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
			MaxAge:   -1,
		})

		http.Redirect(w, r, "/login", http.StatusSeeOther)
	})

	mux.Handle("GET /", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeHTML(w, cfg.Logger, http.StatusOK, dashboardPage())
	})))

	if cfg.Users != nil {
		registerUserRoutes(mux, store, cfg)
	}

	return mux
}

func requireAuth(store *sessionStore, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !store.authenticated(r) {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (s *sessionStore) create() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}

	token := base64.RawURLEncoding.EncodeToString(buf)

	s.mu.Lock()
	s.sessions[token] = time.Now().Add(sessionTTL)
	s.mu.Unlock()

	return token, nil
}

func (s *sessionStore) authenticated(r *http.Request) bool {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	expiresAt, ok := s.sessions[cookie.Value]
	if !ok {
		return false
	}

	if time.Now().After(expiresAt) {
		delete(s.sessions, cookie.Value)
		return false
	}

	return true
}

func (s *sessionStore) destroy(r *http.Request) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return
	}

	s.mu.Lock()
	delete(s.sessions, cookie.Value)
	s.mu.Unlock()
}

func secureEqual(got, want string) bool {
	if len(got) != len(want) {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func writeHTML(w http.ResponseWriter, logger *slog.Logger, status int, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(status)

	if _, err := fmt.Fprint(w, body); err != nil {
		logger.Error("write response failed", "err", err)
	}
}

func loginPage(message string) string {
	messageHTML := ""
	if message != "" {
		messageHTML = `<div class="alert">` + message + `</div>`
	}

	return `<!doctype html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>Login · Open Go Panel</title>
	<style>
		*{box-sizing:border-box}
		body{margin:0;min-height:100vh;display:grid;place-items:center;background:#0b1020;color:#e5e7eb;font-family:system-ui,-apple-system,sans-serif}
		.card{width:min(420px,calc(100% - 32px));padding:32px;border:1px solid #27324a;border-radius:18px;background:#111827;box-shadow:0 24px 80px rgba(0,0,0,.35)}
		h1{margin:0 0 8px;font-size:28px}
		p{margin:0 0 28px;color:#94a3b8}
		label{display:block;margin:16px 0 7px;font-size:14px;color:#cbd5e1}
		input{width:100%;height:44px;padding:0 12px;border:1px solid #334155;border-radius:10px;background:#0f172a;color:#fff;outline:none}
		input:focus{border-color:#6366f1}
		button{width:100%;height:44px;margin-top:22px;border:0;border-radius:10px;background:#6366f1;color:white;font-weight:700;cursor:pointer}
		.alert{margin:0 0 18px;padding:10px 12px;border:1px solid #7f1d1d;border-radius:10px;background:#450a0a;color:#fecaca;font-size:14px}
		.brand{display:inline-flex;margin-bottom:22px;padding:6px 10px;border-radius:999px;background:#1e293b;color:#cbd5e1;font-size:12px;font-weight:700}
	</style>
</head>
<body>
	<main class="card">
		<div class="brand">OPEN GO PANEL</div>
		<h1>Server login</h1>
		<p>Sign in to manage this server.</p>
		` + messageHTML + `
		<form method="post" action="/login" autocomplete="on">
			<label for="username">Username</label>
			<input id="username" name="username" type="text" autocomplete="username" required autofocus>
			<label for="password">Password</label>
			<input id="password" name="password" type="password" autocomplete="current-password" required>
			<button type="submit">Sign in</button>
		</form>
	</main>
</body>
</html>`
}

func dashboardPage() string {
	return `<!doctype html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>Open Go Panel</title>
	<style>
		*{box-sizing:border-box}
		body{margin:0;background:#0b1020;color:#e5e7eb;font-family:system-ui,-apple-system,sans-serif}
		header{height:64px;display:flex;align-items:center;justify-content:space-between;padding:0 28px;border-bottom:1px solid #1e293b;background:#0f172a}
		.brand{font-weight:800;letter-spacing:.02em}
		button{padding:9px 14px;border:1px solid #334155;border-radius:9px;background:#111827;color:#cbd5e1;cursor:pointer}
		main{max-width:1180px;margin:0 auto;padding:42px 28px}
		h1{margin:0;font-size:32px}
		.sub{margin:8px 0 30px;color:#94a3b8}
		.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(220px,1fr));gap:16px}
		.card{display:block;min-height:140px;padding:22px;border:1px solid #253047;border-radius:16px;background:#111827;color:inherit;text-decoration:none}
		.card h2{margin:0 0 10px;font-size:16px}
		.card p{margin:0;color:#64748b;font-size:14px;line-height:1.5}
		.status{display:inline-flex;align-items:center;gap:8px;margin-top:20px;color:#86efac;font-size:13px;font-weight:700}
		.dot{width:8px;height:8px;border-radius:50%;background:#22c55e}
	</style>
</head>
<body>
	<header>
		<div class="brand">Open Go Panel</div>
		<form method="post" action="/logout"><button type="submit">Logout</button></form>
	</header>
	<main>
		<h1>Server overview</h1>
		<p class="sub">The panel is installed and running.</p>
		<section class="grid">
			<article class="card"><h2>Sites</h2><p>Site and Caddy management will be added next.</p></article>
			<a class="card" href="/users"><h2>Users</h2><p>Create Linux users, manage SSH keys, passwords and access.</p></a>
			<article class="card"><h2>Databases</h2><p>Database installation and management will be added next.</p></article>
			<article class="card"><h2>Terminal</h2><p>Web terminal access will be added next.</p></article>
		</section>
		<div class="status"><span class="dot"></span>Open Go Panel is running</div>
	</main>
</body>
</html>`
}
