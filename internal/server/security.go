package server

import (
	"fmt"
	"html"
	"net"
	"net/http"
	"strings"

	"github.com/bau59/open-go-panel/internal/security"
)

type securityPageData struct {
	Status      security.Status
	Decisions   []security.Decision
	Alerts      []security.Alert
	Allowlist   security.Allowlist
	DataError   string
	Message     string
}

func registerSecurityRoutes(mux *http.ServeMux, store *sessionStore, cfg Config) {
	mux.Handle("GET /security", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeSecurityPage(w, r, cfg, http.StatusOK, "")
	})))

	mux.Handle("POST /security/install", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trustedIP := remoteIP(r)
		if err := cfg.Security.StartInstall(trustedIP); err != nil {
			writeSecurityPage(w, r, cfg, http.StatusBadRequest, err.Error())
			return
		}
		http.Redirect(w, r, "/security", http.StatusSeeOther)
	})))

	mux.Handle("POST /security/web/enable", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := cfg.Security.EnableWebProtection(r.Context()); err != nil {
			writeSecurityPage(w, r, cfg, http.StatusBadRequest, err.Error())
			return
		}
		http.Redirect(w, r, "/security", http.StatusSeeOther)
	})))

	mux.Handle("POST /security/protection/start", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := cfg.Security.StartProtection(r.Context()); err != nil {
			writeSecurityPage(w, r, cfg, http.StatusBadRequest, err.Error())
			return
		}
		http.Redirect(w, r, "/security", http.StatusSeeOther)
	})))

	mux.Handle("POST /security/firewall/enable", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := cfg.Security.EnableFirewall(r.Context()); err != nil {
			writeSecurityPage(w, r, cfg, http.StatusBadRequest, err.Error())
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
			writeSecurityPage(w, r, cfg, http.StatusBadRequest, err.Error())
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
			writeSecurityPage(w, r, cfg, http.StatusBadRequest, err.Error())
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
			writeSecurityPage(w, r, cfg, http.StatusBadRequest, err.Error())
			return
		}
		http.Redirect(w, r, "/security", http.StatusSeeOther)
	})))
}

