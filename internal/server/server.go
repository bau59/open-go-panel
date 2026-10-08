package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"html"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bau59/open-go-panel/internal/adminer"
	"github.com/bau59/open-go-panel/internal/app"
	panelcaddy "github.com/bau59/open-go-panel/internal/caddy"
	"github.com/bau59/open-go-panel/internal/linuxuser"
	"github.com/bau59/open-go-panel/internal/dbmanager"
	paneldocker "github.com/bau59/open-go-panel/internal/docker"
	"github.com/bau59/open-go-panel/internal/security"
	"github.com/bau59/open-go-panel/internal/software"
	"github.com/bau59/open-go-panel/internal/systeminfo"
	"github.com/bau59/open-go-panel/internal/state"
)

const (
	sessionCookieName  = "ogp_session"
	sessionTTL         = 24 * time.Hour
	loginWindow        = 5 * time.Minute
	loginBlockDuration = 15 * time.Minute
	maxLoginFailures   = 5
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
	Software      *software.Manager
	Docker        *paneldocker.Manager
	State         *state.Store
	ClosePanel    func(context.Context) error
}

type sessionStore struct {
	mu       sync.Mutex
	sessions map[string]time.Time
}

type loginAttempt struct {
	Failures     int
	WindowStart  time.Time
	BlockedUntil time.Time
}

type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string]loginAttempt
}

func New(cfg Config) http.Handler {
	store := &sessionStore{
		sessions: make(map[string]time.Time),
	}
	limiter := &loginLimiter{attempts: make(map[string]loginAttempt)}

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
		loginKey := loginRemoteKey(r)
		if allowed, retryAfter := limiter.allowed(loginKey); !allowed {
			w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Round(time.Second).Seconds())))
			cfg.Logger.Warn("login rate limited", "remote_addr", r.RemoteAddr)
			writeHTML(w, cfg.Logger, http.StatusTooManyRequests, loginPage("Too many failed sign-in attempts. Try again later."))
			return
		}

		if err := r.ParseForm(); err != nil {
			writeHTML(w, cfg.Logger, http.StatusBadRequest, loginPage("Invalid request"))
			return
		}

		userOK := secureEqual(r.FormValue("username"), cfg.AdminUser)
		passwordOK := secureEqual(r.FormValue("password"), cfg.AdminPassword)

		if !userOK || !passwordOK {
			limiter.failure(loginKey)
			cfg.Logger.Warn("login failed", "remote_addr", r.RemoteAddr)
			writeHTML(w, cfg.Logger, http.StatusUnauthorized, loginPage("Invalid username or password"))
			return
		}

		limiter.success(loginKey)
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

	if cfg.ClosePanel != nil {
		mux.Handle("POST /panel/close", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := cfg.ClosePanel(r.Context()); err != nil {
				http.Error(w, "could not schedule panel shutdown: "+err.Error(), http.StatusInternalServerError)
				return
			}
			writeHTML(w, cfg.Logger, http.StatusOK, pageHead("Panel closing")+ `<body>
				<main class="shell" style="max-width:700px;padding-top:64px">
					<section class="panel panel-pad">
						<h1>Panel is closing</h1>
						<p>The panel service will stop and its listening port will close. Managed applications, databases and Docker containers continue running.</p>
						<p>To open the panel again, connect to the server through SSH and run:</p>
						<pre><code>sudo systemctl start open-go-panel.service</code></pre>
						<p>If you use an SSH tunnel: <code>ssh -L 8443:127.0.0.1:8443 user@server</code></p>
					</section>
				</main></body></html>`)
		})))
	}

	registerDashboardResources(mux,store,cfg)
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
			// The detailed resource inventory is fetched asynchronously so the
			// dashboard itself never waits for one systemctl subprocess per app.
			activeApps = -1
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
		registerTerminalRoutes(mux, store, cfg)
	}
	if cfg.Caddy != nil && cfg.Apps != nil {
		registerCaddyRoutes(mux, store, cfg)
		registerPerformanceRoutes(mux, store, cfg)
		registerLogRetentionRoutes(mux, store, cfg)
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
	if cfg.Software != nil {
		registerSoftwareRoutes(mux, store, cfg)
	}
	if cfg.Docker != nil {
		registerDockerRoutes(mux, store, cfg)
	}
	if cfg.State != nil {
		registerActivityRoutes(mux, store, cfg)
	}

	if cfg.State != nil {
		return csrfProtection(store, auditMutations(cfg.State, mux))
	}
	return csrfProtection(store, mux)
}


type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

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
		// Optional audit trail can be disabled explicitly from Log retention.
		// Authentication and service errors continue through structured logs.
		if value,exists,err:=store.Setting("logs.audit_enabled");err==nil && exists && value=="0" {
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

func loginRemoteKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func (l *loginLimiter) allowed(key string) (bool, time.Duration) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	attempt, ok := l.attempts[key]
	if !ok {
		return true, 0
	}
	if !attempt.BlockedUntil.IsZero() {
		if now.Before(attempt.BlockedUntil) {
			return false, time.Until(attempt.BlockedUntil)
		}
		delete(l.attempts, key)
		return true, 0
	}
	if now.Sub(attempt.WindowStart) > loginWindow {
		delete(l.attempts, key)
	}
	return true, 0
}

