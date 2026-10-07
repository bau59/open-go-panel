package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bau59/open-go-panel/internal/adminer"
	"github.com/bau59/open-go-panel/internal/app"
	panelcaddy "github.com/bau59/open-go-panel/internal/caddy"
	"github.com/bau59/open-go-panel/internal/linuxuser"
	"github.com/bau59/open-go-panel/internal/dbmanager"
	"github.com/bau59/open-go-panel/internal/security"
	"github.com/bau59/open-go-panel/internal/systeminfo"
	"github.com/bau59/open-go-panel/internal/state"
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
	Apps          *app.Manager
	Caddy         *panelcaddy.Manager
	Security      *security.Manager
	Databases     *dbmanager.Manager
	Adminer       *adminer.Manager
	State         *state.Store
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
		info, err := systeminfo.Read()
		if err != nil {
			cfg.Logger.Warn("read server metrics failed", "err", err)
		}

		appCount := 0
		userCount := 0
		activeApps := 0

		if cfg.Apps != nil {
			apps, _ := cfg.Apps.List()
			appCount = len(apps)
			for _, app := range apps {
				if cfg.Apps.Status(r.Context(), app.ID) == "active" {
					activeApps++
				}
			}
		}

		if cfg.Users != nil {
			users, _ := cfg.Users.List(r.Context())
			userCount = len(users)
		}

		writeHTML(w, cfg.Logger, http.StatusOK, dashboardPage(info, appCount, userCount, activeApps))
	})))

	if cfg.Users != nil {
		registerUserRoutes(mux, store, cfg)
	}
	if cfg.Apps != nil && cfg.Users != nil {
		registerAppRoutes(mux, store, cfg)
	}
	if cfg.Caddy != nil && cfg.Apps != nil {
		registerCaddyRoutes(mux, store, cfg)
	}
	if cfg.Security != nil {
		registerSecurityRoutes(mux, store, cfg)
	}
	if cfg.Databases != nil {
		registerDatabaseRoutes(mux, store, cfg)
	}
	if cfg.Adminer != nil {
		registerAdminerRoutes(mux, store, cfg)
	}
	if cfg.State != nil {
		registerActivityRoutes(mux, store, cfg)
	}

	if cfg.State != nil {
		return auditMutations(cfg.State, mux)
	}
	return mux
}


type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusRecorder) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(p)
}

func auditMutations(store *state.Store, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path == "/login" || r.URL.Path == "/logout" || strings.HasPrefix(r.URL.Path, "/db-admin/") {
			next.ServeHTTP(w, r)
			return
		}

		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)

		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		if status >= 400 {
			return
		}

		action := r.Pattern
		if action == "" {
			action = r.Method + " " + r.URL.Path
		}
		store.Audit(r.Context(), action, r.URL.Path, "remote="+r.RemoteAddr)
	})
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
		messageHTML = `<div class="alert">` + html.EscapeString(message) + `</div>`
	}

	return `<!doctype html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>Login · Open Go Panel</title>
	<style>` + baseStyles + `
		.login-screen{min-height:100vh;display:grid;place-items:center;padding:24px}
		.login-card{width:min(430px,100%);padding:30px;border:1px solid var(--border);border-radius:24px;background:linear-gradient(180deg,rgba(21,27,39,.96),rgba(14,19,28,.98));box-shadow:0 30px 80px rgba(0,0,0,.34)}
		.login-brand{display:flex;align-items:center;gap:11px;margin-bottom:26px;font-weight:780}
		.login-card h1{font-size:29px}
		.login-card .sub{margin-bottom:24px}
		.login-card label{margin-top:15px}
		.login-card .button{width:100%;margin-top:20px}
	</style>
</head>
<body>
	<div class="login-screen">
		<main class="login-card">
			<div class="login-brand"><span class="brand-mark">OG</span><span>Open Go Panel</span></div>
			<h1>Server login</h1>
			<p class="sub">Sign in to manage this server.</p>
			` + messageHTML + `
			<form method="post" action="/login" autocomplete="on">
				<label for="username">Username</label>
				<input id="username" name="username" type="text" autocomplete="username" required autofocus>
				<label for="password">Password</label>
				<input id="password" name="password" type="password" autocomplete="current-password" required>
				<button class="button" type="submit">Sign in</button>
			</form>
		</main>
	</div>
</body>
</html>`
}

func dashboardPage(info systeminfo.Info, appCount, userCount, activeApps int) string {
	return pageHead("Overview") + `<body>` + appHeader("overview") + `
	<main class="shell">
		<div class="page-head">
			<div>
				<p class="eyebrow">Server</p>
				<h1>` + html.EscapeString(info.Hostname) + `</h1>
				<p class="sub">` + html.EscapeString(info.OS) + ` · kernel ` + html.EscapeString(info.Kernel) + `</p>
			</div>
		</div>

		<section class="metrics-grid">
			<div class="metric"><span>CPU</span><strong>` + fmt.Sprintf("%d cores", info.CPUs) + `</strong><small>load ` + html.EscapeString(info.Load1) + ` / ` + html.EscapeString(info.Load5) + ` / ` + html.EscapeString(info.Load15) + `</small></div>
			<div class="metric"><span>Memory</span><strong>` + formatBytes(info.MemoryUsed) + ` / ` + formatBytes(info.MemoryTotal) + `</strong><div class="meter"><i style="width:` + fmt.Sprintf("%.1f", info.MemoryPercent) + `%"></i></div></div>
			<div class="metric"><span>Disk /</span><strong>` + formatBytes(info.DiskUsed) + ` / ` + formatBytes(info.DiskTotal) + `</strong><div class="meter"><i style="width:` + fmt.Sprintf("%.1f", info.DiskPercent) + `%"></i></div></div>
			<div class="metric"><span>Uptime</span><strong>` + formatDuration(info.Uptime) + `</strong><small>` + fmt.Sprintf("%d apps · %d active · %d users", appCount, activeApps, userCount) + `</small></div>
		</section>

		<section class="grid cards" style="margin-top:18px">
			<a class="card" href="/apps">
				<div class="card-icon">APP</div>
				<h2>Applications</h2>
				<p>Create apps, manage systemd, resources, ports and logs.</p>
			</a>
			<a class="card" href="/users">
				<div class="card-icon">USR</div>
				<h2>Users</h2>
				<p>Manage Linux accounts, passwords and SSH access.</p>
			</a>
			<a class="card" href="/databases"><div class="card-icon">DB</div><h2>Databases</h2><p>Install MySQL or PostgreSQL, create databases and manage credentials.</p></a>
			<article class="card"><div class="card-icon">TTY</div><h2>Terminal</h2><p>Browser terminal access will be added next.</p></article>
		</section>
		<div class="statline"><i></i>Open Go Panel is running</div>
	</main>
</body>
</html>`
}

func formatBytes(v uint64) string {
	const unit = 1024
	if v < unit {
		return fmt.Sprintf("%d B", v)
	}
	div, exp := uint64(unit), 0
	for n := v / unit; n >= unit && exp < 5; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(v)/float64(div), "KMGTPE"[exp])
}

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	if days > 0 {
		return fmt.Sprintf("%dd %dh", days, hours)
	}
	return fmt.Sprintf("%dh %dm", hours, int(d.Minutes())%60)
}
