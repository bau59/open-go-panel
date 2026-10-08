package server

import (
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/bau59/open-go-panel/internal/security"
)

type securityPageData struct {
	Status        security.Status
	Decisions     []security.Decision
	Alerts        []security.Alert
	Allowlist     security.Allowlist
	DecisionPage  pageInfo
	AlertPage     pageInfo
	TrustPage     pageInfo
	DecisionQuery string
	AlertQuery    string
	TrustQuery    string
	AlertSince    string
	Query         url.Values
	DataError     string
	Message       string
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

	mux.Handle("POST /security/service", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if err := cfg.Security.ControlService(r.Context(), r.FormValue("component"), r.FormValue("action")); err != nil {
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

	mux.Handle("POST /security/web/disable", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := cfg.Security.DisableWebProtection(r.Context()); err != nil {
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

	mux.Handle("POST /security/firewall/disable", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := cfg.Security.DisableFirewall(r.Context()); err != nil {
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
	values := cloneValues(r.URL.Query())
	data := securityPageData{
		Status:        cfg.Security.Status(r.Context()),
		Message:       message,
		Query:         values,
		DecisionQuery: strings.TrimSpace(values.Get("decision_q")),
		AlertQuery:    strings.TrimSpace(values.Get("alert_q")),
		TrustQuery:    strings.TrimSpace(values.Get("trust_q")),
		AlertSince:    strings.TrimSpace(values.Get("alerts_since")),
	}
	if data.AlertSince != "7d" && data.AlertSince != "30d" {
		data.AlertSince = "24h"
	}

	if data.Status.Installed {
		var errorsList []string

		if decisions, err := cfg.Security.DecisionsData(r.Context()); err == nil {
			decisions = filterDecisions(decisions, data.DecisionQuery)
			data.Decisions, data.DecisionPage = paginate(decisions, parsePage(r, "decisions_page"), parsePerPage(r))
		} else {
			errorsList = append(errorsList, "decisions: "+err.Error())
		}

		if alerts, err := cfg.Security.AlertsDataSince(r.Context(), data.AlertSince); err == nil {
			alerts = filterAlerts(alerts, data.AlertQuery)
			data.Alerts, data.AlertPage = paginate(alerts, parsePage(r, "alerts_page"), parsePerPage(r))
		} else {
			errorsList = append(errorsList, "alerts: "+err.Error())
		}

		if allowlist, err := cfg.Security.AllowlistData(r.Context()); err == nil {
			allowlist.Entries = filterAllowEntries(allowlist.Entries, data.TrustQuery)
			allowlist.Entries, data.TrustPage = paginate(allowlist.Entries, parsePage(r, "trust_page"), parsePerPage(r))
			data.Allowlist = allowlist
		} else {
			errorsList = append(errorsList, "allowlist: "+err.Error())
		}
		data.DataError = strings.Join(errorsList, "; ")
	}

	writeHTML(w, cfg.Logger, statusCode, securityPage(data))
}

func filterDecisions(items []security.Decision, query string) []security.Decision {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return items
	}
	out := make([]security.Decision, 0, len(items))
	for _, item := range items {
		text := strings.ToLower(strings.Join([]string{item.Value, item.Reason, item.Country, item.AS, item.Action, item.Source}, " "))
		if strings.Contains(text, query) {
			out = append(out, item)
		}
	}
	return out
}

func filterAlerts(items []security.Alert, query string) []security.Alert {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return items
	}
	out := make([]security.Alert, 0, len(items))
	for _, item := range items {
		text := strings.ToLower(strings.Join([]string{item.ID, item.Value, item.Reason, item.Country, item.AS, item.Decisions, item.Kind}, " "))
		if strings.Contains(text, query) {
			out = append(out, item)
		}
	}
	return out
}

func filterAllowEntries(items []security.AllowEntry, query string) []security.AllowEntry {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return items
	}
	out := make([]security.AllowEntry, 0, len(items))
	for _, item := range items {
		text := strings.ToLower(item.Value + " " + item.Comment + " " + item.Expiration)
		if strings.Contains(text, query) {
			out = append(out, item)
		}
	}
	return out
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

	stateText := func(ok bool) string {
		if ok {
			return `<span class="state-text ok"><i></i>active</span>`
		}
		return `<span class="state-text warn"><i></i>inactive</span>`
	}

	serviceActions := func(component string, active bool) string {
		if active {
			return `
				<div class="actions component-actions">
					<form method="post" action="/security/service"><input type="hidden" name="component" value="` + component + `"><input type="hidden" name="action" value="restart"><button class="secondary">Restart</button></form>
					<form method="post" action="/security/service" onsubmit="return confirm('Stop this security component?')"><input type="hidden" name="component" value="` + component + `"><input type="hidden" name="action" value="stop"><button class="secondary">Stop</button></form>
				</div>`
		}
		return `<div class="component-actions"><form method="post" action="/security/service"><input type="hidden" name="component" value="` + component + `"><input type="hidden" name="action" value="start"><button class="secondary">Start</button></form></div>`
	}

	install := ""
	if !data.Status.Installed {
		install = `
		<section class="panel panel-pad" style="margin-bottom:16px">
			<div class="section-title">
				<div><h2>Install recommended protection</h2><p class="note" style="margin:6px 0 0">Installs CrowdSec Security Engine and the official nftables firewall bouncer.</p></div>
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
	setupClass := "warn"
	setupText := "Install or start CrowdSec, then enable web protection and the recommended firewall."
	if data.Status.EngineActive && data.Status.BouncerActive && data.Status.WebProtection && data.Status.FirewallActive {
		setupState = "Protected"
		setupClass = "ok"
		setupText = "Recommended protection is enabled: detection, dynamic blocking, Caddy log analysis and deny-by-default firewall."
	}

	webAction := `<div class="component-actions"><form method="post" action="/security/web/enable"><button class="secondary">Enable</button></form></div>`
	if data.Status.WebProtection {
		webAction = `<div class="component-actions"><form method="post" action="/security/web/disable" onsubmit="return confirm('Disable CrowdSec analysis of Caddy logs?')"><button class="secondary">Disable</button></form></div>`
	}
	firewallAction := `<div class="component-actions"><form method="post" action="/security/firewall/enable"><button class="secondary">Enable recommended</button></form></div>`
	if data.Status.FirewallActive {
		firewallAction = `<div class="component-actions"><form method="post" action="/security/firewall/disable" onsubmit="return confirm('Disable UFW firewall?')"><button class="secondary">Disable UFW</button></form></div>`
	}

	var decisions strings.Builder
	for _, item := range data.Decisions {
		if item.Value == "" {
			continue
		}
		fmt.Fprintf(&decisions, `
			<tr>
				<td><code>%s</code></td><td>%s</td><td>%s</td><td>%s</td><td>%s</td>
				<td><form method="post" action="/security/unban"><input type="hidden" name="ip" value="%s"><button class="secondary">Unban</button></form></td>
			</tr>`,
			html.EscapeString(item.Value), html.EscapeString(item.Reason), html.EscapeString(item.Country),
			html.EscapeString(item.AS), html.EscapeString(item.Expiration), html.EscapeString(item.Value),
		)
	}
	if decisions.Len() == 0 {
		decisions.WriteString(`<tr><td colspan="6" class="empty">No active CrowdSec decisions.</td></tr>`)
	}

	var allowRows strings.Builder
	for _, item := range data.Allowlist.Entries {
		fmt.Fprintf(&allowRows, `
			<tr><td><code>%s</code></td><td>%s</td><td>%s</td>
			<td><form method="post" action="/security/untrust"><input type="hidden" name="value" value="%s"><button class="secondary">Remove</button></form></td></tr>`,
			html.EscapeString(item.Value), html.EscapeString(item.Comment), html.EscapeString(item.Expiration), html.EscapeString(item.Value),
		)
	}
	if allowRows.Len() == 0 {
		allowRows.WriteString(`<tr><td colspan="4" class="empty">No trusted addresses.</td></tr>`)
	}

	var alertRows strings.Builder
	for _, item := range data.Alerts {
		fmt.Fprintf(&alertRows, `
			<tr><td>%s</td><td><code>%s</code></td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>`,
			html.EscapeString(item.ID), html.EscapeString(item.Value), html.EscapeString(item.Reason),
			html.EscapeString(item.Country), html.EscapeString(item.Decisions), html.EscapeString(item.CreatedAt),
		)
	}
	if alertRows.Len() == 0 {
		alertRows.WriteString(`<tr><td colspan="6" class="empty">No CrowdSec alerts for this filter.</td></tr>`)
	}

	return pageHead("Security") + `<body>` + appHeader("security") + `
	<main class="shell">
		<div class="page-head">
			<div><p class="eyebrow">Ubuntu security</p><h1>Security</h1><p class="sub">CrowdSec handles detection and dynamic bans. UFW controls the static inbound perimeter.</p></div>
		</div>
		` + alert + install + `

		<section class="panel panel-pad" style="margin-bottom:16px">
			<div class="section-title">
				<div><h2>Protection components</h2><p class="note" style="margin:6px 0 0">Each component can be managed independently.</p></div>
				<span class="state-text ` + setupClass + `"><i></i>` + setupState + `</span>
			</div>
			<div class="metrics-grid">
				<div class="metric security-component"><div class="security-component-head"><span>CrowdSec engine</span>` + stateText(data.Status.EngineActive) + `</div><strong>Detection</strong><p class="note">Behavior analysis and decisions.</p>` + serviceActions("engine", data.Status.EngineActive) + `</div>
				<div class="metric security-component"><div class="security-component-head"><span>Firewall bouncer</span>` + stateText(data.Status.BouncerActive) + `</div><strong>nftables</strong><p class="note">Applies CrowdSec decisions.</p>` + serviceActions("bouncer", data.Status.BouncerActive) + `</div>
				<div class="metric security-component"><div class="security-component-head"><span>Web traffic</span>` + stateText(data.Status.WebProtection) + `</div><strong>Caddy logs</strong><p class="note">Feeds HTTP events into CrowdSec.</p>` + webAction + `</div>
				<div class="metric security-component"><div class="security-component-head"><span>Perimeter</span>` + stateText(data.Status.FirewallActive) + `</div><strong>UFW</strong><p class="note">Static inbound firewall policy.</p>` + firewallAction + `</div>
			</div>
			<p class="sub" style="margin:16px 0 0">` + setupText + `</p>
		</section>

		<details class="panel panel-pad advanced-block" style="margin-bottom:16px">
			<summary class="section-title" style="margin:0;cursor:pointer"><div><h2>Firewall rules</h2><p class="note" style="margin:6px 0 0">Current UFW rules.</p></div><span class="secondary">View</span></summary>
			<pre class="security-output" style="margin-top:18px">` + html.EscapeString(data.Status.FirewallStatus) + `</pre>
		</details>

		<section class="panel" style="margin-bottom:16px">
			<div class="panel-pad">
				<div class="section-title"><div><h2>Blocked IPs</h2><p class="note" style="margin:6px 0 0">Temporary decisions expire automatically.</p></div></div>
				<form class="list-toolbar" method="get" action="/security" style="padding:0;border:0">
					<input name="decision_q" value="` + html.EscapeString(data.DecisionQuery) + `" placeholder="Search IP, scenario, country or ASN">
					<button class="secondary">Search</button>
				</form>
			</div>
			<table><thead><tr><th>IP / value</th><th>Reason</th><th>Country</th><th>Network</th><th>Expires</th><th></th></tr></thead><tbody>` + decisions.String() + `</tbody></table>
			` + pagerHTML("/security", "decisions_page", data.Query, data.DecisionPage) + `
		</section>

		<section class="panel" style="margin-bottom:16px">
			<div class="panel-pad">
				<div class="section-title"><div><h2>Never block</h2><p class="note" style="margin:6px 0 0">Trusted administration IPs and CIDRs.</p></div></div>
				<form method="post" action="/security/trust" class="compact-form compact-form-3">
					<input name="value" placeholder="IP or CIDR" required><input name="comment" placeholder="Comment"><button class="button">Trust</button>
				</form>
				<form class="list-toolbar" method="get" action="/security" style="padding:14px 0 0;border:0">
					<input name="trust_q" value="` + html.EscapeString(data.TrustQuery) + `" placeholder="Search trusted addresses">
					<button class="secondary">Search</button>
				</form>
			</div>
			<table><thead><tr><th>Address</th><th>Comment</th><th>Expiration</th><th></th></tr></thead><tbody>` + allowRows.String() + `</tbody></table>
			` + pagerHTML("/security", "trust_page", data.Query, data.TrustPage) + `
		</section>

		<section class="panel">
			<div class="panel-pad">
				<div class="section-title"><div><h2>Alerts</h2><p class="note" style="margin:6px 0 0">Detection history remains after temporary bans expire.</p></div></div>
				<form class="list-toolbar" method="get" action="/security" style="padding:0;border:0">
					<input name="alert_q" value="` + html.EscapeString(data.AlertQuery) + `" placeholder="Search alert source or scenario">
					<select name="alerts_since">
						<option value="24h"` + selected(data.AlertSince, "24h") + `>Last 24 hours</option>
						<option value="7d"` + selected(data.AlertSince, "7d") + `>Last 7 days</option>
						<option value="30d"` + selected(data.AlertSince, "30d") + `>Last 30 days</option>
					</select>
					<button class="secondary">Apply</button>
				</form>
			</div>
			<table><thead><tr><th>ID</th><th>Source</th><th>Reason</th><th>Country</th><th>Decision</th><th>Created</th></tr></thead><tbody>` + alertRows.String() + `</tbody></table>
			` + pagerHTML("/security", "alerts_page", data.Query, data.AlertPage) + `
		</section>
	</main>
</body></html>`
}