func (l *loginLimiter) failure(key string) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	attempt := l.attempts[key]
	if attempt.WindowStart.IsZero() || now.Sub(attempt.WindowStart) > loginWindow {
		attempt = loginAttempt{WindowStart: now}
	}
	attempt.Failures++
	if attempt.Failures >= maxLoginFailures {
		attempt.BlockedUntil = now.Add(loginBlockDuration)
	}
	l.attempts[key] = attempt
}

func (l *loginLimiter) success(key string) {
	l.mu.Lock()
	delete(l.attempts, key)
	l.mu.Unlock()
}

func secureEqual(got, want string) bool {
	if len(got) != len(want) {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func writeHTML(w http.ResponseWriter, logger *slog.Logger, status int, body string) {
	body = injectCSRFForms(body, csrfTokenForWriter(w))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; connect-src 'self' ws: wss:; img-src 'self' data:; font-src 'self' data:")
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
			<div class="metric"><span>Uptime</span><strong>` + formatDuration(info.Uptime) + `</strong><small>` + fmt.Sprintf("%d apps · %d users · live usage below", appCount, userCount) + `</small></div>
		</section>

		<section class="panel panel-pad" style="margin-top:18px" id="resources-overview">
			<div class="section-title">
				<div><h2>Runtime resources</h2><p class="note">Live on-demand snapshot from systemd and Docker. Cumulative CPU time is not CPU percentage.</p></div>
				<a class="secondary" href="/docker">Manage containers</a>
			</div>
			<div class="metrics-grid" style="margin:12px 0 16px">
				<div class="metric"><span>Apps running</span><strong id="res-apps">Loading…</strong></div>
				<div class="metric"><span>Docker containers</span><strong id="res-containers">Loading…</strong></div>
				<div class="metric"><span>Container resources</span><strong id="res-docker-usage">Loading…</strong><small>Sum of running container measurements</small></div>
				<div class="metric"><span>Process memory</span><strong id="res-app-memory">Loading…</strong><small>Sum of measured active app units (not total host RAM)</small></div>
			</div>
			<div class="grid" style="grid-template-columns:repeat(auto-fit,minmax(min(100%,340px),1fr));gap:14px">
				<div><h3 style="margin-bottom:10px">Applications</h3><div class="table-scroll"><table><thead><tr><th>App</th><th>State</th><th>Memory</th><th>CPU time</th></tr></thead><tbody id="res-app-table"><tr><td colspan="4">Loading…</td></tr></tbody></table></div></div>
				<div><h3 style="margin-bottom:10px">Docker</h3><div class="table-scroll"><table><thead><tr><th>Container</th><th>State</th><th>CPU</th><th>Memory</th></tr></thead><tbody id="res-docker-table"><tr><td colspan="4">Loading…</td></tr></tbody></table></div></div>
			</div>
			<p class="note" id="res-status" style="margin-top:12px">Resource checks run only when the dashboard is opened.</p>
		</section>
		<script>
		(function(){
			const byId=(id)=>document.getElementById(id);
			const renderTable=(id,items,columns)=>{
				const body=byId(id);body.replaceChildren();
				if(!items||!items.length){const tr=document.createElement('tr'),td=document.createElement('td');td.colSpan=columns.length;td.textContent='No available entries';tr.append(td);body.append(tr);return}
				for(const item of items){const tr=document.createElement('tr');for(const key of columns){const td=document.createElement('td');const value=item[key];td.textContent=value===undefined?'No data':String(value);tr.append(td)}body.append(tr)}
			};
			fetch('/dashboard/resources',{credentials:'same-origin',cache:'no-store'})
				.then(r=>{if(!r.ok)throw new Error('Resource snapshot unavailable');return r.json()})
				.then(data=>{
					byId('res-apps').textContent=data.apps_running+' / '+data.apps_total;
					byId('res-containers').textContent=data.containers_running+' / '+data.containers_total;
					const used=(data.units||[]).filter(x=>x.active&&x.memory!=='No data');
					byId('res-app-memory').textContent=data.apps_memory||'No data';
					const busy=(data.containers||[]).filter(x=>x.running&&x.cpu!=='No data');
					byId('res-docker-usage').textContent=(data.containers_memory||'No data')+' / '+(data.containers_cpu||'No data');
					renderTable('res-app-table',(data.units||[]).map(x=>({...x,active:x.active?'Running':'Stopped'})),['name','active','memory','cpu_time']);
					renderTable('res-docker-table',(data.containers||[]).map(x=>({...x,running:x.running?'Running':'Stopped'})),['name','running','cpu','memory']);
					byId('res-status').textContent=[data.apps_error,data.docker_error].filter(Boolean).join(' · ')||'Snapshot loaded. No periodic resource polling.';
				})
				.catch(e=>{byId('res-status').textContent=e.message;for(const id of ['res-apps','res-containers','res-app-memory','res-docker-usage'])byId(id).textContent='Unavailable';});
		})();
		</script>

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
			<a class="card" href="/software"><div class="card-icon">PKG</div><h2>Software</h2><p>Install and update Docker, Node.js, Tailwind, Go and shared build tools.</p></a>
			<a class="card" href="/docker"><div class="card-icon">CTR</div><h2>Docker</h2><p>Manage Docker containers, restart policy and lifecycle actions.</p></a>
			<a class="card" href="/terminal"><div class="card-icon">TTY</div><h2>Terminal</h2><p>Open a root or managed-user shell from the authenticated panel.</p></a>
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
