package server

import (
	"fmt"
	"errors"
	"html"
	"net/http"
	"net/url"
	"mime/multipart"
	"os/exec"
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
	PostgresMetrics  dbmanager.PostgresMetrics
	RedisMetrics     dbmanager.RedisMetrics
	MySQLSizes       map[string]int64
	PostgresSizes    map[string]int64
	Backups          map[int64][]dbmanager.Backup
	Schedule         dbmanager.BackupSchedule
	BackupRemote     string
	Offsite          dbmanager.OffsiteConnection
	OffsiteSchedule  dbmanager.OffsiteSchedule
	BackupTask       dbmanager.BackupTask
	LastLocalDate    string
	LastOffsiteDate  string
	OffsiteSuccess   string
	ImportTask       dbmanager.RemoteImportTask
	Message          string
}

func registerDatabaseRoutes(mux *http.ServeMux, store *sessionStore, cfg Config) {
	registerDatabaseSlowLogRoutes(mux, store, cfg)
	mux.Handle("GET /databases", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeDatabasesPage(w, r, cfg, http.StatusOK, "")
	})))

	mux.Handle("GET /databases/mysql/logs", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !cfg.Databases.Status(r.Context()).MySQLInstalled {
			http.NotFound(w, r)
			return
		}
		cmd := exec.CommandContext(r.Context(), "journalctl", "-u", "mysql.service", "-n", "200", "--no-pager", "--output=short-iso")
		output, err := cmd.CombinedOutput()
		logs := string(output)
		if err != nil {
			logs = "Unable to read MySQL journal: " + err.Error() + "\n" + logs
		}
		if strings.TrimSpace(logs) == "" {
			logs = "No journal entries available for mysql.service."
		}
		writeHTML(w, cfg.Logger, http.StatusOK, pageHead("MySQL logs")+ `<body>` + appHeader("databases") + `
		<main class="shell">
			<div class="page-head"><div><p class="eyebrow">Data / MySQL</p><h1>MySQL journal</h1><p class="sub">Last 200 systemd journal entries for mysql.service.</p></div>
			<div class="actions"><a class="secondary" href="/databases">Back to databases</a><a class="secondary" href="/databases/mysql/logs">Refresh</a></div></div>
			<section class="panel panel-pad"><pre style="white-space:pre-wrap;overflow-wrap:anywhere;max-height:70vh;overflow:auto">` + html.EscapeString(logs) + `</pre></section>
		</main></body></html>`)
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


	mux.Handle("GET /databases/backups/task", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		writeHTML(w, cfg.Logger, http.StatusOK, databaseBackupTaskHTML(cfg.Databases.CurrentBackupTask()))
	})))

	mux.Handle("POST /databases/backups/offsite/connection", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil { http.Error(w, "invalid form", http.StatusBadRequest); return }
		port, err := strconv.Atoi(r.FormValue("port"))
		if err != nil { port = 22 }
		conn := dbmanager.OffsiteConnection{
			Provider: r.FormValue("provider"), Destination: r.FormValue("destination"),
			Endpoint: r.FormValue("endpoint"), Region: r.FormValue("region"),
			AccessKey: r.FormValue("access_key"), SecretKey: r.FormValue("secret_key"),
			Host: r.FormValue("host"), Port: port, User: r.FormValue("user"),
			KeyFile: r.FormValue("key_file"), KnownHostsFile: r.FormValue("known_hosts_file"),
		}
		if err := cfg.Databases.SaveOffsiteConnection(conn); err != nil {
			writeDatabasesPage(w, r, cfg, http.StatusBadRequest, err.Error()); return
		}
		http.Redirect(w, r, "/databases?notice=External+destination+saved", http.StatusSeeOther)
	})))

	mux.Handle("POST /databases/backups/offsite/schedule", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil { http.Error(w, "invalid form", http.StatusBadRequest); return }
		hour, err := strconv.Atoi(r.FormValue("hour_utc"))
		if err != nil { writeDatabasesPage(w, r, cfg, http.StatusBadRequest, "invalid external upload hour"); return }
		if err := cfg.Databases.SetOffsiteSchedule(r.FormValue("enabled") == "1", hour); err != nil {
			writeDatabasesPage(w, r, cfg, http.StatusBadRequest, err.Error()); return
		}
		http.Redirect(w, r, "/databases?notice=External+schedule+saved", http.StatusSeeOther)
	})))

	mux.Handle("POST /databases/backups/offsite/test", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := cfg.Databases.StartOffsiteConnectionTest(); err != nil {
			writeDatabasesPage(w, r, cfg, http.StatusBadRequest, err.Error()); return
		}
		http.Redirect(w, r, "/databases", http.StatusSeeOther)
	})))

	mux.Handle("POST /databases/backups/offsite/run", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := cfg.Databases.StartOffsiteUploadLatest(); err != nil {
			writeDatabasesPage(w, r, cfg, http.StatusBadRequest, err.Error()); return
		}
		http.Redirect(w, r, "/databases", http.StatusSeeOther)
	})))

	mux.Handle("POST /databases/{id}/backup/export", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 { http.Error(w, "invalid database id", http.StatusBadRequest); return }
		if err := r.ParseForm(); err != nil { http.Error(w, "invalid form", http.StatusBadRequest); return }
		if err := cfg.Databases.StartOffsiteUploadOne(id, r.FormValue("path")); err != nil {
			writeDatabasesPage(w, r, cfg, http.StatusBadRequest, err.Error()); return
		}
		http.Redirect(w, r, "/databases", http.StatusSeeOther)
	})))

	mux.Handle("POST /databases/backups/remote", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if err := cfg.Databases.SetBackupRemote(r.FormValue("remote")); err != nil {
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
		schedule := cfg.Databases.BackupSchedule()
		if err := cfg.Databases.StartLocalBackup(id, schedule.Keep); err != nil {
			writeDatabasesPage(w, r, cfg, http.StatusBadRequest, err.Error())
			return
		}
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
		if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
			http.Error(w, "cannot extend backup download deadline", http.StatusServiceUnavailable); return
		}
		w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(path)+`"`)
		w.Header().Set("Content-Type", "application/gzip")
		http.ServeFile(w, r, path)
	})))

	mux.Handle("POST /databases/{id}/backup/delete", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid database id", http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if err := cfg.Databases.DeleteBackup(id, r.FormValue("path")); err != nil {
			writeDatabasesPage(w, r, cfg, http.StatusBadRequest, err.Error())
			return
		}
		http.Redirect(w, r, "/databases", http.StatusSeeOther)
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

	mux.Handle("POST /databases/{engine}/restart", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		engine := strings.TrimSpace(r.PathValue("engine"))
		if err := cfg.Databases.RestartEngine(r.Context(), engine); err != nil {
			writeDatabasesPage(w, r, cfg, http.StatusBadRequest, err.Error())
			return
		}
		http.Redirect(w, r, "/databases", http.StatusSeeOther)
	})))

	mux.Handle("POST /databases/{id}/import", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid database id", http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if err := cfg.Databases.StartRemoteImport(id, r.FormValue("connection")); err != nil {
			writeDatabasesPage(w, r, cfg, http.StatusBadRequest, err.Error())
			return
		}
		http.Redirect(w, r, "/databases", http.StatusSeeOther)
	})))


	mux.Handle("POST /databases/{id}/import/file", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, "invalid database id", http.StatusBadRequest)
			return
		}
		var file multipart.File
		var header *multipart.FileHeader
		file, header, err = r.FormFile("file")
		if err != nil {
			writeDatabasesPage(w, r, cfg, http.StatusBadRequest, "select a SQL file")
			return
		}
		defer file.Close()
		if header.Size > dbmanager.MaxSQLUploadBytes {
			writeDatabasesPage(w, r, cfg, http.StatusBadRequest, "SQL file exceeds 1 GiB")
			return
		}
		mode := r.FormValue("mode")
		if mode != "apply" && mode != "replace" {
			writeDatabasesPage(w, r, cfg, http.StatusBadRequest, "invalid SQL import mode")
			return
		}
		if err := cfg.Databases.StartFileImport(id, header.Filename, file, mode == "replace"); err != nil {
			writeDatabasesPage(w, r, cfg, http.StatusBadRequest, err.Error())
			return
		}
		http.Redirect(w, r, "/databases", http.StatusSeeOther)
	})))

	mux.Handle("POST /databases/import/dismiss", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg.Databases.ClearImportTask()
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


	registerRedisRoutes(mux, store, cfg)
}

