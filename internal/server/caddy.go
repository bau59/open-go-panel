package server

import (
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"

	panelcaddy "github.com/bau59/open-go-panel/internal/caddy"
)

func registerCaddyRoutes(mux *http.ServeMux, store *sessionStore, cfg Config) {
	mux.Handle("GET /caddy", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sites, _ := cfg.Caddy.Sites()
		config, _ := cfg.Caddy.Config()
		writeHTML(w, cfg.Logger, http.StatusOK, caddyPage(cfg.Caddy.Status(r.Context()), sites, config, ""))
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

func caddyPage(status string, sites []panelcaddy.Site, config, message string) string {
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
		<section class="panel" style="margin-bottom:16px">
			<table>
				<thead><tr><th>Domain</th><th>App</th><th>Target</th></tr></thead>
				<tbody>` + rows.String() + `</tbody>
			</table>
		</section>
		<section class="panel panel-pad">
			<div class="section-title"><div><h2>Generated Caddyfile</h2><p class="note">Managed by Open Go Panel. Changes are validated before reload.</p></div></div>
			<pre class="security-output">` + html.EscapeString(config) + `</pre>
		</section>
	</main>
</body></html>`
}
