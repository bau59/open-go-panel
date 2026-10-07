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

	mux.Handle("POST /security/web/enable", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := cfg.Security.EnableWebProtection(r.Context()); err != nil {
			writeSecurityPage(w, r, cfg, err.Error())
			return
		}
		http.Redirect(w, r, "/security", http.StatusSeeOther)
	})))

	mux.Handle("POST /security/protection/start", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := cfg.Security.StartProtection(r.Context()); err != nil {
			writeSecurityPage(w, r, cfg, err.Error())
			return
		}
		http.Redirect(w, r, "/security", http.StatusSeeOther)
	})))

	mux.Handle("POST /security/firewall/enable", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := cfg.Security.EnableFirewall(r.Context()); err != nil {
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

	setupState := "Needs setup"
	setupClass := " warn"
	setupText := "Enable CrowdSec protection first, then apply the firewall defaults."
	setupAction := ""
	if status.Installed && (!status.EngineActive || !status.BouncerActive) {
		setupText = "CrowdSec is installed, but one of its protection services is stopped."
		setupAction = `<form method="post" action="/security/protection/start"><button class="button">Start CrowdSec protection</button></form>`
	} else if status.EngineActive && status.BouncerActive && !status.WebProtection {
		setupState = "Almost ready"
		setupText = "CrowdSec is running, but Caddy web traffic is not connected yet. Enable web protection to detect HTTP scanners and attacks."
		setupAction = `<form method="post" action="/security/web/enable"><button class="button">Enable web protection</button></form>`
	} else if status.EngineActive && status.BouncerActive && !status.FirewallActive {
		setupState = "Almost ready"
		setupText = "CrowdSec is detecting and blocking abusive IPs. Enable UFW to close unused public ports."
		setupAction = `<form method="post" action="/security/firewall/enable"><button class="button">Enable recommended firewall</button></form>`
	} else if status.EngineActive && status.BouncerActive && status.FirewallActive && status.WebProtection {
		setupState = "Protected"
		setupClass = " ok"
		setupText = "Recommended protection is enabled: detection, dynamic IP blocking and a deny-by-default firewall."
	}
	if !status.Installed {
		setupAction = `<form method="post" action="/security/install"><button class="button">Install recommended protection</button></form>`
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

		<section class="panel panel-pad" style="margin-bottom:16px">
			<div class="section-title">
				<div>
					<h2>Recommended protection</h2>
					<p class="note" style="margin:6px 0 0">For a normal public server, all three layers below should be active.</p>
				</div>
				<span class="status-badge` + setupClass + `">` + setupState + `</span>
			</div>
			<div class="grid security-grid">
				<div class="card" style="min-height:0">
					<h2>1. CrowdSec engine</h2>
					<p>Reads SSH and Caddy traffic, detects scanners, brute force and common HTTP attacks, then creates temporary decisions.</p>
					<div style="margin-top:12px">` + badge(status.EngineActive) + `</div>
				</div>
				<div class="card" style="min-height:0">
					<h2>2. Firewall bouncer</h2>
					<p>Takes CrowdSec decisions and blocks abusive IP addresses at nftables level before they reach your apps.</p>
					<div style="margin-top:12px">` + badge(status.BouncerActive) + `</div>
				</div>
				<div class="card" style="min-height:0">
					<h2>3. UFW firewall</h2>
					<p>Closes unused inbound ports. Open Go Panel keeps SSH, HTTP, HTTPS and the panel port reachable.</p>
					<div style="margin-top:12px">` + badge(status.FirewallActive) + `</div>
				</div>
				<div class="card" style="min-height:0">
					<h2>Web traffic</h2>
					<p>Caddy logs are connected to the CrowdSec Caddy collection so HTTP attacks can be detected from access traffic.</p>
					<div style="margin-top:12px">` + badge(status.WebProtection) + `</div>
				</div>
			</div>
			<p class="sub" style="margin:16px 0 12px">` + setupText + `</p>
			<div class="actions" style="justify-content:flex-start">` + setupAction + `</div>
		</section>

		<section class="metrics-grid">
			<div class="metric"><span>Security Engine</span><strong>CrowdSec</strong><small>` + badge(status.EngineActive) + `</small></div>
			<div class="metric"><span>Firewall</span><strong>UFW</strong><small>` + badge(status.FirewallActive) + `</small></div>
			<div class="metric"><span>Firewall bouncer</span><strong>nftables</strong><small>` + badge(status.BouncerActive) + `</small></div>
			<div class="metric"><span>Allowlist</span><strong>open-go-panel</strong><small>Trusted IPs and CIDR ranges</small></div>
		</section>

		<section class="panel panel-pad" style="margin-top:16px">
			<div class="section-title">
				<div><h2>Firewall rules</h2><p class="note">UFW is the static perimeter firewall. Use the recommended action once; CrowdSec handles dynamic attacker bans separately.</p></div>
				` + badge(status.FirewallActive) + `
			</div>
			<pre class="security-output">` + html.EscapeString(status.FirewallStatus) + `</pre>
			<form method="post" action="/security/firewall/enable" style="margin-top:12px">
				<button class="button">Apply recommended firewall</button>
			</form>
		</section>

		<div class="grid security-grid" style="margin-top:16px">
			<section class="panel panel-pad">
				<div class="section-title"><div><h2>Blocked IPs</h2><p class="note">Temporary CrowdSec bans. They expire automatically; use Unban only when an address was blocked by mistake.</p></div></div>
				<pre class="security-output">` + html.EscapeString(decisions) + `</pre>
				<form method="post" action="/security/unban" class="compact-form">
					<input name="ip" placeholder="IP to unban" required>
					<button class="secondary">Unban</button>
				</form>
			</section>

			<section class="panel panel-pad">
				<div class="section-title"><div><h2>Never block</h2><p class="note">Add your office, VPN or administration IP/CIDR here. CrowdSec will ignore these trusted addresses.</p></div></div>
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
