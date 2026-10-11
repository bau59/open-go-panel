package server

import (
	"context"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	panelcaddy "github.com/bau59/open-go-panel/internal/caddy"
	"github.com/bau59/open-go-panel/internal/security"
)

func registerSecurityDetailRoutes(mux *http.ServeMux, store *sessionStore, cfg Config) {
	mux.Handle("GET /security/ip", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := strings.TrimSpace(r.URL.Query().Get("ip"))
		if net.ParseIP(ip) == nil {
			http.Error(w, "invalid IP address", http.StatusBadRequest)
			return
		}
		window := r.URL.Query().Get("period")
		if window != "7d" && window != "30d" {
			window = "24h"
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		result, err := cfg.Caddy.QueryIPAccessLogs(ctx, ip, window)
		message := ""
		if err != nil {
			message = err.Error()
		}
		writeHTML(w, cfg.Logger, http.StatusOK, securityIPPage(ip, window, result, message))
	})))

	mux.Handle("GET /security/profiles", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		kind := profileKind(r.URL.Query().Get("file"))
		doc, err := cfg.Security.ReadProfilesFile(kind)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		message := ""
		if r.URL.Query().Get("saved") == "1" {
			message = "Profiles validated and applied successfully."
		}
		writeHTML(w, cfg.Logger, http.StatusOK, securityProfilesPage(doc, message))
	})))

	mux.Handle("POST /security/profiles", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 80*1024)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid profile form or size limit exceeded", http.StatusBadRequest)
			return
		}
		kind := strings.TrimSpace(r.FormValue("file"))
		if kind != "main" && kind != "local" {
			http.Error(w, "invalid profiles file", http.StatusBadRequest)
			return
		}
		content := r.FormValue("yaml")
		err := cfg.Security.SaveProfilesFile(r.Context(), kind, r.FormValue("version"), content)
		if err == nil {
			http.Redirect(w, r, "/security/profiles?file="+kind+"&saved=1", http.StatusSeeOther)
			return
		}
		doc, readErr := cfg.Security.ReadProfilesFile(kind)
		if readErr != nil {
			http.Error(w, err.Error()+"; "+readErr.Error(), http.StatusInternalServerError)
			return
		}
		// Show the rejected draft without changing the stored file.
		doc.Content = content
		writeHTML(w, cfg.Logger, http.StatusBadRequest, securityProfilesPage(doc, err.Error()))
	})))
}

func profileKind(raw string) string {
	if raw == "local" {
		return "local"
	}
	return "main"
}

