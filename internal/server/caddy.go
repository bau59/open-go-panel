package server

import (
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"

	panelapp "github.com/bau59/open-go-panel/internal/app"
	panelcaddy "github.com/bau59/open-go-panel/internal/caddy"
)

func registerCaddyRoutes(mux *http.ServeMux, store *sessionStore, cfg Config) {
	mux.Handle("GET /caddy", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sites, _ := cfg.Caddy.Sites()
		config, _ := cfg.Caddy.Config()
		template, _ := cfg.Caddy.Template()
		writeHTML(w, cfg.Logger, http.StatusOK, caddyPage(cfg.Caddy.Status(r.Context()), sites, config, template, ""))
	})))

	mux.Handle("POST /caddy/template", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if err := cfg.Caddy.SetTemplate(r.Context(), r.FormValue("template")); err != nil {
			sites, _ := cfg.Caddy.Sites()
			config, _ := cfg.Caddy.Config()
			template, _ := cfg.Caddy.Template()
			writeHTML(w, cfg.Logger, http.StatusBadRequest, caddyPage(cfg.Caddy.Status(r.Context()), sites, config, template, err.Error()))
			return
		}
		http.Redirect(w, r, "/caddy", http.StatusSeeOther)
	})))

	mux.Handle("POST /caddy/template/recommended", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := cfg.Caddy.ResetTemplate(r.Context()); err != nil {
			sites, _ := cfg.Caddy.Sites()
			config, _ := cfg.Caddy.Config()
			template, _ := cfg.Caddy.Template()
			writeHTML(w, cfg.Logger, http.StatusBadRequest, caddyPage(cfg.Caddy.Status(r.Context()), sites, config, template, err.Error()))
			return
		}
		http.Redirect(w, r, "/caddy", http.StatusSeeOther)
	})))

	mux.Handle("POST /apps/{id}/domain", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid app id", http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		app, err := cfg.Apps.Get(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if app.Port == 0 {
			http.Error(w, "this app has no reverse proxy port", http.StatusBadRequest)
			return
		}
		if err := cfg.Caddy.SetSite(r.Context(), id, r.FormValue("domain"), app.Port); err != nil {
			status := cfg.Apps.Status(r.Context(), id)
			unit, _ := cfg.Apps.Unit(id)
			writeHTML(w, cfg.Logger, http.StatusBadRequest, appPage(app, status, unit, err.Error()))
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/apps/%d", id), http.StatusSeeOther)
	})))

	mux.Handle("POST /apps/{id}/domain/delete", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid app id", http.StatusBadRequest)
			return
		}
		if err := cfg.Caddy.RemoveSite(r.Context(), id); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/apps/%d", id), http.StatusSeeOther)
	})))
}