func writeDatabasesPage(w http.ResponseWriter, r *http.Request, cfg Config, statusCode int, message string) {
	data := loadDatabasePageData(r, cfg, message)
	writeHTML(w, cfg.Logger, statusCode, databasesPage(data))
}

func loadDatabasePageData(r *http.Request, cfg Config, message string) databasePageData {
	data := databasePageData{
		Status:     cfg.Databases.Status(r.Context()),
		Adminer:    cfg.Adminer.Status(r.Context()),
		MySQLSizes:    make(map[string]int64),
		PostgresSizes: make(map[string]int64),
		Backups:       make(map[int64][]dbmanager.Backup),
		Schedule:   cfg.Databases.BackupSchedule(),
		BackupRemote: cfg.Databases.BackupRemote(),
		Offsite: cfg.Databases.OffsiteConnection(),
		OffsiteSchedule: cfg.Databases.OffsiteSchedule(),
		BackupTask: cfg.Databases.CurrentBackupTask(),
		LastLocalDate: cfg.Databases.LastBackupDate(),
		LastOffsiteDate: cfg.Databases.LastOffsiteDate(),
		OffsiteSuccess: cfg.Databases.OffsiteCompletedAt(),
		ImportTask: cfg.Databases.ImportTask(),
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
	if data.Status.PostgresActive {
		data.PostgresMetrics, _ = cfg.Databases.PostgresMetrics(r.Context())
		sizes, _ := cfg.Databases.PostgresDatabaseSizes(r.Context())
		for _, size := range sizes {
			data.PostgresSizes[size.Name] = size.Bytes
		}
	}
	if data.Status.RedisActive {
		data.RedisMetrics, _ = cfg.Databases.RedisMetrics(r.Context())
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

	stateText := func(ok bool) string {
		if ok {
			return `<span class="state-text ok"><i></i>active</span>`
		}
		return `<span class="state-text warn"><i></i>inactive</span>`
	}

	installMySQL := ""
	if !data.Status.MySQLInstalled {
		installMySQL = `<form method="post" action="/databases/install"><input type="hidden" name="engine" value="mysql"><button class="secondary">Install MySQL</button></form>`
	}
	installPostgres := ""
	if !data.Status.PostgresInstalled {
		installPostgres = `<form method="post" action="/databases/install"><input type="hidden" name="engine" value="postgres"><button class="secondary">Install PostgreSQL</button></form>`
	}
	installRedis := ""
	if !data.Status.RedisInstalled {
		installRedis = `<form method="post" action="/databases/install"><input type="hidden" name="engine" value="redis"><button class="secondary">Install Redis</button></form>`
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

	engineControls := func(engine string, installed, active bool, installHTML string) string {
		if !installed {
			return `<div class="db-service-footer">` + installHTML + `</div>`
		}
		parts := `<form method="post" action="/databases/` + engine + `/restart" onsubmit="return confirm('Restart ` + engine + `? Active connections may be interrupted.')"><button class="secondary">Restart</button></form>`
		if engine == "mysql" && active {
			parts = `<a class="secondary" href="/databases/mysql/logs">Logs</a>` + parts
		}
		if engine == "redis" && active {
			parts = `<a class="secondary" href="/databases/redis">Open Redis</a>` + parts
		}
		return `<div class="db-service-footer"><div class="actions db-service-actions">` + parts + `</div></div>`
	}

	importNotice := ""
	importKind := "Remote database"
	if data.ImportTask.SourceKind == "file" { importKind = "SQL file" }
	if data.ImportTask.Running {
		importNotice = `<div class="software-task" style="margin-bottom:16px"><span class="status-badge warn">importing</span><div><strong>` + importKind + ` import into database #` + fmt.Sprintf("%d", data.ImportTask.DatabaseID) + ` · ` + html.EscapeString(data.ImportTask.SourceHost) + `</strong><p class="note">A safety backup is created before changes. Import runs in the background.</p></div></div><script>(function(){var route=location.pathname+location.search;setTimeout(function(){if(location.pathname+location.search!==route)return;if(window.htmx){window.htmx.ajax('GET',route,{target:'body',swap:'innerHTML'});}else{location.reload();}},4000);})()</script>`
	} else if data.ImportTask.Error != "" {
		failure := data.ImportTask.Error
		guidance := ""
		if data.ImportTask.SourceKind == "file" {
			failure = strings.TrimPrefix(failure, "SQL file import failed: ")
			if strings.Contains(failure, "ERROR 1064") {
				guidance = `<p class="note">MySQL rejected the SQL syntax in the uploaded dump. Check the reported line in the source file; the panel does not rewrite SQL statements.</p>`
			}
		}
		importNotice = `<div class="alert import-status"><div><strong>` + importKind + ` import failed</strong><p>` + html.EscapeString(failure) + `</p>` + guidance + `</div><form method="post" action="/databases/import/dismiss"><button class="secondary">Dismiss</button></form></div>`
	} else if !data.ImportTask.FinishedAt.IsZero() {
		importNotice = `<div class="alert import-status success"><div><strong>` + importKind + ` import completed</strong><p>Safety backup: <code>` + html.EscapeString(filepath.Base(data.ImportTask.BackupPath)) + `</code></p></div><form method="post" action="/databases/import/dismiss"><button class="secondary">Dismiss</button></form></div>`
	}

	mysqlDetails := `<div class="db-service-facts">
		<div><span>Port</span><strong>3306</strong></div>
		<div><span>Version</span><strong>—</strong></div>
		<div><span>Uptime</span><strong>—</strong></div>
		<div><span>Connections</span><strong>—</strong></div>
	</div>`
	if data.Status.MySQLActive {
		buffer := "—"
		if data.MySQLMetrics.BufferPoolBytes > 0 {
			buffer = formatBytes(uint64(data.MySQLMetrics.BufferPoolUsed)) + " / " + formatBytes(uint64(data.MySQLMetrics.BufferPoolBytes))
		}
		mysqlDetails = `<div class="db-service-facts">
			<div><span>Port</span><strong>3306</strong></div>
			<div><span>Version</span><strong title="` + html.EscapeString(data.MySQLMetrics.Version) + `">` + html.EscapeString(data.MySQLMetrics.Version) + `</strong></div>
			<div><span>Uptime</span><strong>` + formatDuration(time.Duration(data.MySQLMetrics.UptimeSeconds)*time.Second) + `</strong></div>
			<div><span>Connections</span><strong>` + fmt.Sprintf("%d", data.MySQLMetrics.ThreadsConnected) + ` / peak ` + fmt.Sprintf("%d", data.MySQLMetrics.MaxUsedConnections) + `</strong></div>
			<div class="span-2"><span>InnoDB buffer</span><strong>` + html.EscapeString(buffer) + `</strong></div>
			<div><span>Slow queries</span><strong>` + fmt.Sprintf("%d", data.MySQLMetrics.SlowQueries) + `</strong></div>
		</div>`
	}

	postgresDetails := `<div class="db-service-facts">
		<div><span>Port</span><strong>5432</strong></div>
		<div><span>Version</span><strong>—</strong></div>
		<div><span>Uptime</span><strong>—</strong></div>
		<div><span>Connections</span><strong>—</strong></div>
	</div>`
	if data.Status.PostgresActive {
		postgresDetails = `<div class="db-service-facts">
			<div><span>Port</span><strong>5432</strong></div>
			<div><span>Version</span><strong title="` + html.EscapeString(data.PostgresMetrics.Version) + `">` + html.EscapeString(data.PostgresMetrics.Version) + `</strong></div>
			<div><span>Uptime</span><strong>` + formatDuration(time.Duration(data.PostgresMetrics.UptimeSeconds)*time.Second) + `</strong></div>
			<div><span>Connections</span><strong>` + fmt.Sprintf("%d", data.PostgresMetrics.Connections) + `</strong></div>
			<div><span>Databases</span><strong>` + fmt.Sprintf("%d", data.PostgresMetrics.Databases) + `</strong></div>
			<div><span>Total size</span><strong>` + formatBytes(uint64(maxInt64(data.PostgresMetrics.TotalBytes, 0))) + `</strong></div>
		</div>`
	}

	redisDetails := `<div class="db-service-facts">
		<div><span>Port</span><strong>6379</strong></div>
		<div><span>Version</span><strong>—</strong></div>
		<div><span>Uptime</span><strong>—</strong></div>
		<div><span>Clients</span><strong>—</strong></div>
	</div>`
	if data.Status.RedisActive {
		redisDetails = `<div class="db-service-facts">
			<div><span>Port</span><strong>6379</strong></div>
			<div><span>Version</span><strong>` + html.EscapeString(data.RedisMetrics.Version) + `</strong></div>
			<div><span>Uptime</span><strong>` + formatDuration(time.Duration(data.RedisMetrics.UptimeSeconds)*time.Second) + `</strong></div>
			<div><span>Clients</span><strong>` + fmt.Sprintf("%d", data.RedisMetrics.ConnectedClients) + `</strong></div>
			<div><span>Keys</span><strong>` + fmt.Sprintf("%d", data.RedisMetrics.TotalKeys) + `</strong></div>
			<div><span>Memory</span><strong>` + formatBytes(uint64(maxInt64(data.RedisMetrics.UsedMemoryBytes, 0))) + `</strong></div>
		</div>`
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
		switch item.Engine {
		case "mysql":
			if bytes, ok := data.MySQLSizes[item.Name]; ok {
				size = formatBytes(uint64(bytes))
			}
		case "postgres":
			if bytes, ok := data.PostgresSizes[item.Name]; ok {
				size = formatBytes(uint64(bytes))
			}
		}

		var backupRows strings.Builder
		for _, backup := range data.Backups[item.ID] {
			fmt.Fprintf(&backupRows, `
				<div class="backup-row">
					<div class="db-backup-file"><strong>%s</strong><div class="note">%s</div></div>
					<div class="actions db-backup-actions">
						<a class="secondary" href="/databases/%d/backup/download?path=%s">Download</a>
						<form method="post" action="/databases/%d/restore" onsubmit="return confirm('Restore this backup over the current database?')">
							<input type="hidden" name="path" value="%s">
							<button class="secondary">Restore</button>
						</form>
						<form method="post" action="/databases/%d/backup/delete" onsubmit="return confirm('Permanently delete this backup file? This cannot be undone.')">
							<input type="hidden" name="path" value="%s">
							<button class="danger">Delete</button>
						</form>
					</div>
				</div>`,
				html.EscapeString(filepath.Base(backup.Path)),
				html.EscapeString(formatBytes(uint64(backup.Size))),
				item.ID,
				url.QueryEscape(backup.Path),
				item.ID,
				html.EscapeString(backup.Path),
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
			<td><span class="meta-chip">%s</span></td>
			<td><code class="db-cell-text">%s</code></td>
			<td><code class="db-cell-text">%s</code></td>
			<td>
				<div class="actions db-row-actions">
					%s
					<a class="secondary" href="/databases/%d/slow-queries">Slow queries</a>
					<form method="post" action="/databases/%d/backup"><button class="secondary">Backup now</button></form>
					<details class="db-action-menu">
						<summary class="secondary">Backups</summary>
						<div class="inline-popover wide">%s</div>
					</details>
					<details class="db-action-menu">
						<summary class="secondary">Connection</summary>
						<div class="inline-popover wide"><code style="word-break:break-all">%s</code></div>
					</details>
					<details class="db-action-menu">
						<summary class="secondary">Import SQL</summary>
						<div class="inline-popover wide">
							<form class="db-sql-import-form" method="post" action="/databases/%d/import/file" enctype="multipart/form-data" onsubmit="return confirm(this.elements.mode.value === 'replace' ? 'Replace this database with the SQL file? A safety backup will be created first.' : 'Execute this SQL file against the current database? A safety backup will be created first.')">
								<label>SQL file · max 1 GiB</label>
								<input class="file-upload-input" type="file" name="file" accept=".sql,.sql.gz" required>
								<label>Import mode</label>
								<select name="mode">
									<option value="apply">Apply to existing database</option>
									<option value="replace">Replace database contents</option>
								</select>
								<p class="note">.sql or .sql.gz. Uses the database user. A safety backup is created before import; failures trigger recovery.</p>
								<button class="button">Upload &amp; import</button>
							</form>
						</div>
					</details>
					<details class="db-action-menu">
						<summary class="secondary">Import remote</summary>
						<div class="inline-popover wide">
							<form method="post" action="/databases/%d/import" onsubmit="return confirm('Replace the local database with a snapshot from the remote database? A safety backup will be created first.')">
								<label>Remote connection</label>
								<input type="password" name="connection" autocomplete="off" placeholder="%s" required>
								<p class="note" style="margin:8px 0 0">Accepts standard URLs and Go MySQL DSNs, including <code>mysql://user:pass@tcp(host:3306)/db</code>. Credentials are used once and are not stored.</p>
								<button class="button" style="margin-top:10px">Import & replace local</button>
							</form>
						</div>
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
			item.ID,
			backupRows.String(),
			html.EscapeString(item.DSN()),
			item.ID,
			item.ID,
			func() string {
				if item.Engine == "mysql" {
					return "mysql://user:password@remote-host:3306/database"
				}
				return "postgres://user:password@remote-host:5432/database?sslmode=require"
			}(),
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
				<p class="sub">MySQL, PostgreSQL and Redis with backups, migrations and server controls.</p>
			</div>
		</div>

		` + alert + importNotice + `

		<section class="db-services-grid" style="margin-bottom:16px">
			<div class="panel panel-pad db-service-card">
				<div class="db-service-head"><div><p class="eyebrow">SQL server</p><h2>MySQL</h2></div>` + stateText(data.Status.MySQLActive) + `</div>
				` + mysqlDetails + engineControls("mysql", data.Status.MySQLInstalled, data.Status.MySQLActive, installMySQL) + `
			</div>
			<div class="panel panel-pad db-service-card">
				<div class="db-service-head"><div><p class="eyebrow">SQL server</p><h2>PostgreSQL</h2></div>` + stateText(data.Status.PostgresActive) + `</div>
				` + postgresDetails + engineControls("postgres", data.Status.PostgresInstalled, data.Status.PostgresActive, installPostgres) + `
			</div>
			<div class="panel panel-pad db-service-card">
				<div class="db-service-head"><div><p class="eyebrow">Cache / key-value</p><h2>Redis</h2></div>` + stateText(data.Status.RedisActive) + `</div>
				` + redisDetails + engineControls("redis", data.Status.RedisInstalled, data.Status.RedisActive, installRedis) + `
			</div>
		</section>

		<section class="panel" style="margin-bottom:16px">
			<div class="panel-pad database-list-head">
				<div>
					<h2>Managed SQL databases</h2>
					<p class="note" style="margin:6px 0 0">Credentials, backups, Adminer, SQL uploads and one-time remote imports.</p>
				</div>
				<span class="meta-chip">` + fmt.Sprintf("%d", len(data.Items)) + ` databases</span>
			</div>
			<form method="post" action="/databases" class="toolbar toolbar-4">
				<select name="engine" required>
					<option value="mysql">MySQL</option>
					<option value="postgres">PostgreSQL</option>
				</select>
				<input name="name" placeholder="Database name" pattern="[A-Za-z][A-Za-z0-9_]{0,62}" required>
				<input name="user" placeholder="User (optional)">
				<button class="button">Create database</button>
			</form>
			<div class="db-table-scroll">
			<table class="db-managed-table">
				<thead><tr><th>Database</th><th>Engine</th><th>User</th><th>Password</th><th>Actions</th></tr></thead>
				<tbody>` + rows.String() + `</tbody>
			</table>
			</div>
		</section>

		` + databaseBackupSections(data) + `


		` + mysqlSettings + `

		<section class="panel panel-pad" style="margin-bottom:16px">
			<div class="section-title">
				<div><h2>Adminer</h2><p class="note" style="margin:6px 0 0">Runs only on 127.0.0.1:8787 and is exposed through the authenticated panel proxy.</p></div>
				` + stateText(data.Adminer.Active) + `
			</div>
			<div class="actions" style="justify-content:flex-start">` + adminerControls + `</div>
		</section>

	</main>
</body></html>`
}
