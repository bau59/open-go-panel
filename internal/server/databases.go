package server

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bau59/open-go-panel/internal/adminer"
	"github.com/bau59/open-go-panel/internal/dbmanager"
	"github.com/bau59/open-go-panel/internal/systeminfo"
)

type databasePageData struct {
	Status           dbmanager.Status
	Adminer          adminer.Status
	Items            []dbmanager.Database
	MySQLConfig      string
	RecommendedMySQL string
	MySQLMetrics     dbmanager.MySQLMetrics
	MySQLSizes       map[string]int64
	Backups          map[int64][]dbmanager.Backup
	Schedule         dbmanager.BackupSchedule
	Message          string
}

func registerDatabaseRoutes(mux *http.ServeMux, store *sessionStore, cfg Config) {
	mux.Handle("GET /databases", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeDatabasesPage(w, r, cfg, http.StatusOK, "")
	})))

	mux.Handle("POST /databases/install", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if err := cfg.Databases.Install(r.Context(), strings.TrimSpace(r.FormValue("engine"))); err != nil {
			writeDatabasesPage(w, r, cfg, http.StatusBadRequest, err.Error())
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
			writeDatabasesPage(w, r, cfg, http.StatusBadRequest, err.Error())
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
			writeDatabasesPage(w, r, cfg, http.StatusBadRequest, err.Error())
			return
		}
		http.Redirect(w, r, "/databases", http.StatusSeeOther)
	})))

	mux.Handle("POST /databases/mysql/config/recommended", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info, _ := systeminfo.Read()
		config := cfg.Databases.RecommendedMySQLConfig(info.MemoryTotal, info.CPUs)
		if err := cfg.Databases.ApplyMySQLConfig(r.Context(), config); err != nil {
			writeDatabasesPage(w, r, cfg, http.StatusBadRequest, err.Error())
			return
		}
		http.Redirect(w, r, "/databases", http.StatusSeeOther)
	})))

	mux.Handle("POST /databases/backups/schedule", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		hour, err := strconv.Atoi(r.FormValue("hour_utc"))
		if err != nil {
			writeDatabasesPage(w, r, cfg, http.StatusBadRequest, "invalid backup hour")
			return
		}
		keep, err := strconv.Atoi(r.FormValue("keep"))
		if err != nil {
			writeDatabasesPage(w, r, cfg, http.StatusBadRequest, "invalid backup retention")
			return
		}
		if err := cfg.Databases.SetBackupSchedule(r.FormValue("enabled") == "1", hour, keep); err != nil {
			writeDatabasesPage(w, r, cfg, http.StatusBadRequest, err.Error())
			return
		}
		http.Redirect(w, r, "/databases", http.StatusSeeOther)
	})))

	mux.Handle("POST /databases/{id}/backup", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid database id", http.StatusBadRequest)
			return
		}
		if _, err := cfg.Databases.Backup(r.Context(), id); err != nil {
			writeDatabasesPage(w, r, cfg, http.StatusBadRequest, err.Error())
			return
		}
		schedule := cfg.Databases.BackupSchedule()
		_ = cfg.Databases.PruneBackups(id, schedule.Keep)
		http.Redirect(w, r, "/databases", http.StatusSeeOther)
	})))

	mux.Handle("GET /databases/{id}/backup/download", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid database id", http.StatusBadRequest)
			return
		}
		path, err := cfg.Databases.BackupFile(id, r.URL.Query().Get("path"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(path)+`"`)
		w.Header().Set("Content-Type", "application/gzip")
		http.ServeFile(w, r, path)
	})))

	mux.Handle("POST /databases/{id}/restore", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid database id", http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if err := cfg.Databases.Restore(r.Context(), id, r.FormValue("path")); err != nil {
			writeDatabasesPage(w, r, cfg, http.StatusBadRequest, err.Error())
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
			writeDatabasesPage(w, r, cfg, http.StatusBadRequest, err.Error())
			return
		}
		http.Redirect(w, r, "/databases", http.StatusSeeOther)
	})))
}

func writeDatabasesPage(w http.ResponseWriter, r *http.Request, cfg Config, statusCode int, message string) {
	data := loadDatabasePageData(r, cfg, message)
	writeHTML(w, cfg.Logger, statusCode, databasesPage(data))
}

func loadDatabasePageData(r *http.Request, cfg Config, message string) databasePageData {
	data := databasePageData{
		Status:     cfg.Databases.Status(r.Context()),
		Adminer:    cfg.Adminer.Status(r.Context()),
		MySQLSizes: make(map[string]int64),
		Backups:    make(map[int64][]dbmanager.Backup),
		Schedule:   cfg.Databases.BackupSchedule(),
		Message:    message,
	}
	data.Items, _ = cfg.Databases.List()
	data.MySQLConfig, _ = cfg.Databases.MySQLConfig()
	info, _ := systeminfo.Read()
	data.RecommendedMySQL = cfg.Databases.RecommendedMySQLConfig(info.MemoryTotal, info.CPUs)

	if data.Status.MySQLActive {
		data.MySQLMetrics, _ = cfg.Databases.MySQLMetrics(r.Context())
		sizes, _ := cfg.Databases.MySQLDatabaseSizes(r.Context())
		for _, size := range sizes {
			data.MySQLSizes[size.Name] = size.Bytes
		}
	}
	for _, item := range data.Items {
		data.Backups[item.ID], _ = cfg.Databases.Backups(item.ID)
	}
	return data
}