func writeSecurityPage(w http.ResponseWriter, r *http.Request, cfg Config, statusCode int, message string) {
	data := securityPageData{
		Status:  cfg.Security.Status(r.Context()),
		Message: message,
	}
	if data.Status.Installed {
		var errorsList []string
		if decisions, err := cfg.Security.DecisionsData(r.Context()); err == nil {
			data.Decisions = decisions
		} else {
			errorsList = append(errorsList, "decisions: "+err.Error())
		}
		if alerts, err := cfg.Security.AlertsData(r.Context()); err == nil {
			data.Alerts = alerts
		} else {
			errorsList = append(errorsList, "alerts: "+err.Error())
		}
		if allowlist, err := cfg.Security.AllowlistData(r.Context()); err == nil {
			data.Allowlist = allowlist
		} else {
			errorsList = append(errorsList, "allowlist: "+err.Error())
		}
		data.DataError = strings.Join(errorsList, "; ")
	}
	writeHTML(w, cfg.Logger, statusCode, securityPage(data))
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func securityPage(data securityPageData) string {
	alert := ""
	if data.Message != "" {
		alert = `<div class="alert">` + html.EscapeString(data.Message) + `</div>`
	}
	if data.DataError != "" {
		alert += `<div class="alert">` + html.EscapeString(data.DataError) + `</div>`
	}

	badge := func(ok bool) string {
		if ok {
			return `<span class="status-badge ok">active</span>`
		}
		return `<span class="status-badge warn">inactive</span>`
	}

	install := ""
	if !data.Status.Installed {
		install = `
		<section class="panel panel-pad" style="margin-bottom:16px">
			<div class="section-title">
				<div>
					<h2>Install recommended protection</h2>
					<p class="note" style="margin:6px 0 0">Installs CrowdSec Security Engine and the official nftables firewall bouncer.</p>
				</div>
			</div>
			<p class="sub">Your current connection IP is added to the Open Go Panel allowlist before the bouncer is enabled.</p>
			<form method="post" action="/security/install" style="margin-top:14px"><button class="button">Install CrowdSec</button></form>
		</section>`
	}
	if data.Status.Installing {
		install = `<div class="alert" style="border-color:rgba(246,196,83,.25);background:var(--warning-soft);color:#f7d884">CrowdSec installation is running. Refresh this page in a minute.</div>`
	}
	if data.Status.InstallError != "" {
		install += `<div class="alert">` + html.EscapeString(data.Status.InstallError) + `</div>`
	}

	setupState := "Needs setup"
	setupClass := " warn"
	setupText := "Install or start CrowdSec, then enable web protection and the recommended firewall."
	setupAction := ""
	if !data.Status.Installed {
		setupAction = `<form method="post" action="/security/install"><button class="button">Install recommended protection</button></form>`
	} else if !data.Status.EngineActive || !data.Status.BouncerActive {
		setupText = "CrowdSec is installed, but one of its protection services is stopped."
		setupAction = `<form method="post" action="/security/protection/start"><button class="button">Start CrowdSec protection</button></form>`
	} else if !data.Status.WebProtection {
		setupState = "Almost ready"
		setupText = "CrowdSec is running, but Caddy web traffic is not connected yet."
		setupAction = `<form method="post" action="/security/web/enable"><button class="button">Enable web protection</button></form>`
	} else if !data.Status.FirewallActive {
		setupState = "Almost ready"
		setupText = "CrowdSec is detecting and blocking abusive IPs. Enable UFW to close unused public ports."
		setupAction = `<form method="post" action="/security/firewall/enable"><button class="button">Enable recommended firewall</button></form>`
	} else {
		setupState = "Protected"
		setupClass = " ok"
		setupText = "Recommended protection is enabled: detection, dynamic blocking, Caddy log analysis and deny-by-default firewall."
	}

	var decisions strings.Builder
	for _, item := range data.Decisions {
		value := item.Value
		if value == "" {
			continue
		}
		fmt.Fprintf(&decisions, `
			<tr>
				<td><code>%s</code></td>
				<td>%s</td>
				<td>%s</td>
				<td>%s</td>
				<td>%s</td>
				<td><form method="post" action="/security/unban"><input type="hidden" name="ip" value="%s"><button class="secondary">Unban</button></form></td>
			</tr>`,
			html.EscapeString(value),
			html.EscapeString(item.Reason),
			html.EscapeString(item.Country),
			html.EscapeString(item.AS),
			html.EscapeString(item.Expiration),
			html.EscapeString(value),
		)
	}
	if decisions.Len() == 0 {
		decisions.WriteString(`<tr><td colspan="6" class="empty">No active CrowdSec decisions.</td></tr>`)
	}

	var allowRows strings.Builder
	for _, item := range data.Allowlist.Entries {
		fmt.Fprintf(&allowRows, `
			<tr>
				<td><code>%s</code></td>
				<td>%s</td>
				<td>%s</td>
				<td><form method="post" action="/security/untrust"><input type="hidden" name="value" value="%s"><button class="secondary">Remove</button></form></td>
			</tr>`,
			html.EscapeString(item.Value),
			html.EscapeString(item.Comment),
			html.EscapeString(item.Expiration),
			html.EscapeString(item.Value),
		)
	}
	if allowRows.Len() == 0 {
		allowRows.WriteString(`<tr><td colspan="4" class="empty">No trusted addresses.</td></tr>`)
	}

	var alertRows strings.Builder
	for _, item := range data.Alerts {
		fmt.Fprintf(&alertRows, `
			<tr>
				<td>%s</td>
				<td><code>%s</code></td>
				<td>%s</td>
				<td>%s</td>
				<td>%s</td>
				<td>%s</td>
			</tr>`,
			html.EscapeString(item.ID),
			html.EscapeString(item.Value),
			html.EscapeString(item.Reason),
			html.EscapeString(item.Country),
			html.EscapeString(item.Decisions),
			html.EscapeString(item.CreatedAt),
		)
	}
	if alertRows.Len() == 0 {
		alertRows.WriteString(`<tr><td colspan="6" class="empty">No CrowdSec alerts in the last 24 hours.</td></tr>`)
	}

	return pageHead("Security") + `<body>` + appHeader("security") + `
	<main class="shell">
		<div class="page-head">
			<div>
				<p class="eyebrow">Ubuntu security</p>
				<h1>Security</h1>
				<p class="sub">CrowdSec handles detection and dynamic bans. UFW controls the static inbound perimeter.</p>
			</div>
		</div>

		` + alert + install + `

		<section class="panel panel-pad" style="margin-bottom:16px">
			<div class="section-title">
				<div><h2>Recommended protection</h2><p class="note" style="margin:6px 0 0">For a public server, all four indicators below should be active.</p></div>
				<span class="status-badge` + setupClass + `">` + setupState + `</span>
			</div>
			<div class="metrics-grid">
				<div class="metric"><span>CrowdSec engine</span><strong>Detection</strong><small>` + badge(data.Status.EngineActive) + `</small></div>
				<div class="metric"><span>Firewall bouncer</span><strong>nftables</strong><small>` + badge(data.Status.BouncerActive) + `</small></div>
				<div class="metric"><span>Web traffic</span><strong>Caddy logs</strong><small>` + badge(data.Status.WebProtection) + `</small></div>
				<div class="metric"><span>Perimeter</span><strong>UFW</strong><small>` + badge(data.Status.FirewallActive) + `</small></div>
			</div>
			<p class="sub" style="margin:16px 0 12px">` + setupText + `</p>
			<div class="actions" style="justify-content:flex-start">` + setupAction + `</div>
		</section>

		<section class="panel panel-pad" style="margin-bottom:16px">
			<div class="section-title">
				<div><h2>Firewall rules</h2><p class="note" style="margin:6px 0 0">UFW keeps SSH, HTTP, HTTPS and the Open Go Panel port open. CrowdSec manages attacker IPs separately.</p></div>
				` + badge(data.Status.FirewallActive) + `
			</div>
			<pre class="security-output">` + html.EscapeString(data.Status.FirewallStatus) + `</pre>
			<form method="post" action="/security/firewall/enable" style="margin-top:12px"><button class="secondary">Apply recommended firewall</button></form>
		</section>

		<section class="panel" style="margin-bottom:16px">
			<div class="panel-pad"><div class="section-title"><div><h2>Blocked IPs</h2><p class="note" style="margin:6px 0 0">Temporary decisions expire automatically. Unban only false positives.</p></div></div></div>
			<table>
				<thead><tr><th>IP / value</th><th>Reason</th><th>Country</th><th>Network</th><th>Expires</th><th></th></tr></thead>
				<tbody>` + decisions.String() + `</tbody>
			</table>
		</section>

		<section class="panel" style="margin-bottom:16px">
			<div class="panel-pad">
				<div class="section-title"><div><h2>Never block</h2><p class="note" style="margin:6px 0 0">Office, VPN and administration IPs/CIDRs that CrowdSec must ignore.</p></div></div>
				<form method="post" action="/security/trust" class="compact-form compact-form-3">
					<input name="value" placeholder="IP or CIDR" required>
					<input name="comment" placeholder="Comment">
					<button class="button">Trust</button>
				</form>
			</div>
			<table>
				<thead><tr><th>Address</th><th>Comment</th><th>Expiration</th><th></th></tr></thead>
				<tbody>` + allowRows.String() + `</tbody>
			</table>
		</section>

		<section class="panel">
			<div class="panel-pad"><div class="section-title"><div><h2>Alerts · last 24 hours</h2><p class="note" style="margin:6px 0 0">Detection history remains after temporary bans expire.</p></div></div></div>
			<table>
				<thead><tr><th>ID</th><th>Source</th><th>Reason</th><th>Country</th><th>Decision</th><th>Created</th></tr></thead>
				<tbody>` + alertRows.String() + `</tbody>
			</table>
		</section>
	</main>
</body></html>`
}
