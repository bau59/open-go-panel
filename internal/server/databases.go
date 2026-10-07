package server

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/bau59/open-go-panel/internal/adminer"
	"github.com/bau59/open-go-panel/internal/dbmanager"
	"github.com/bau59/open-go-panel/internal/systeminfo"
)

func registerDatabaseRoutes(mux *http.ServeMux, store *sessionStore, cfg Config) {
	mux.Handle("GET /databases", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		items, _ := cfg.Databases.List()
		mysqlConfig, _ := cfg.Databases.MySQLConfig()
		info, _ := systeminfo.Read()
		recommended := cfg.Databases.RecommendedMySQLConfig(info.MemoryTotal, info.CPUs)
		writeHTML(w, cfg.Logger, http.StatusOK, databasesPage(cfg.Databases.Status(r.Context()), cfg.Adminer.Status(r.Context()), items, mysqlConfig, recommended, ""))
	})))

	mux.Handle("POST /databases/install", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		engine := strings.TrimSpace(r.FormValue("engine"))
		if err := cfg.Databases.Install(r.Context(), engine); err != nil {
			items, _ := cfg.Databases.List()
			writeHTML(w, cfg.Logger, http.StatusBadRequest, databasesPage(cfg.Databases.Status(r.Context()), cfg.Adminer.Status(r.Context()), items, mustMySQLConfig(cfg), recommendedMySQLConfig(cfg), err.Error()))
			return
		}
		http.Redirect(w, r, "/databases", http.StatusSeeOther)
	})))

	mux.Handle("POST /databases", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		_, err := cfg.Databases.Create(
			r.Context(),
			strings.TrimSpace(r.FormValue("engine")),
			strings.TrimSpace(r.FormValue("name")),
			strings.TrimSpace(r.FormValue("user")),
		)
		if err != nil {
			items, _ := cfg.Databases.List()
			writeHTML(w, cfg.Logger, http.StatusBadRequest, databasesPage(cfg.Databases.Status(r.Context()), cfg.Adminer.Status(r.Context()), items, mustMySQLConfig(cfg), recommendedMySQLConfig(cfg), err.Error()))
			return
		}
		http.Redirect(w, r, "/databases", http.StatusSeeOther)
	})))

	mux.Handle("POST /databases/mysql/config", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if err := cfg.Databases.ApplyMySQLConfig(r.Context(), r.FormValue("config")); err != nil {
			items, _ := cfg.Databases.List()
			mysqlConfig, _ := cfg.Databases.MySQLConfig()
			info, _ := systeminfo.Read()
			recommended := cfg.Databases.RecommendedMySQLConfig(info.MemoryTotal, info.CPUs)
			writeHTML(w, cfg.Logger, http.StatusBadRequest, databasesPage(cfg.Databases.Status(r.Context()), cfg.Adminer.Status(r.Context()), items, mysqlConfig, recommended, err.Error()))
			return
		}
		http.Redirect(w, r, "/databases", http.StatusSeeOther)
	})))

	mux.Handle("POST /databases/mysql/config/recommended", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info, _ := systeminfo.Read()
		config := cfg.Databases.RecommendedMySQLConfig(info.MemoryTotal, info.CPUs)
		if err := cfg.Databases.ApplyMySQLConfig(r.Context(), config); err != nil {
			items, _ := cfg.Databases.List()
			current, _ := cfg.Databases.MySQLConfig()
			writeHTML(w, cfg.Logger, http.StatusBadRequest, databasesPage(cfg.Databases.Status(r.Context()), cfg.Adminer.Status(r.Context()), items, current, config, err.Error()))
			return
		}
		http.Redirect(w, r, "/databases", http.StatusSeeOther)
	})))

	mux.Handle("POST /databases/{id}/delete", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid database id", http.StatusBadRequest)
			return
		}
		if err := cfg.Databases.Delete(r.Context(), id); err != nil {
			items, _ := cfg.Databases.List()
			writeHTML(w, cfg.Logger, http.StatusBadRequest, databasesPage(cfg.Databases.Status(r.Context()), cfg.Adminer.Status(r.Context()), items, mustMySQLConfig(cfg), recommendedMySQLConfig(cfg), err.Error()))
			return
		}
		http.Redirect(w, r, "/databases", http.StatusSeeOther)
	})))
}

