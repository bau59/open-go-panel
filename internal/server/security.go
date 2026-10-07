package server

import (
	"html"
	"net"
	"net/http"
	"strings"

	"github.com/bau59/open-go-panel/internal/security"
)

func registerSecurityRoutes(mux *http.ServeMux, store *sessionStore, cfg Config) {
	mux.Handle("GET /security", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeHTML(w, cfg.Logger, http.StatusOK, securityPage(
			cfg.Security.Status(r.Context()),
			cfg.Security.Decisions(r.Context()),
			cfg.Security.Alerts(r.Context()),
			cfg.Security.Allowlist(r.Context()),
			"",
		))
	})))

	mux.Handle("POST /security/install", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trustedIP := remoteIP(r)
		if err := cfg.Security.StartInstall(trustedIP); err != nil {
			writeSecurityPage(w, r, cfg, err.Error())
			return
		}
		http.Redirect(w, r, "/security", http.StatusSeeOther)
	})))

	mux.Handle("POST /security/unban", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if err := cfg.Security.Unban(r.Context(), r.FormValue("ip")); err != nil {
			writeSecurityPage(w, r, cfg, err.Error())
			return
		}
		http.Redirect(w, r, "/security", http.StatusSeeOther)
	})))

	mux.Handle("POST /security/trust", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if err := cfg.Security.Trust(r.Context(), r.FormValue("value"), r.FormValue("comment")); err != nil {
			writeSecurityPage(w, r, cfg, err.Error())
			return
		}
		http.Redirect(w, r, "/security", http.StatusSeeOther)
	})))

	mux.Handle("POST /security/untrust", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if err := cfg.Security.Untrust(r.Context(), r.FormValue("value")); err != nil {
			writeSecurityPage(w, r, cfg, err.Error())
			return
		}
		http.Redirect(w, r, "/security", http.StatusSeeOther)
	})))
}

func writeSecurityPage(w http.ResponseWriter, r *http.Request, cfg Config, message string) {
	writeHTML(w, cfg.Logger, http.StatusBadRequest, securityPage(
		cfg.Security.Status(r.Context()),
		cfg.Security.Decisions(r.Context()),
		cfg.Security.Alerts(r.Context()),
		cfg.Security.Allowlist(r.Context()),
		message,
	))
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func securityPage(status security.Status, decisions, alerts, allowlist, message string) string {
	alert := ""
	if message != "" {
		alert = `<div class="alert">` + html.EscapeString(message) + `</div>`
	}

	badge := func(ok bool) string {
		if ok {
			return `<span class="status-badge ok">active</span>`
		}
		return `<span class="status-badge warn">inactive</span>`
	}

	install := ""
	if !status.Installed {
		install = `
		<section class="panel panel-pad" style="margin-bottom:16px">
			<div class="section-title">
				<div>
					<h2>Install CrowdSec</h2>
					<p class="note" style="margin:6px 0 0">Security Engine + official nftables firewall bouncer.</p>
				</div>
			</div>
			<p class="sub">Your current connection IP is added to the Open Go Panel allowlist before the firewall bouncer is enabled.</p>
			<form method="post" action="/security/install" style="margin-top:14px">
				<button class="button">Install CrowdSec</button>
			</form>
		</section>`
	}
	if status.Installing {
		install = `<div class="alert" style="border-color:rgba(246,196,83,.25);background:var(--warning-soft);color:#f7d884">CrowdSec installation is running. Refresh this page in a minute.</div>`
	}
	if status.InstallError != "" {
		install += `<div class="alert">` + html.EscapeString(status.InstallError) + `</div>`
	}

	return pageHead("Security") + `<body>` + appHeader("security") + `
	<main class="shell">
		<div class="page-head">
			<div>
				<p class="eyebrow">Ubuntu security</p>
				<h1>Security</h1>
				<p class="sub">CrowdSec detects abusive traffic. The official firewall bouncer applies decisions through nftables.</p>
			</div>
		</div>

		` + alert + install + `

		<section class="metrics-grid">
			<div class="metric"><span>Security Engine</span><strong>CrowdSec</strong><small>` + badge(status.EngineActive) + `</small></div>
			<div class="metric"><span>Firewall bouncer</span><strong>nftables</strong><small>` + badge(status.BouncerActive) + `</small></div>
			<div class="metric"><span>Ban model</span><strong>Temporary</strong><small>Decisions expire automatically</small></div>
			<div class="metric"><span>Allowlist</span><strong>open-go-panel</strong><small>Trusted IPs and CIDR ranges</small></div>
		</section>

		<div class="grid security-grid" style="margin-top:16px">
			<section class="panel panel-pad">
				<div class="section-title"><div><h2>Active decisions</h2><p class="note">Current bans and expiration.</p></div></div>
				<pre class="security-output">` + html.EscapeString(decisions) + `</pre>
				<form method="post" action="/security/unban" class="compact-form">
					<input name="ip" placeholder="IP to unban" required>
					<button class="secondary">Unban</button>
				</form>
			</section>

			<section class="panel panel-pad">
				<div class="section-title"><div><h2>Trusted addresses</h2><p class="note">These IPs and networks should not be blocked.</p></div></div>
				<pre class="security-output">` + html.EscapeString(allowlist) + `</pre>
				<form method="post" action="/security/trust" class="compact-form compact-form-3">
					<input name="value" placeholder="IP or CIDR" required>
					<input name="comment" placeholder="Comment">
					<button class="button">Trust</button>
				</form>
				<form method="post" action="/security/untrust" class="compact-form">
					<input name="value" placeholder="IP or CIDR to remove" required>
					<button class="secondary">Remove</button>
				</form>
			</section>
		</div>

		<section class="panel panel-pad" style="margin-top:16px">
			<div class="section-title"><div><h2>Alerts · last 24 hours</h2><p class="note">Detection history remains after temporary bans expire.</p></div></div>
			<pre class="security-output">` + html.EscapeString(alerts) + `</pre>
		</section>
	</main>
</body></html>`
}
