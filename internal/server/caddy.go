package server

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	panelapp "github.com/bau59/open-go-panel/internal/app"
	panelcaddy "github.com/bau59/open-go-panel/internal/caddy"
)

func registerCaddyRoutes(mux *http.ServeMux, store *sessionStore, cfg Config) {
	registerCaddyDiagnostics(mux, store, cfg)
	mux.Handle("GET /caddy", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sites, _ := cfg.Caddy.Sites()
		config, _ := cfg.Caddy.Config()
		template, _ := cfg.Caddy.Template()
		settings, _ := cfg.Caddy.GlobalSettings()
		selectedID, _ := strconv.ParseInt(r.URL.Query().Get("app"), 10, 64)
		writeHTML(w, cfg.Logger, http.StatusOK, caddyPage(cfg.Caddy.Status(r.Context()), sites, config, template, settings, selectedID, ""))
	})))

	mux.Handle("POST /caddy/domain", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if err := cfg.Caddy.AddStandaloneDomain(r.Context(), r.FormValue("domain")); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, "/caddy", http.StatusSeeOther)
	})))

	mux.Handle("POST /caddy/site/{id}/rename", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid site id", http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if err := cfg.Caddy.RenameSite(r.Context(), id, r.FormValue("domain")); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, "/caddy?app="+strconv.FormatInt(id, 10), http.StatusSeeOther)
	})))

	mux.Handle("POST /caddy/site/{id}/redirect", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id >= 0 { http.Error(w, "invalid standalone domain id", http.StatusBadRequest); return }
		if err := r.ParseForm(); err != nil { http.Error(w, "invalid request", http.StatusBadRequest); return }
		if err := cfg.Caddy.SetStandaloneRedirect(r.Context(), id, r.FormValue("destination")); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, "/caddy?app="+strconv.FormatInt(id, 10), http.StatusSeeOther)
	})))

	mux.Handle("POST /caddy/site/{id}/attach", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id >= 0 {
			http.Error(w, "invalid standalone domain id", http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		appID, err := strconv.ParseInt(r.FormValue("app_id"), 10, 64)
		if err != nil || appID <= 0 {
			http.Error(w, "invalid application id", http.StatusBadRequest)
			return
		}
		app, err := cfg.Apps.Get(appID)
		if err != nil {
			http.Error(w, "application not found", http.StatusNotFound)
			return
		}
		kind := "proxy"
		if app.Type == "static" {
			kind = "static"
		}
		if err := cfg.Caddy.AttachStandaloneDomain(r.Context(), id, appID, app.Port, app.Root, kind); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, "/caddy?app="+strconv.FormatInt(appID, 10), http.StatusSeeOther)
	})))

	mux.Handle("POST /caddy/site/{id}/remove", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id >= 0 {
			http.Error(w, "invalid standalone domain id", http.StatusBadRequest)
			return
		}
		if err := cfg.Caddy.RemoveSite(r.Context(), id); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, "/caddy", http.StatusSeeOther)
	})))

	mux.Handle("GET /caddy/logs", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sites, _ := cfg.Caddy.Sites()
		filters, journalQuery := parseLogFilters(r)
		domain := strings.TrimSpace(r.URL.Query().Get("domain"))
		kind := strings.TrimSpace(r.URL.Query().Get("kind"))
		if kind != "access" && kind != "error" {
			kind = "all"
		}
		result, err := cfg.Caddy.QueryStructuredLogs(r.Context(), panelcaddy.LogQuery{
			Domain:  domain,
			Search:  filters.Search,
			Kind:    kind,
			Since:   journalQuery.Since,
			Until:   journalQuery.Until,
			Page:    filters.Page,
			PerPage: filters.PerPage,
		})
		message := ""
		if err != nil {
			message = err.Error()
		}
		writeHTML(w, cfg.Logger, http.StatusOK, caddyLogsPage(sites, domain, kind, filters, result.Entries, result.HasNext, message))
	})))

	mux.Handle("POST /caddy/settings", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		dialTimeout, dialErr := strconv.Atoi(defaultString(r.FormValue("proxy_dial_timeout"), "0"))
		headerTimeout, headerErr := strconv.Atoi(defaultString(r.FormValue("proxy_header_timeout"), "0"))
		if dialErr != nil || headerErr != nil {
			http.Error(w, "invalid reverse proxy timeout", http.StatusBadRequest)
			return
		}
		settings := panelcaddy.GlobalSettings{
			HTTPS:                     r.FormValue("https") == "1",
			Compression:               r.FormValue("compression") == "1",
			AccessLog:                 r.FormValue("access_log") == "1",
			SecurityHeaders:           r.FormValue("security_headers") == "1",
			FrameProtection:           r.FormValue("frame_protection") == "1",
			HSTS:                      r.FormValue("hsts") == "1",
			ProxyDialTimeoutSeconds:   dialTimeout,
			ProxyHeaderTimeoutSeconds: headerTimeout,
		}
		if err := cfg.Caddy.SetGlobalSettings(r.Context(), settings); err != nil {
			sites, _ := cfg.Caddy.Sites()
			config, _ := cfg.Caddy.Config()
			template, _ := cfg.Caddy.Template()
			current, _ := cfg.Caddy.GlobalSettings()
			writeHTML(w, cfg.Logger, http.StatusBadRequest, caddyPage(cfg.Caddy.Status(r.Context()), sites, config, template, current, 0, err.Error()))
			return
		}
		http.Redirect(w, r, "/caddy", http.StatusSeeOther)
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
			settings, _ := cfg.Caddy.GlobalSettings()
			writeHTML(w, cfg.Logger, http.StatusBadRequest, caddyPage(cfg.Caddy.Status(r.Context()), sites, config, template, settings, 0, err.Error()))
			return
		}
		http.Redirect(w, r, "/caddy", http.StatusSeeOther)
	})))

	mux.Handle("POST /caddy/template/recommended", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := cfg.Caddy.ResetTemplate(r.Context()); err != nil {
			sites, _ := cfg.Caddy.Sites()
			config, _ := cfg.Caddy.Config()
			template, _ := cfg.Caddy.Template()
			settings, _ := cfg.Caddy.GlobalSettings()
			writeHTML(w, cfg.Logger, http.StatusBadRequest, caddyPage(cfg.Caddy.Status(r.Context()), sites, config, template, settings, 0, err.Error()))
			return
		}
		http.Redirect(w, r, "/caddy", http.StatusSeeOther)
	})))

	mux.Handle("POST /caddy/site/{id}/static", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 { http.Error(w, "invalid app id", http.StatusBadRequest); return }
		if err := r.ParseForm(); err != nil { http.Error(w, "invalid form", http.StatusBadRequest); return }
		app, err := cfg.Apps.Get(id)
		if err != nil { http.Error(w, "application not found", http.StatusNotFound); return }
		if err := cfg.Caddy.SetProxyStatic(r.Context(), id, app.Root, r.FormValue("paths"), r.FormValue("replace_override") == "yes", func() int { n, _ := strconv.Atoi(r.FormValue("cache_seconds")); if r.FormValue("cache_seconds") == "" { return 3600 }; return n }()); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest); return
		}
		http.Redirect(w, r, "/caddy?app="+strconv.FormatInt(id, 10), http.StatusSeeOther)
	})))

	mux.Handle("POST /caddy/site/{id}/template", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid app id", http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if err := cfg.Caddy.SetSiteTemplate(r.Context(), id, r.FormValue("template")); err != nil {
			sites, _ := cfg.Caddy.Sites()
			config, _ := cfg.Caddy.Config()
			template, _ := cfg.Caddy.Template()
			settings, _ := cfg.Caddy.GlobalSettings()
			writeHTML(w, cfg.Logger, http.StatusBadRequest, caddyPage(cfg.Caddy.Status(r.Context()), sites, config, template, settings, id, err.Error()))
			return
		}
		http.Redirect(w, r, "/caddy?app="+strconv.FormatInt(id, 10), http.StatusSeeOther)
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
		kind := "proxy"
		if app.Type == "static" {
			kind = "static"
		} else if app.Port == 0 {
			http.Error(w, "this app has no web port", http.StatusBadRequest)
			return
		}
		if err := cfg.Caddy.SetSite(r.Context(), id, r.FormValue("domain"), app.Port, app.Root, kind); err != nil {
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

func caddyPage(status string, sites []panelcaddy.Site, config, template string, settings panelcaddy.GlobalSettings, selectedID int64, message string) string {
	alert := ""
	if message != "" {
		alert = `<div class="alert">` + html.EscapeString(message) + `</div>`
	}

	var rows strings.Builder
	for _, site := range sites {
		mode := "Global defaults"
		if strings.TrimSpace(site.Template) != "" {
			mode = "Custom"
		}
		if site.AppID < 0 && mode == "Global defaults" {
			if site.Kind == "redirect" { mode = "301 redirect" } else { mode = "Parked" }
		}
		fmt.Fprintf(&rows, `
			<tr>
				<td><a href="/caddy?app=%d"><strong>%s</strong></a></td>
				<td>%s</td>
				<td><code>%s</code></td>
				<td><span class="meta-chip">%s</span></td>
				<td><div class="actions table-actions"><a class="secondary" href="/caddy?app=%d">Settings</a></div></td>
			</tr>`,
			site.AppID,
			html.EscapeString(site.Domain),
			func() string { if site.AppID < 0 { return "Unassigned" }; return fmt.Sprintf("#%d", site.AppID) }(),
			html.EscapeString(site.Target()),
			html.EscapeString(mode),
			site.AppID,
		)
	}
	if rows.Len() == 0 {
		rows.WriteString(`<tr><td colspan="5" class="empty">No domains configured.</td></tr>`)
	}

	selectedEditor := ""
	if selectedID != 0 {
		for _, site := range sites {
			if site.AppID != selectedID {
				continue
			}
			override := strings.TrimSpace(site.Template)
			modeText := "This domain inherits the global defaults."
			buttonText := "Save custom config"
			if panelcaddy.IsManagedProxyStatic(override) { modeText = "Static routes are managed by the panel. The generated config is shown below as read-only." }
			if site.AppID < 0 {
				modeText = "This domain is independent from applications."
			}
			if override != "" && !panelcaddy.IsManagedProxyStatic(override) {
				modeText = "This domain has a custom Caddy override."
				buttonText = "Update custom config"
			}
			defaultTemplate := cfgTemplateForDisplay(site, template, settings)
			editorValue := override
			if editorValue == "" {
				editorValue = defaultTemplate
			}
			placeholderNote := `<code>{domain}</code> and <code>{port}</code>`
			if site.Kind == "parked" { placeholderNote = `<code>{domain}</code>` }
			if site.Kind == "redirect" { placeholderNote = `<code>{domain}</code> and <code>{root}</code>` }
			if site.Kind == "static" {
				placeholderNote = `<code>{domain}</code> and <code>{root}</code>`
			}
			selectedEditor = `
		<section class="panel panel-pad" style="margin-bottom:16px">
			<div class="section-title">
				<div>
					<p class="eyebrow" style="margin-bottom:6px">Domain</p>
					<h2 style="font-size:20px">` + html.EscapeString(site.Domain) + `</h2>
				</div>
				<div class="actions"><a class="secondary" href="/caddy/site/` + fmt.Sprintf("%d", site.AppID) + `/diagnostics">DNS / TLS</a><a class="secondary" href="/caddy">Close</a></div>
			</div>
			<div class="domain-summary">
				<div><span>Application</span><strong>` + func() string { if site.AppID < 0 { return "Unassigned" }; return fmt.Sprintf("#%d", site.AppID) }() + `</strong></div>
				<div><span>Type</span><strong>` + html.EscapeString(defaultString(site.Kind, "proxy")) + `</strong></div>
				<div><span>Target</span><code>` + html.EscapeString(site.Target()) + `</code></div>
				<div><span>Configuration</span><strong>` + func() string { if panelcaddy.IsManagedProxyStatic(override) { return "Managed static" }; if override != "" { return "Custom override" }; if site.AppID < 0 { if site.Kind == "redirect" { return "Managed redirect" }; return "Parked" }; return "Global defaults" }() + `</strong></div>
			</div>
			` + func() string { if site.AppID <= 0 || site.Kind == "static" { return "" }; return `
			<details class="advanced-block" style="margin-top:16px">
				<summary class="secondary">Static files from public/</summary>
				<p class="note">Serve selected URLs directly with Caddy and proxy all other requests to the application. The panel sets directory ACL automatically.</p>
				<form method="post" action="/caddy/site/` + fmt.Sprintf("%d", site.AppID) + `/static">
					<label>URL patterns (one per line)</label>
					<textarea class="codearea" name="paths" spellcheck="false" style="min-height:145px">` + html.EscapeString(func() string {
						if paths := panelcaddy.ManagedProxyStaticPaths(site.Template); paths != "" { return paths }
						return "/assets/*\n/img/*\n/uploads/*\n/favicon.ico\n/favicon-16x16.png\n/favicon-32x32.png\n/site.webmanifest\n/robots.txt"
					}()) + `</textarea>
					<label style="margin-top:12px">Browser cache policy</label>
					<select name="cache_seconds" style="max-width:300px">
						` + func() string { var b strings.Builder; for _, x := range []struct{n int; label string}{{0,"Revalidate every time"},{3600,"1 hour (recommended)"},{86400,"24 hours"}} { selected := ""; if panelcaddy.ManagedProxyStaticCache(override) == x.n { selected=" selected" }; fmt.Fprintf(&b, `<option value="%d"%s>%s</option>`,x.n,selected,html.EscapeString(x.label)) }; return b.String() }() + `
					</select>
					` + func() string { if override == "" || panelcaddy.IsManagedProxyStatic(override) { return "" }; return `<label><input type="checkbox" name="replace_override" value="yes" required> Replace existing custom Caddy config</label>` }() + `
					<p class="note">Root: application public/ directory. Cache-Control: one hour. Validate and reload automatically. Changes are validated and Caddy is reloaded.</p>
					<button class="button">` + func() string { if panelcaddy.IsManagedProxyStatic(override) { return "Save static routes" }; return "Enable static serving" }() + `</button>
				</form>
			</details>` }() + `
			<div style="margin-top:16px">
				<form method="post" action="/caddy/site/` + fmt.Sprintf("%d", site.AppID) + `/rename" class="compact-form">
					<input name="domain" value="` + html.EscapeString(site.Domain) + `" required aria-label="Domain name">
					<button class="secondary">Change domain</button>
				</form>
				` + func() string {
					if site.AppID >= 0 { return "" }
					return `<form method="post" action="/caddy/site/` + fmt.Sprintf("%d", site.AppID) + `/redirect" class="compact-form" style="margin-top:12px">
						<input name="destination" type="url" placeholder="https://example.org/" value="` + func() string { if site.Kind == "redirect" { return html.EscapeString(site.Root) }; return "" }() + `" aria-label="301 redirect URL">
						<button class="secondary">Save 301 redirect</button>
					</form>
					<p class="note">Leave redirect URL empty to return to parked mode.</p>
					<form method="post" action="/caddy/site/` + fmt.Sprintf("%d", site.AppID) + `/attach" class="compact-form" style="margin-top:12px">
						<input type="number" name="app_id" min="1" placeholder="Application ID" required>
						<button class="button">Attach to application</button>
					</form>
					<form method="post" action="/caddy/site/` + fmt.Sprintf("%d", site.AppID) + `/remove" onsubmit="return confirm('Remove standalone domain?')" style="margin-top:12px">
						<button class="danger">Remove domain</button>
					</form>`
				}() + `
			</div>
			<p class="sub" style="margin:14px 0 0">` + modeText + ` Common settings such as HTTPS, compression and access logging belong to the global Caddy configuration.</p>
			<details class="advanced-block" style="margin-top:16px"` + func() string { if override != "" && !panelcaddy.IsManagedProxyStatic(override) { return " open" }; return "" }() + `>
				<summary class="secondary">` + func() string { if panelcaddy.IsManagedProxyStatic(override) { return "Generated Caddy config (read-only)" }; return "Advanced: custom config for this domain" }() + `</summary>
				` + func() string { if !panelcaddy.IsManagedProxyStatic(override) { return "" }; return `<pre class="security-output" style="margin-top:12px;white-space:pre-wrap">` + html.EscapeString(editorValue) + `</pre>` }() + `
				` + func() string { if panelcaddy.IsManagedProxyStatic(override) { return "" }; return `
				<form method="post" action="/caddy/site/` + fmt.Sprintf("%d", site.AppID) + `/template" style="margin-top:14px">
					<textarea class="codearea" name="template" spellcheck="false" style="min-height:240px">` + html.EscapeString(editorValue) + `</textarea>
					<p class="note" style="margin:8px 0 0">Required placeholders: ` + placeholderNote + `. Empty the field and save to return to global defaults.</p>
					<div class="actions" style="justify-content:flex-start;margin-top:12px">
						<button class="button">` + buttonText + `</button>
					</div>
				</form>` }() + `
			</details>
		</section>`
			break
		}
	}

	statusClass := "warn"
	if status == "active" {
		statusClass = "ok"
	}

	switchRow := func(name, label, description string, enabled bool) string {
		return `
			<label class="setting-switch">
				<div>
					<strong>` + html.EscapeString(label) + `</strong>
					<span>` + html.EscapeString(description) + `</span>
				</div>
				<span class="switch"><input type="checkbox" name="` + html.EscapeString(name) + `" value="1"` + checked(enabled) + `><i></i></span>
			</label>`
	}

	customTemplateNote := ""
	globalSettingsConfirm := ""
	if strings.TrimSpace(template) != "" &&
		strings.TrimSpace(template) != strings.TrimSpace(panelcaddy.ManagedSiteTemplate(
			panelcaddy.Site{Kind: "proxy", Port: 1}, settings)) {
		customTemplateNote = `<p class="note" style="margin:0 0 16px"><strong>Custom global template detected.</strong> Saving these defaults will replace the custom global proxy template. Per-domain overrides will remain unchanged.</p>`
		globalSettingsConfirm = ` onsubmit="return confirm('Replace the custom global Caddy template with generated defaults?')"`
	}

	return pageHead("Caddy") + `<body>` + appHeader("caddy") + `
	<main class="shell">
		<div class="page-head">
			<div>
				<p class="eyebrow">Edge / routing</p>
				<h1>Caddy</h1>
				<p class="sub">Global web defaults and domain routing. Most projects should inherit the global settings.</p>
			</div>
			<div class="page-control-cluster">
				<div class="service-state"><span>Caddy</span><span class="state-text ` + statusClass + `"><i></i>` + html.EscapeString(status) + `</span></div>
				<a class="secondary" href="/caddy/logs">Access logs</a>
			</div>
		</div>
		` + alert + `

		<section class="panel" style="margin-bottom:16px">
			<div class="panel-pad" style="padding-bottom:10px">
				<div class="section-title" style="margin-bottom:0">
					<div><h2>Domains</h2><p class="note" style="margin:6px 0 0">Domains may be connected to apps or parked independently.</p></div>
				</div>
			</div>
			<form method="post" action="/caddy/domain" class="toolbar compact-form" style="margin-top:0">
				<input name="domain" placeholder="example.com" required aria-label="Standalone domain">
				<button class="button">Add domain</button>
			</form>
			<table>
				<thead><tr><th>Domain</th><th>App</th><th>Target</th><th>Config</th><th></th></tr></thead>
				<tbody>` + rows.String() + `</tbody>
			</table>
		</section>

		` + selectedEditor + `

		<section class="panel panel-pad" style="margin-bottom:16px">
			<div class="section-title">
				<div>
					<h2>Global defaults</h2>
					<p class="note" style="margin:6px 0 0">Defaults for domains using generated Caddy configuration. Advanced custom overrides are preserved.</p>
				</div>
			</div>
			` + customTemplateNote + `
			<form method="post" action="/caddy/settings"` + globalSettingsConfirm + `>
				<div class="settings-list">
					` + switchRow("https", "Automatic HTTPS", "Issue and renew TLS certificates automatically.", settings.HTTPS) + `
					` + switchRow("compression", "Response compression", "Enable zstd and gzip for supported clients.", settings.Compression) + `
					` + switchRow("access_log", "Access log", "Write HTTP access events for diagnostics and CrowdSec.", settings.AccessLog) + `
				</div>
				<div class="caddy-setting-section">
					<h3>HTTP security</h3>
					<p class="note">Opt-in response headers for managed sites. Custom site templates remain unchanged.</p>
					<div class="settings-list">
						` + switchRow("security_headers", "Security headers", "Add X-Content-Type-Options: nosniff and a safer Referrer-Policy.", settings.SecurityHeaders) + `
						` + switchRow("frame_protection", "Frame protection", "Set X-Frame-Options: SAMEORIGIN. Disable for sites embedded on other domains.", settings.FrameProtection) + `
						` + switchRow("hsts", "HSTS (7 days)", "Tell browsers to use HTTPS. Only enable when every managed domain has working TLS.", settings.HSTS) + `
					</div>
				</div>
				<div class="caddy-setting-section">
					<h3>Reverse proxy</h3>
					<p class="note">Defaults for app domains using the generated proxy template. Static sites are unaffected.</p>
					<div class="caddy-timeouts">
						<div>
							<label for="caddy-dial-timeout">Backend connection timeout</label>
							<select id="caddy-dial-timeout" name="proxy_dial_timeout">
								<option value="0"` + selected(strconv.Itoa(settings.ProxyDialTimeoutSeconds), "0") + `>Caddy default (3 seconds)</option>
								<option value="3"` + selected(strconv.Itoa(settings.ProxyDialTimeoutSeconds), "3") + `>3 seconds</option>
								<option value="5"` + selected(strconv.Itoa(settings.ProxyDialTimeoutSeconds), "5") + `>5 seconds</option>
								<option value="10"` + selected(strconv.Itoa(settings.ProxyDialTimeoutSeconds), "10") + `>10 seconds</option>
								<option value="30"` + selected(strconv.Itoa(settings.ProxyDialTimeoutSeconds), "30") + `>30 seconds</option>
							</select>
							<p class="note">Time allowed to open a connection to an app.</p>
						</div>
						<div>
							<label for="caddy-header-timeout">Backend response-header timeout</label>
							<select id="caddy-header-timeout" name="proxy_header_timeout">
								<option value="0"` + selected(strconv.Itoa(settings.ProxyHeaderTimeoutSeconds), "0") + `>Caddy default (no limit)</option>
								<option value="10"` + selected(strconv.Itoa(settings.ProxyHeaderTimeoutSeconds), "10") + `>10 seconds</option>
								<option value="30"` + selected(strconv.Itoa(settings.ProxyHeaderTimeoutSeconds), "30") + `>30 seconds</option>
								<option value="60"` + selected(strconv.Itoa(settings.ProxyHeaderTimeoutSeconds), "60") + `>60 seconds</option>
								<option value="120"` + selected(strconv.Itoa(settings.ProxyHeaderTimeoutSeconds), "120") + `>120 seconds</option>
							</select>
							<p class="note">Maximum wait for response headers; not the entire request duration.</p>
						</div>
					</div>
				</div>
				<p class="note" style="margin-top:16px">Saving regenerates the global managed proxy template. Individual custom Caddy overrides are preserved.</p>
				<div class="actions" style="justify-content:flex-start;margin-top:16px"><button class="button">Save global settings</button></div>
			</form>
		</section>



		<details class="panel panel-pad advanced-block" style="margin-bottom:16px">
			<summary class="section-title" style="margin:0;cursor:pointer">
				<div><h2>Advanced global template</h2><p class="note" style="margin:6px 0 0">Raw Caddy template. Normally you do not need to edit this.</p></div>
				<span class="secondary">Open</span>
			</summary>
			<div style="margin-top:18px">
				<form method="post" action="/caddy/template">
					<textarea class="codearea" name="template" spellcheck="false" style="min-height:220px">` + html.EscapeString(template) + `</textarea>
					<div class="actions" style="justify-content:flex-start;margin-top:12px">
						<button class="button">Validate & apply raw template</button>
						<button class="secondary" type="submit" formaction="/caddy/template/recommended">Reset recommended</button>
					</div>
				</form>
			</div>
		</details>

		<details class="panel panel-pad advanced-block">
			<summary class="section-title" style="margin:0;cursor:pointer">
				<div><h2>Generated config</h2><p class="note" style="margin:6px 0 0">Read-only configuration currently managed by Open Go Panel.</p></div>
				<span class="secondary">View</span>
			</summary>
			<pre class="security-output" style="margin-top:18px">` + html.EscapeString(config) + `</pre>
		</details>
	</main>
</body></html>`
}

func cfgTemplateForDisplay(site panelcaddy.Site, template string, settings panelcaddy.GlobalSettings) string {
	if site.Kind == "static" || site.Kind == "parked" || site.Kind == "redirect" || site.Port == 0 {
		return panelcaddy.ManagedSiteTemplate(site, settings)
	}
	return template
}

func appDomainBlock(cfg Config, app panelapp.App) string {
	if cfg.Caddy == nil || (app.Port == 0 && app.Type != "static") {
		return ""
	}
	site, ok, err := cfg.Caddy.SiteForApp(app.ID)
	if err != nil {
		return `<div class="alert" style="margin-top:18px">` + html.EscapeString(err.Error()) + `</div>`
	}
	if ok {
		mode := "Global defaults"
		if strings.TrimSpace(site.Template) != "" {
			mode = "Custom Caddy config"
		}
		scheme := "https"
		routeLabel := "HTTPS"
		if settings, err := cfg.Caddy.GlobalSettings(); err == nil && !settings.HTTPS {
			scheme = "http"
			routeLabel = "HTTP"
		}
		return `
			<section class="panel panel-pad app-card">
				<div class="section-title">
					<div><h2>Domain</h2><p class="note" style="margin:6px 0 0">Public address and reverse proxy routing.</p></div>
					<span class="state-text ok"><i></i>connected</span>
				</div>
				<div class="domain-summary">
					<div><span>Domain</span><a href="` + scheme + `://` + html.EscapeString(site.Domain) + `" target="_blank" rel="noopener"><strong>` + html.EscapeString(site.Domain) + `</strong></a></div>
					<div><span>Target</span><code>` + html.EscapeString(site.Target()) + `</code></div>
					<div><span>Protocol</span><strong>` + routeLabel + `</strong></div>
					<div><span>Caddy config</span><strong>` + mode + `</strong></div>
				</div>
				<div class="actions" style="justify-content:flex-start;margin-top:14px">
					<a class="secondary" href="` + scheme + `://` + html.EscapeString(site.Domain) + `" target="_blank" rel="noopener">Open site</a>
					<a class="secondary" href="/caddy/logs?domain=` + url.QueryEscape(site.Domain) + `">Traffic logs</a>
					<a class="secondary" href="/caddy?app=` + fmt.Sprintf("%d", app.ID) + `">Domain settings</a>
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
			</section>`
	}
	return `
		<section class="panel panel-pad app-card">
			<div class="section-title">
				<div><h2>Domain</h2><p class="note" style="margin:6px 0 0">Connect a public domain. Caddy handles routing and the global web defaults.</p></div>
			</div>
			<form method="post" action="/apps/` + fmt.Sprintf("%d", app.ID) + `/domain" class="compact-form">
				<input name="domain" placeholder="example.com" required>
				<button class="button">Connect domain</button>
			</form>
		</section>`
}


func caddyLogsPage(sites []panelcaddy.Site, domain, kind string, filters logFilters, entries []panelcaddy.LogEntry, hasNext bool, message string) string {
	alert := ""
	if message != "" {
		alert = `<div class="alert">` + html.EscapeString(message) + `</div>`
	}

	var domainOptions strings.Builder
	domainOptions.WriteString(`<option value="">All domains</option>`)
	for _, site := range sites {
		selectedAttr := ""
		if site.Domain == domain {
			selectedAttr = " selected"
		}
		fmt.Fprintf(&domainOptions, `<option value="%s"%s>%s</option>`,
			html.EscapeString(site.Domain),
			selectedAttr,
			html.EscapeString(site.Domain),
		)
	}

	kindOption := func(value, label string) string {
		selectedAttr := ""
		if kind == value {
			selectedAttr = " selected"
		}
		return `<option value="` + value + `"` + selectedAttr + `>` + label + `</option>`
	}

	var rows strings.Builder
	for _, entry := range entries {
		when := "—"
		if !entry.Time.IsZero() {
			when = entry.Time.Local().Format("2006-01-02 15:04:05")
		}
		request := html.EscapeString(entry.Method+" "+entry.URI)
		if strings.TrimSpace(entry.Method) == "" {
			request = html.EscapeString(entry.URI)
		}
		statusClass := ""
		if entry.Status >= 500 {
			statusClass = " danger"
		} else if entry.Status >= 400 {
			statusClass = " warn"
		} else if entry.Status >= 200 && entry.Status < 400 {
			statusClass = " ok"
		}
		statusText := "—"
		if entry.Status > 0 {
			statusText = strconv.Itoa(entry.Status)
		}
		kindClass := ""
		if entry.Kind == "error" {
			kindClass = " warn"
		}

		details := ""
		if entry.Message != "" || entry.Raw != "" {
			details = `
				<details class="log-details">
					<summary class="secondary">Details</summary>
					<div class="inline-popover wide log-popover">
						` + func() string {
							if entry.Message == "" {
								return ""
							}
							return `<p class="log-error-message">` + html.EscapeString(entry.Message) + `</p>`
						}() + `
						<pre class="security-output" style="max-height:260px">` + html.EscapeString(entry.Raw) + `</pre>
					</div>
				</details>`
		}

		fmt.Fprintf(&rows, `
			<tr>
				<td><code>%s</code></td>
				<td><strong>%s</strong><div class="muted">%s</div></td>
				<td><code>%s</code></td>
				<td><span class="http-code%s">%s</span></td>
				<td><code>%s</code></td>
				<td>%s ms<div class="muted">%s</div></td>
				<td><span class="badge%s">%s</span></td>
				<td>%s</td>
			</tr>`,
			html.EscapeString(when),
			html.EscapeString(entry.Domain),
			html.EscapeString(entry.Protocol),
			request,
			statusClass,
			html.EscapeString(statusText),
			html.EscapeString(entry.ClientIP),
			html.EscapeString(fmt.Sprintf("%.2f", entry.DurationMS)),
			html.EscapeString(formatBytes(uint64(maxInt64(entry.Size, 0)))),
			kindClass,
			html.EscapeString(entry.Kind),
			details,
		)
	}
	if rows.Len() == 0 {
		rows.WriteString(`<tr><td colspan="8" class="empty">No Caddy HTTP events for this filter.</td></tr>`)
	}

	extra := make(url.Values)
	if domain != "" {
		extra.Set("domain", domain)
	}
	if kind != "" && kind != "all" {
		extra.Set("kind", kind)
	}

	filterValues := cloneValues(extra)
	if filters.Search != "" {
		filterValues.Set("q", filters.Search)
	}
	filterValues.Set("period", filters.Period)
	if filters.From != "" {
		filterValues.Set("from", filters.From)
	}
	if filters.To != "" {
		filterValues.Set("to", filters.To)
	}
	filterValues.Set("per_page", strconv.Itoa(filters.PerPage))

	var hidden strings.Builder
	for key, values := range filterValues {
		if key == "domain" || key == "kind" {
			continue
		}
		for _, value := range values {
			fmt.Fprintf(&hidden, `<input type="hidden" name="%s" value="%s">`, html.EscapeString(key), html.EscapeString(value))
		}
	}

	return pageHead("Caddy logs") + `<body>` + appHeader("caddy") + `
	<main class="shell">
		<div class="page-head">
			<div>
				<p class="eyebrow">HTTP traffic</p>
				<h1>Caddy logs</h1>
				<p class="sub">Structured HTTP access and reverse-proxy errors from the Caddy journal.</p>
			</div>
			<a class="secondary" href="/caddy">Caddy settings</a>
		</div>
		` + alert + `
		<section class="panel">
			<div class="panel-pad" style="padding-bottom:0">
				<form method="get" action="/caddy/logs" class="caddy-log-filters">
					` + hidden.String() + `
					<div>
						<label>Domain</label>
						<select name="domain">` + domainOptions.String() + `</select>
					</div>
					<div>
						<label>Event type</label>
						<select name="kind">
							` + kindOption("all", "Access + errors") + kindOption("access", "Access only") + kindOption("error", "Errors only") + `
						</select>
					</div>
					<div class="filter-submit"><button class="secondary">Apply</button></div>
				</form>
				` + logToolbar("/caddy/logs", filters, extra) + `
			</div>
			<div class="table-scroll">
				<table data-no-pager="1">
					<thead><tr><th>Time</th><th>Domain</th><th>Request</th><th>Status</th><th>Client IP</th><th>Duration / size</th><th>Type</th><th></th></tr></thead>
					<tbody>` + rows.String() + `</tbody>
				</table>
			</div>
			` + logPagerHTML("/caddy/logs", filters, extra, hasNext) + `
		</section>
	</main>
</body></html>`
}

func maxInt64(value, fallback int64) int64 {
	if value < fallback {
		return fallback
	}
	return value
}