func databasesPage(status dbmanager.Status, adminerStatus adminer.Status, items []dbmanager.Database, mysqlConfig, recommendedMySQL, message string) string {
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

	installMySQL := ""
	if !status.MySQLInstalled {
		installMySQL = `<form method="post" action="/databases/install"><input type="hidden" name="engine" value="mysql"><button class="secondary">Install MySQL</button></form>`
	}
	installPostgres := ""
	if !status.PostgresInstalled {
		installPostgres = `<form method="post" action="/databases/install"><input type="hidden" name="engine" value="postgres"><button class="secondary">Install PostgreSQL</button></form>`
	}

	adminerControls := ""
	if !adminerStatus.Installed {
		adminerControls = `<form method="post" action="/adminer/install"><button class="button">Install Adminer</button></form>`
	} else if adminerStatus.Active {
		adminerControls = `<a class="button" href="/db-admin/" target="_blank" rel="noopener">Open Adminer</a>
			<form method="post" action="/adminer/update"><button class="secondary">Update</button></form>
			<form method="post" action="/adminer/stop"><button class="secondary">Stop</button></form>`
	} else {
		adminerControls = `<form method="post" action="/adminer/start"><button class="button">Start Adminer</button></form>
			<form method="post" action="/adminer/update"><button class="secondary">Update</button></form>`
	}

	var rows strings.Builder
	for _, item := range items {
		adminerURL := "/db-admin/"
		if item.Engine == "mysql" {
			adminerURL += "?server=" + url.QueryEscape("127.0.0.1") + "&username=" + url.QueryEscape(item.User) + "&db=" + url.QueryEscape(item.Name)
		} else if item.Engine == "postgres" {
			adminerURL += "?pgsql=" + url.QueryEscape("127.0.0.1") + "&username=" + url.QueryEscape(item.User) + "&db=" + url.QueryEscape(item.Name)
		}
		openAdminer := ""
		if adminerStatus.Active {
			openAdminer = `<a class="secondary" href="` + html.EscapeString(adminerURL) + `" target="_blank" rel="noopener">Open in Adminer</a>`
		}
		fmt.Fprintf(&rows, `
		<tr>
			<td><strong>%s</strong><div class="muted">#%d</div></td>
			<td><span class="badge">%s</span></td>
			<td><code>%s</code></td>
			<td><code>%s</code></td>
			<td>
				<div class="actions">
					` + openAdminer + `
					<details>
						<summary class="secondary">Connection</summary>
						<div class="inline-popover wide"><code style="word-break:break-all">%s</code></div>
					</details>
					<form method="post" action="/databases/%d/delete" onsubmit="return confirm('Delete database and its user?')">
						<button class="danger">Delete</button>
					</form>
				</div>
			</td>
		</tr>`,
			html.EscapeString(item.Name),
			item.ID,
			html.EscapeString(item.Engine),
			html.EscapeString(item.User),
			html.EscapeString(item.Password),
			html.EscapeString(item.DSN()),
			item.ID,
		)
	}
	if rows.Len() == 0 {
		rows.WriteString(`<tr><td colspan="5" class="empty">No managed databases yet.</td></tr>`)
	}

	return pageHead("Databases") + `<body>` + appHeader("databases") + `
	<main class="shell">
		<div class="page-head">
			<div>
				<p class="eyebrow">Data</p>
				<h1>Databases</h1>
				<p class="sub">Local MySQL and PostgreSQL instances managed through native Ubuntu services.</p>
			</div>
		</div>

		` + alert + `

		<section class="metrics-grid" style="margin-bottom:16px">
			<div class="metric"><span>MySQL</span><strong>3306</strong><small>` + badge(status.MySQLActive) + `</small>` + installMySQL + `</div>
			<div class="metric"><span>PostgreSQL</span><strong>5432</strong><small>` + badge(status.PostgresActive) + `</small>` + installPostgres + `</div>
			<div class="metric"><span>Managed databases</span><strong>` + fmt.Sprintf("%d", len(items)) + `</strong><small>Created by Open Go Panel</small></div>
			<div class="metric"><span>Network</span><strong>localhost</strong><small>Not exposed publicly by default</small></div>
		</section>


		` + func() string {
			if !status.MySQLInstalled {
				return ""
			}
			current := mysqlConfig
			if strings.TrimSpace(current) == "" {
				current = recommendedMySQL
			}
			return `
		<section class="panel panel-pad" style="margin-bottom:16px">
			<div class="section-title">
				<div>
					<h2>MySQL server settings</h2>
					<p class="note" style="margin:6px 0 0">Managed file: <code>/etc/mysql/mysql.conf.d/99-open-go-panel.cnf</code>. Changes are validated before MySQL restart.</p>
				</div>
			</div>
			<div class="actions" style="justify-content:flex-start;margin-bottom:12px">
				<form method="post" action="/databases/mysql/config/recommended" onsubmit="return confirm('Apply recommended MySQL settings and restart MySQL?')">
					<button class="secondary">Apply recommended for this server</button>
				</form>
			</div>
			<form method="post" action="/databases/mysql/config">
				<textarea class="codearea" name="config" spellcheck="false" style="min-height:360px">` + html.EscapeString(current) + `</textarea>
				<div class="actions" style="justify-content:flex-start;margin-top:12px">
					<button class="button">Validate & restart MySQL</button>
				</div>
			</form>
			<p class="note" style="margin:10px 0 0">The preset is conservative for a shared server: local bind, InnoDB buffer pool sized from RAM, bounded connections, slow query log and safe durability.</p>
		</section>`
		}() + `

		<section class="panel panel-pad" style="margin-bottom:16px">
			<div class="section-title">
				<div>
					<h2>Adminer</h2>
					<p class="note" style="margin:6px 0 0">Database GUI runs only on 127.0.0.1:8787 and is proxied through the authenticated panel route.</p>
				</div>
				` + badge(adminerStatus.Active) + `
			</div>
			<div class="actions" style="justify-content:flex-start">` + adminerControls + `</div>
		</section>

		<section class="panel" style="margin-bottom:16px">
			<form method="post" action="/databases" class="toolbar" style="grid-template-columns:1fr 1fr 1fr auto">
				<select name="engine" required>
					<option value="mysql">MySQL</option>
					<option value="postgres">PostgreSQL</option>
				</select>
				<input name="name" placeholder="Database name" pattern="[A-Za-z][A-Za-z0-9_]{0,62}" required>
				<input name="user" placeholder="User (optional)" pattern="[A-Za-z][A-Za-z0-9_]{0,62}">
				<button class="button">Create database</button>
			</form>
			<table>
				<thead><tr><th>Database</th><th>Engine</th><th>User</th><th>Password</th><th></th></tr></thead>
				<tbody>` + rows.String() + `</tbody>
			</table>
		</section>

		<section class="panel panel-pad">
			<div class="section-title"><h2>Access model</h2></div>
			<p class="sub" style="margin:0">Databases listen locally by default. Open Go Panel creates a dedicated database user per database and does not expose MySQL or PostgreSQL through the firewall.</p>
		</section>
	</main>
</body></html>`
}


func mustMySQLConfig(cfg Config) string {
	if cfg.Databases == nil {
		return ""
	}
	v, _ := cfg.Databases.MySQLConfig()
	return v
}

func recommendedMySQLConfig(cfg Config) string {
	if cfg.Databases == nil {
		return ""
	}
	info, _ := systeminfo.Read()
	return cfg.Databases.RecommendedMySQLConfig(info.MemoryTotal, info.CPUs)
}