func securityIPPage(ip, period string, result panelcaddy.LogResult, message string) string {
	esc := html.EscapeString
	qip := url.QueryEscape(ip)
	notice := ""
	if message != "" {
		notice = "<div class=\"alert\">" + esc(message) + "</div>"
	}
	hostCounts := map[string]int{}
	failures := 0
	for _, entry := range result.Entries {
		hostCounts[entry.Domain]++
		if entry.Status >= 400 {
			failures++
		}
	}
	hosts := make([]string, 0, len(hostCounts))
	for host := range hostCounts {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	var domains strings.Builder
	for _, host := range hosts {
		if domains.Len() > 0 {
			domains.WriteString(" · ")
		}
		fmt.Fprintf(&domains, "%s (%d)", esc(host), hostCounts[host])
	}
	if domains.Len() == 0 {
		domains.WriteString("No matching access logs")
	}

	var rows strings.Builder
	for _, e := range result.Entries {
		status := strconv.Itoa(e.Status)
		if e.Status == 0 {
			status = "—"
		}
		fmt.Fprintf(&rows, "<tr><td><span class=\"note\">%s</span></td><td><code>%s</code></td><td>%s</td><td><code>%s</code></td><td>%s</td><td>%.0f ms</td></tr>",
			esc(e.Time.Local().Format("2006-01-02 15:04:05")), esc(e.Domain), esc(e.Method),
			esc(e.URI), status, e.DurationMS)
	}
	if rows.Len() == 0 {
		rows.WriteString("<tr><td colspan=\"6\" class=\"empty\">No HTTP access events found for this IP in the selected period. Old journals may have expired, or access logging may be disabled.</td></tr>")
	}
	if result.HasNext {
		notice += "<p class=\"note\">Showing the latest 200 matching requests. Narrow the period to inspect recent activity.</p>"
	}

	return pageHead("IP investigation") + "<body>" + appHeader("security") + `
	<main class="shell">
		<div class="page-head"><div><p class="eyebrow"><a href="/security">Security</a> / IP investigation</p><h1>IP: <code>` + esc(ip) + `</code></h1><p class="sub">On-demand Caddy access-log investigation. No additional monitoring, storage or log duplication.</p></div></div>
		` + notice + `
		<section class="panel panel-pad" style="margin-bottom:16px">
			<form method="get" action="/security/ip" class="list-toolbar" style="padding:0;border:0">
				<input type="hidden" name="ip" value="` + esc(ip) + `">
				<label for="security-period">Journal period</label>
				<select id="security-period" name="period">
					<option value="24h"` + selected(period, "24h") + `>Last 24 hours</option>
					<option value="7d"` + selected(period, "7d") + `>Last 7 days</option>
					<option value="30d"` + selected(period, "30d") + `>Last 30 days</option>
				</select>
				<button class="secondary">Search logs</button>
			</form>
			<div class="metrics-grid" style="margin-top:16px">
				<div class="metric"><span>Matching requests</span><strong>` + strconv.Itoa(len(result.Entries)) + `</strong></div>
				<div class="metric"><span>HTTP 4xx / 5xx</span><strong>` + strconv.Itoa(failures) + `</strong></div>
			</div>
			<p class="note" style="margin-top:12px">Domains in this sample: ` + domains.String() + `</p>
			<p class="note">Only recorded access requests are shown. These are evidence of activity, not necessarily the exact events that triggered a CrowdSec scenario. Times are shown in the server's timezone.</p>
			<p class="note"><a href="/security?decision_q=` + qip + `">Back to decisions</a></p>
		</section>
		<section class="panel" style="margin-bottom:16px">
			<div class="panel-pad"><div class="section-title"><div><h2>Recent HTTP requests</h2><p class="note">Most recent first · maximum 200 rows</p></div></div></div>
			<div style="overflow-x:auto"><table><thead><tr><th>Time</th><th>Domain</th><th>Method</th><th>Path &amp; query</th><th>Status</th><th>Duration</th></tr></thead><tbody>` + rows.String() + `</tbody></table></div>
		</section>
	</main></body></html>`
}

func securityProfilesPage(doc security.ProfilesFile, message string) string {
	esc := html.EscapeString
	notice := ""
	if message != "" {
		notice = "<div class=\"alert\">" + esc(message) + "</div>"
	}
	mode := "Main profiles"
	if doc.Kind == "local" {
		mode = "Local overrides"
	}
	note := "Profiles are evaluated in order. Filters decide whether an alert gets a ban; duration applies to new decisions only."
	if doc.Kind == "local" {
		note = "Optional profiles.yaml.local is evaluated before the main file. Use on_success: break to prevent a matching rule from falling through. YAML documents are not merged."
	}
	if !doc.Exists {
		note += " This file does not exist yet; saving creates it."
	}
	return pageHead("CrowdSec profiles") + "<body>" + appHeader("security") + `
	<main class="shell">
		<div class="page-head"><div><p class="eyebrow"><a href="/security">Security</a> / CrowdSec</p>
		<h1>Decision profiles</h1><p class="sub">Configure remediation filters, ban duration and actions in the native CrowdSec YAML configuration.</p></div></div>
		` + notice + `
		<section class="panel panel-pad" style="margin-bottom:16px">
			<div class="section-title"><div><h2>` + esc(mode) + `</h2><p class="note">` + esc(doc.Path) + `</p></div></div>
			<div class="actions" style="margin:12px 0 14px"><a href="/security/profiles?file=main">Main profiles</a><a href="/security/profiles?file=local">Local overrides</a></div>
			<p class="note">` + esc(note) + `</p>
			<form method="post" action="/security/profiles" onsubmit="return confirm('Validate and apply these CrowdSec profiles? The engine will restart.');">
				<input type="hidden" name="file" value="` + esc(doc.Kind) + `">
				<input type="hidden" name="version" value="` + esc(doc.Version) + `">
				<label for="crowdsec-yaml">Profile YAML</label>
				<textarea id="crowdsec-yaml" name="yaml" rows="25" spellcheck="false" required style="width:100%;min-height:360px;resize:vertical;font-family:monospace;line-height:1.5;margin-top:8px" placeholder="name: custom_ip_remediation&#10;filters:&#10; - Alert.Remediation == true &amp;&amp; Alert.GetScope() == &quot;Ip&quot;&#10;decisions:&#10; - type: ban&#10;   duration: 4h&#10;on_success: break">` + esc(doc.Content) + `</textarea>
				<p class="note" style="margin-top:8px">On save: check for external edits → atomic write → crowdsec -t → restart CrowdSec. Failed validation or restart restores the original file. Existing bans are not recalculated.</p>
				<div class="actions" style="margin-top:14px"><button class="button" type="submit">Validate &amp; apply</button><a href="/security">Back to Security</a></div>
			</form>
		</section>
	</main></body></html>`
}