func caddyPage(status string, sites []panelcaddy.Site, config, template, message string) string {
	alert := ""
	if message != "" {
		alert = `<div class="alert">` + html.EscapeString(message) + `</div>`
	}

	var rows strings.Builder
	for _, site := range sites {
		fmt.Fprintf(&rows, `
			<tr>
				<td><strong>%s</strong></td>
				<td>#%d</td>
				<td><code>%s</code></td>
			</tr>`,
			html.EscapeString(site.Domain),
			site.AppID,
			html.EscapeString(site.Target()),
		)
	}
	if rows.Len() == 0 {
		rows.WriteString(`<tr><td colspan="3" class="empty">No domains configured.</td></tr>`)
	}

	statusClass := ""
	if status == "active" {
		statusClass = " ok"
	} else {
		statusClass = " warn"
	}

	return pageHead("Caddy") + `<body>` + appHeader("caddy") + `
	<main class="shell">
		<div class="page-head">
			<div>
				<p class="eyebrow">Edge / routing</p>
				<h1>Caddy</h1>
				<p class="sub">TLS certificates, domains and reverse proxy routing to applications.</p>
			</div>
			<span class="status-badge` + statusClass + `">` + html.EscapeString(status) + `</span>
		</div>
		` + alert + `
		<section class="metrics-grid" style="margin-bottom:16px">
			<div class="metric"><span>HTTPS</span><strong>Automatic</strong><small>Caddy issues and renews certificates.</small></div>
			<div class="metric"><span>Compression</span><strong>zstd + gzip</strong><small>Enabled by the recommended template.</small></div>
			<div class="metric"><span>Access log</span><strong>Enabled</strong><small>Useful for diagnostics and CrowdSec.</small></div>
			<div class="metric"><span>Proxy target</span><strong>127.0.0.1</strong><small>Applications stay off the public network.</small></div>
		</section>
		<section class="panel" style="margin-bottom:16px">
			<table>
				<thead><tr><th>Domain</th><th>App</th><th>Target</th></tr></thead>
				<tbody>` + rows.String() + `</tbody>
			</table>
		</section>
		<section class="panel panel-pad" style="margin-bottom:16px">
			<div class="section-title">
				<div>
					<h2>Site template</h2>
					<p class="note" style="margin:6px 0 0">Applied to every connected app. Required placeholders: <code>{domain}</code> and <code>{port}</code>.</p>
				</div>
				<form method="post" action="/caddy/template/recommended">
					<button class="secondary">Recommended template</button>
				</form>
			</div>
			<form method="post" action="/caddy/template">
				<textarea class="codearea" name="template" spellcheck="false" style="min-height:220px">` + html.EscapeString(template) + `</textarea>
				<div class="actions" style="justify-content:flex-start;margin-top:12px">
					<button class="button">Validate & apply template</button>
				</div>
			</form>
			<p class="note" style="margin:10px 0 0">Recommended template keeps HTTPS automatic, enables compression and access logging, and proxies only to the app&apos;s local port.</p>
		</section>
		<section class="panel panel-pad">
			<div class="section-title"><div><h2>Generated Caddyfile</h2><p class="note">Managed by Open Go Panel. Changes are validated before reload.</p></div></div>
			<pre class="security-output">` + html.EscapeString(config) + `</pre>
		</section>
	</main>
</body></html>`
}


func appDomainBlock(cfg Config, app panelapp.App) string {
	if cfg.Caddy == nil || app.Port == 0 {
		return ""
	}
	site, ok, err := cfg.Caddy.SiteForApp(app.ID)
	if err != nil {
		return `<div class="alert" style="margin-top:18px">` + html.EscapeString(err.Error()) + `</div>`
	}
	if ok {
		return `
			<div style="margin-top:20px;padding-top:18px;border-top:1px solid var(--border)">
				<div class="section-title" style="margin-bottom:10px">
					<div>
						<label style="margin:0">Domain</label>
						<a href="https://` + html.EscapeString(site.Domain) + `" target="_blank" rel="noopener"><strong>` + html.EscapeString(site.Domain) + `</strong></a>
					</div>
					<span class="status-badge ok">connected</span>
				</div>
				<p class="note" style="margin:0 0 10px">HTTPS is handled by Caddy and proxied to <code>` + html.EscapeString(site.Target()) + `</code>.</p>
				<div class="actions" style="justify-content:flex-start">
					<a class="secondary" href="https://` + html.EscapeString(site.Domain) + `" target="_blank" rel="noopener">Open site</a>
					<a class="secondary" href="/caddy">Caddy settings</a>
					<details>
						<summary class="secondary">Change domain</summary>
						<form method="post" action="/apps/` + fmt.Sprintf("%d", app.ID) + `/domain" class="inline-popover wide">
							<label>New domain</label>
							<input name="domain" value="` + html.EscapeString(site.Domain) + `" required>
							<button class="button">Save domain</button>
						</form>
					</details>
					<form method="post" action="/apps/` + fmt.Sprintf("%d", app.ID) + `/domain/delete"><button class="danger">Disconnect</button></form>
				</div>
			</div>`
	}
	return `
		<div style="margin-top:20px;padding-top:18px;border-top:1px solid var(--border)">
			<label>Domain</label>
			<form method="post" action="/apps/` + fmt.Sprintf("%d", app.ID) + `/domain" class="compact-form">
				<input name="domain" placeholder="example.com" required>
				<button class="button">Connect domain</button>
			</form>
			<p class="note" style="margin:8px 0 0">Point the domain DNS to this server. Caddy will request and renew HTTPS automatically.</p>
		</div>`
}