func databasesPage(data databasePageData) string {
	alert := ""
	if data.Message != "" {
		alert = `<div class="alert">` + html.EscapeString(data.Message) + `</div>`
	}

	badge := func(ok bool) string {
		if ok {
			return `<span class="status-badge ok">active</span>`
		}
		return `<span class="status-badge warn">inactive</span>`
	}

	installMySQL := ""
	if !data.Status.MySQLInstalled {
		installMySQL = `<form method="post" action="/databases/install"><input type="hidden" name="engine" value="mysql"><button class="secondary">Install MySQL</button></form>`
	}
	installPostgres := ""
	if !data.Status.PostgresInstalled {
		installPostgres = `<form method="post" action="/databases/install"><input type="hidden" name="engine" value="postgres"><button class="secondary">Install PostgreSQL</button></form>`
	}

	adminerControls := ""
	if !data.Adminer.Installed {
		adminerControls = `<form method="post" action="/adminer/install"><button class="button">Install Adminer</button></form>`
	} else if data.Adminer.Active {
		adminerControls = `<a class="button" href="/db-admin/" target="_blank" rel="noopener">Open Adminer</a>
			<form method="post" action="/adminer/update"><button class="secondary">Update</button></form>
			<form method="post" action="/adminer/stop"><button class="secondary">Stop</button></form>`
	} else {
		adminerControls = `<form method="post" action="/adminer/start"><button class="button">Start Adminer</button></form>
			<form method="post" action="/adminer/update"><button class="secondary">Update</button></form>`
	}

	mysqlMetrics := ""
	if data.Status.MySQLActive {
		buffer := "—"
		if data.MySQLMetrics.BufferPoolBytes > 0 {
			buffer = formatBytes(uint64(data.MySQLMetrics.BufferPoolUsed)) + " / " + formatBytes(uint64(data.MySQLMetrics.BufferPoolBytes))
		}
		mysqlMetrics = `
		<section class="metrics-grid" style="margin-bottom:16px">
			<div class="metric"><span>MySQL version</span><strong>` + html.EscapeString(data.MySQLMetrics.Version) + `</strong><small>uptime ` + formatDuration(time.Duration(data.MySQLMetrics.UptimeSeconds)*time.Second) + `</small></div>
			<div class="metric"><span>Connections</span><strong>` + fmt.Sprintf("%d", data.MySQLMetrics.ThreadsConnected) + `</strong><small>peak ` + fmt.Sprintf("%d", data.MySQLMetrics.MaxUsedConnections) + `</small></div>
			<div class="metric"><span>InnoDB buffer</span><strong>` + buffer + `</strong><small>used / allocated</small></div>
			<div class="metric"><span>Slow queries</span><strong>` + fmt.Sprintf("%d", data.MySQLMetrics.SlowQueries) + `</strong><small>` + fmt.Sprintf("%d", data.MySQLMetrics.Questions) + ` total questions</small></div>
		</section>`
	}

	var rows strings.Builder
	for _, item := range data.Items {
		adminerURL := "/db-admin/"
		if item.Engine == "mysql" {
			adminerURL += "?server=" + url.QueryEscape("127.0.0.1") + "&username=" + url.QueryEscape(item.User) + "&db=" + url.QueryEscape(item.Name)
		} else if item.Engine == "postgres" {
			adminerURL += "?pgsql=" + url.QueryEscape("127.0.0.1") + "&username=" + url.QueryEscape(item.User) + "&db=" + url.QueryEscape(item.Name)
		}
		openAdminer := ""
		if data.Adminer.Active {
			openAdminer = `<a class="secondary" href="` + html.EscapeString(adminerURL) + `" target="_blank" rel="noopener">Adminer</a>`
		}

		size := "—"
		if item.Engine == "mysql" {
			if bytes, ok := data.MySQLSizes[item.Name]; ok {
				size = formatBytes(uint64(bytes))
			}
		}

		var backupRows strings.Builder
		for _, backup := range data.Backups[item.ID] {
			fmt.Fprintf(&backupRows, `
				<div class="backup-row">
					<div><strong>%s</strong><div class="note">%s</div></div>
					<div class="actions">
						<a class="secondary" href="/databases/%d/backup/download?path=%s">Download</a>
						<form method="post" action="/databases/%d/restore" onsubmit="return confirm('Restore this backup over the current database?')">
							<input type="hidden" name="path" value="%s">
							<button class="secondary">Restore</button>
						</form>
					</div>
				</div>`,
				html.EscapeString(filepath.Base(backup.Path)),
				html.EscapeString(formatBytes(uint64(backup.Size))),
				item.ID,
				url.QueryEscape(backup.Path),
				item.ID,
				html.EscapeString(backup.Path),
			)
		}
		if backupRows.Len() == 0 {
			backupRows.WriteString(`<p class="note">No backups yet.</p>`)
		}

		fmt.Fprintf(&rows, `
		<tr>
			<td><strong>%s</strong><div class="muted">#%d · %s</div></td>
			<td><span class="badge">%s</span></td>
			<td><code>%s</code></td>
			<td><code>%s</code></td>
			<td>
				<div class="actions">
					%s
					<form method="post" action="/databases/%d/backup"><button class="secondary">Backup now</button></form>
					<details>
						<summary class="secondary">Backups</summary>
						<div class="inline-popover wide">%s</div>
					</details>
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
			html.EscapeString(size),
			html.EscapeString(item.Engine),
			html.EscapeString(item.User),
			html.EscapeString(item.Password),
			openAdminer,
			item.ID,
			backupRows.String(),
			html.EscapeString(item.DSN()),
			item.ID,
		)
	}
	if rows.Len() == 0 {
		rows.WriteString(`<tr><td colspan="5" class="empty">No managed databases yet.</td></tr>`)
	}

	mysqlSettings := ""
	if data.Status.MySQLInstalled {
		current := data.MySQLConfig
		if strings.TrimSpace(current) == "" {
			current = data.RecommendedMySQL
		}
		mysqlSettings = `
		<section class="panel panel-pad" style="margin-bottom:16px">
			<div class="section-title">
				<div>
					<h2>MySQL server settings</h2>
					<p class="note" style="margin:6px 0 0">Managed file: <code>/etc/mysql/mysql.conf.d/99-open-go-panel.cnf</code>. Changes are validated before restart.</p>
				</div>
			</div>
			<div class="actions" style="justify-content:flex-start;margin-bottom:12px">
				<form method="post" action="/databases/mysql/config/recommended" onsubmit="return confirm('Apply recommended MySQL settings and restart MySQL?')">
					<button class="secondary">Apply recommended for this server</button>
				</form>
			</div>
			<form method="post" action="/databases/mysql/config">
				<textarea class="codearea" name="config" spellcheck="false" style="min-height:360px">` + html.EscapeString(current) + `</textarea>
				<div class="actions" style="justify-content:flex-start;margin-top:12px"><button class="button">Validate & restart MySQL</button></div>
			</form>
		</section>`
	}

	return pageHead("Databases") + `<body>` + appHeader("databases") + `
	<main class="shell">
		<div class="page-head">
			<div>
				<p class="eyebrow">Data</p>
				<h1>Databases</h1>
				<p class="sub">Local MySQL and PostgreSQL instances, credentials, tuning and backups.</p>
			</div>
		</div>

		` + alert + `

		<section class="metrics-grid" style="margin-bottom:16px">
			<div class="metric"><span>MySQL</span><strong>3306</strong><small>` + badge(data.Status.MySQLActive) + `</small>` + installMySQL + `</div>
			<div class="metric"><span>PostgreSQL</span><strong>5432</strong><small>` + badge(data.Status.PostgresActive) + `</small>` + installPostgres + `</div>
			<div class="metric"><span>Managed databases</span><strong>` + fmt.Sprintf("%d", len(data.Items)) + `</strong><small>tracked in panel.db</small></div>
			<div class="metric"><span>Network</span><strong>localhost</strong><small>database ports stay private</small></div>
		</section>

		` + mysqlMetrics + `

		<section class="panel panel-pad" style="margin-bottom:16px">
			<div class="section-title">
				<div><h2>Automatic backups</h2><p class="note" style="margin:6px 0 0">Runs once per day after the selected UTC hour. Old copies are pruned per database.</p></div>
			</div>
			<form method="post" action="/databases/backups/schedule" class="grid service-grid-4">
				<label class="check-row"><input type="checkbox" name="enabled" value="1"` + checked(data.Schedule.Enabled) + `><span>Enabled</span></label>
				<div><label>Hour UTC</label><input type="number" name="hour_utc" min="0" max="23" value="` + fmt.Sprintf("%d", data.Schedule.HourUTC) + `"></div>
				<div><label>Keep copies</label><input type="number" name="keep" min="1" max="100" value="` + fmt.Sprintf("%d", data.Schedule.Keep) + `"></div>
				<div style="display:flex;align-items:end"><button class="button">Save schedule</button></div>
			</form>
		</section>

		` + mysqlSettings + `

		<section class="panel panel-pad" style="margin-bottom:16px">
			<div class="section-title">
				<div><h2>Adminer</h2><p class="note" style="margin:6px 0 0">Runs only on 127.0.0.1:8787 and is exposed through the authenticated panel proxy.</p></div>
				` + badge(data.Adminer.Active) + `
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
				<input name="user" placeholder="User (optional)">
				<button class="button">Create database</button>
			</form>
			<table>
				<thead><tr><th>Database</th><th>Engine</th><th>User</th><th>Password</th><th></th></tr></thead>
				<tbody>` + rows.String() + `</tbody>
			</table>
		</section>
	</main>
</body></html>`
}
