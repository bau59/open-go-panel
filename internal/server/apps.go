package server

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	panelapp "github.com/bau59/open-go-panel/internal/app"
	"github.com/bau59/open-go-panel/internal/linuxuser"
	"github.com/bau59/open-go-panel/internal/systeminfo"
)

func registerAppRoutes(mux *http.ServeMux, store *sessionStore, cfg Config) {
	mux.Handle("GET /apps/{id}", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid app id", http.StatusBadRequest)
			return
		}

		app, err := cfg.Apps.Get(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		status := cfg.Apps.Status(r.Context(), id)
		unit, _ := cfg.Apps.Unit(id)
		writeHTML(w, cfg.Logger, http.StatusOK, appPage(app, status, unit, "", appHealthBlock(r, cfg, app), appDomainBlock(cfg, app), databaseBlock(cfg, app), deployBlock(cfg, app)))
	})))

	mux.Handle("POST /apps/{id}/database", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid app id", http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		dbID, err := strconv.ParseInt(r.FormValue("database_id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid database id", http.StatusBadRequest)
			return
		}
		if cfg.Databases == nil {
			http.Error(w, "database manager is unavailable", http.StatusServiceUnavailable)
			return
		}
		db, err := cfg.Databases.Get(dbID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		envName := strings.TrimSpace(r.FormValue("env_name"))
		if envName == "" {
			envName = "DATABASE_URL"
		}
		if err := cfg.Apps.SetEnvironmentVariable(r.Context(), id, envName, db.DSN()); err != nil {
			app, _ := cfg.Apps.Get(id)
			writeHTML(w, cfg.Logger, http.StatusBadRequest, appPage(app, cfg.Apps.Status(r.Context(), id), currentUnit(cfg, id), err.Error(), databaseBlock(cfg, app)))
			return
		}
		if err := cfg.Databases.Attach(id, db.ID, envName); err != nil {
			_ = cfg.Apps.RemoveEnvironmentVariable(r.Context(), id, envName)
			app, _ := cfg.Apps.Get(id)
			writeHTML(w, cfg.Logger, http.StatusBadRequest, appPage(app, cfg.Apps.Status(r.Context(), id), currentUnit(cfg, id), err.Error(), databaseBlock(cfg, app)))
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/apps/%d", id), http.StatusSeeOther)
	})))

	mux.Handle("POST /apps/{id}/database/detach", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid app id", http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		envName := strings.TrimSpace(r.FormValue("env_name"))
		if envName == "" {
			envName = "DATABASE_URL"
		}
		var restoreDSN string
		attachments, _ := cfg.Databases.AttachmentsForApp(id)
		for _, attachment := range attachments {
			if attachment.EnvName != envName {
				continue
			}
			if db, err := cfg.Databases.Get(attachment.DatabaseID); err == nil {
				restoreDSN = db.DSN()
			}
			break
		}

		if err := cfg.Apps.RemoveEnvironmentVariable(r.Context(), id, envName); err != nil {
			app, _ := cfg.Apps.Get(id)
			writeHTML(w, cfg.Logger, http.StatusBadRequest, appPage(app, cfg.Apps.Status(r.Context(), id), currentUnit(cfg, id), err.Error(), databaseBlock(cfg, app)))
			return
		}
		if err := cfg.Databases.Detach(id, envName); err != nil {
			if restoreDSN != "" {
				_ = cfg.Apps.SetEnvironmentVariable(r.Context(), id, envName, restoreDSN)
			}
			app, _ := cfg.Apps.Get(id)
			writeHTML(w, cfg.Logger, http.StatusBadRequest, appPage(app, cfg.Apps.Status(r.Context(), id), currentUnit(cfg, id), err.Error(), databaseBlock(cfg, app)))
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/apps/%d", id), http.StatusSeeOther)
	})))

	mux.Handle("POST /apps/{id}/port", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid app id", http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		port, err := strconv.Atoi(strings.TrimSpace(r.FormValue("port")))
		if err != nil {
			http.Error(w, "invalid application port", http.StatusBadRequest)
			return
		}
		app, err := cfg.Apps.Get(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		oldPort := app.Port
		if err := cfg.Apps.SetPort(r.Context(), id, port); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if cfg.Caddy != nil {
			if site, ok, err := cfg.Caddy.SiteForApp(id); err == nil && ok {
				updated, getErr := cfg.Apps.Get(id)
				if getErr != nil {
					_ = cfg.Apps.SetPort(context.Background(), id, oldPort)
					http.Error(w, getErr.Error(), http.StatusInternalServerError)
					return
				}
				if err := cfg.Caddy.SetSite(r.Context(), id, site.Domain, updated.Port, updated.Root, site.Kind); err != nil {
					_ = cfg.Apps.SetPort(context.Background(), id, oldPort)
					http.Error(w, "port changed but Caddy update failed and was rolled back: "+err.Error(), http.StatusBadRequest)
					return
				}
			}
		}
		http.Redirect(w, r, fmt.Sprintf("/apps/%d", id), http.StatusSeeOther)
	})))

	mux.Handle("POST /apps/{id}/delete", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid app id", http.StatusBadRequest)
			return
		}
		if cfg.Caddy != nil {
			if _, ok, _ := cfg.Caddy.SiteForApp(id); ok {
				if err := cfg.Caddy.RemoveSite(r.Context(), id); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
			}
		}
		if err := cfg.Apps.Delete(r.Context(), id); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, "/apps", http.StatusSeeOther)
	})))

	mux.Handle("POST /apps/{id}/deploy/key", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid app id", http.StatusBadRequest)
			return
		}
		if _, err := cfg.Apps.EnsureDeployKey(r.Context(), id); err != nil {
			app, _ := cfg.Apps.Get(id)
			writeHTML(w, cfg.Logger, http.StatusBadRequest, appPage(app, cfg.Apps.Status(r.Context(), id), currentUnit(cfg, id), err.Error(), deployBlock(cfg, app)))
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/apps/%d", id), http.StatusSeeOther)
	})))

	mux.Handle("POST /apps/{id}/deploy/trust-host", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid app id", http.StatusBadRequest)
			return
		}
		if _, err := cfg.Apps.TrustDeployHost(r.Context(), id); err != nil {
			app, _ := cfg.Apps.Get(id)
			writeHTML(w, cfg.Logger, http.StatusBadRequest, appPage(app, cfg.Apps.Status(r.Context(), id), currentUnit(cfg, id), err.Error(), deployBlock(cfg, app)))
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/apps/%d", id), http.StatusSeeOther)
	})))

	mux.Handle("POST /apps/{id}/deploy/config", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid app id", http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if err := cfg.Apps.SetDeployConfig(id, r.FormValue("repository"), r.FormValue("branch")); err != nil {
			app, _ := cfg.Apps.Get(id)
			writeHTML(w, cfg.Logger, http.StatusBadRequest, appPage(app, cfg.Apps.Status(r.Context(), id), currentUnit(cfg, id), err.Error(), deployBlock(cfg, app)))
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/apps/%d", id), http.StatusSeeOther)
	})))

	mux.Handle("POST /apps/{id}/deploy", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid app id", http.StatusBadRequest)
			return
		}
		if err := cfg.Apps.Deploy(r.Context(), id); err != nil {
			app, _ := cfg.Apps.Get(id)
			writeHTML(w, cfg.Logger, http.StatusBadRequest, appPage(app, cfg.Apps.Status(r.Context(), id), currentUnit(cfg, id), err.Error(), deployBlock(cfg, app)))
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/apps/%d", id), http.StatusSeeOther)
	})))

	mux.Handle("POST /apps/{id}/rollback", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid app id", http.StatusBadRequest)
			return
		}
		if err := cfg.Apps.Rollback(r.Context(), id); err != nil {
			app, _ := cfg.Apps.Get(id)
			writeHTML(w, cfg.Logger, http.StatusBadRequest, appPage(app, cfg.Apps.Status(r.Context(), id), currentUnit(cfg, id), err.Error(), deployBlock(cfg, app)))
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/apps/%d", id), http.StatusSeeOther)
	})))

	mux.Handle("POST /apps/{id}/command", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid app id", http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}

		if err := cfg.Apps.SetCommand(r.Context(), id, r.FormValue("command")); err != nil {
			app, getErr := cfg.Apps.Get(id)
			if getErr != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeHTML(w, cfg.Logger, http.StatusBadRequest, appPage(app, cfg.Apps.Status(r.Context(), id), currentUnit(cfg, id), err.Error()))
			return
		}

		http.Redirect(w, r, fmt.Sprintf("/apps/%d", id), http.StatusSeeOther)
	})))

	mux.Handle("POST /apps/{id}/service", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid app id", http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}

		restartSec, _ := strconv.Atoi(r.FormValue("restart_sec"))
		timeoutStopSec, _ := strconv.Atoi(r.FormValue("timeout_stop_sec"))
		limitNOFILE, _ := strconv.Atoi(r.FormValue("limit_nofile"))
		tasksMax, _ := strconv.Atoi(r.FormValue("tasks_max"))
		logRetentionDays, _ := strconv.Atoi(r.FormValue("log_retention_days"))
		serviceCfg := panelapp.ServiceConfig{
			Mode:        strings.TrimSpace(r.FormValue("mode")),
			RunMode:     strings.TrimSpace(r.FormValue("run_mode")),
			Command:     strings.TrimSpace(r.FormValue("command")),
			Restart:     strings.TrimSpace(r.FormValue("restart")),
			RestartSec:       restartSec,
			TimeoutStopSec:   timeoutStopSec,
			WorkingDirectory: strings.TrimSpace(r.FormValue("working_directory")),
			Path:             strings.TrimSpace(r.FormValue("path")),
			CPUQuota:    strings.TrimSpace(r.FormValue("cpu_quota")),
			MemoryMax:        strings.TrimSpace(r.FormValue("memory_max")),
			LimitNOFILE:      limitNOFILE,
			TasksMax:         tasksMax,
			LogRetentionDays: logRetentionDays,
			AutoStart:        r.FormValue("auto_start") == "1",
			Environment: r.FormValue("environment"),
			RawUnit:     r.FormValue("raw_unit"),
		}

		if err := cfg.Apps.SetServiceConfig(r.Context(), id, serviceCfg); err != nil {
			app, getErr := cfg.Apps.Get(id)
			if getErr != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			writeHTML(w, cfg.Logger, http.StatusBadRequest, appPage(app, cfg.Apps.Status(r.Context(), id), currentUnit(cfg, id), err.Error()))
			return
		}

		http.Redirect(w, r, fmt.Sprintf("/apps/%d", id), http.StatusSeeOther)
	})))


	mux.Handle("POST /apps/{id}/service/auto", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid app id", http.StatusBadRequest)
			return
		}
		app, err := cfg.Apps.Get(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		info, _ := systeminfo.Read()
		auto := recommendedServiceConfig(app, info)
		if err := cfg.Apps.SetServiceConfig(r.Context(), id, auto); err != nil {
			writeHTML(w, cfg.Logger, http.StatusBadRequest, appPage(app, cfg.Apps.Status(r.Context(), id), currentUnit(cfg, id), err.Error()))
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/apps/%d", id), http.StatusSeeOther)
	})))

	mux.Handle("GET /apps/{id}/logs", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid app id", http.StatusBadRequest)
			return
		}
		app, err := cfg.Apps.Get(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		filters, query := parseLogFilters(r)
		result, err := cfg.Apps.QueryLogs(r.Context(), id, query)
		message := ""
		if err != nil {
			message = err.Error()
		}
		writeHTML(w, cfg.Logger, http.StatusOK, appLogsPage(app, filters, result.Lines, result.HasNext, message))
	})))

	mux.Handle("GET /apps/{id}/logs/live", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid app id", http.StatusBadRequest)
			return
		}
		app, err := cfg.Apps.Get(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		writeHTML(w, cfg.Logger, http.StatusOK, appLiveLogsPage(app))
	})))

	mux.Handle("GET /apps/{id}/logs/stream", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid app id", http.StatusBadRequest)
			return
		}
		if _, err := cfg.Apps.Get(id); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		_, _ = fmt.Fprint(w, ": connected\n\n")
		flusher.Flush()

		err = cfg.Apps.StreamLogs(r.Context(), id, 0, func(line string) error {
			line = strings.ReplaceAll(strings.ReplaceAll(line, "\r", ""), "\n", "")
			if _, err := fmt.Fprintf(w, "data: %s\n\n", line); err != nil {
				return err
			}
			flusher.Flush()
			return nil
		})
		if err != nil && r.Context().Err() == nil {
			cfg.Logger.Warn("log stream stopped", "app_id", id, "err", err)
		}
	})))

	for _, action := range []struct {
		path string
		run  func(int64) error
	}{
		{"start", func(id int64) error { return cfg.Apps.Start(context.Background(), id) }},
		{"stop", func(id int64) error { return cfg.Apps.Stop(context.Background(), id) }},
		{"restart", func(id int64) error { return cfg.Apps.Restart(context.Background(), id) }},
	} {
		action := action
		mux.Handle("POST /apps/{id}/"+action.path, requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
			if err != nil {
				http.Error(w, "invalid app id", http.StatusBadRequest)
				return
			}

			if err := action.run(id); err != nil {
				app, getErr := cfg.Apps.Get(id)
				if getErr != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				writeHTML(w, cfg.Logger, http.StatusBadRequest, appPage(app, cfg.Apps.Status(r.Context(), id), currentUnit(cfg, id), err.Error()))
				return
			}

			http.Redirect(w, r, fmt.Sprintf("/apps/%d", id), http.StatusSeeOther)
		})))
	}

	mux.Handle("GET /apps/resources", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apps, err := cfg.Apps.List()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		usage := cfg.Apps.ResourceUsageMany(r.Context(), apps)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if err := json.NewEncoder(w).Encode(usage); err != nil {
			cfg.Logger.Warn("encode app resources failed", "err", err)
		}
	})))

	mux.Handle("GET /apps", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apps, err := cfg.Apps.List()
		if err != nil {
			cfg.Logger.Error("list apps failed", "err", err)
			writeHTML(w, cfg.Logger, http.StatusInternalServerError, appsPage(nil, nil, err.Error()))
			return
		}

		users, err := cfg.Users.List(r.Context())
		if err != nil {
			cfg.Logger.Error("list users for apps failed", "err", err)
			writeHTML(w, cfg.Logger, http.StatusInternalServerError, appsPage(apps, nil, err.Error()))
			return
		}

		writeHTML(w, cfg.Logger, http.StatusOK, appsPage(apps, users, ""))
	})))

	mux.Handle("POST /apps", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}

		_, err := cfg.Apps.Create(
			strings.TrimSpace(r.FormValue("user")),
			strings.TrimSpace(r.FormValue("name")),
			strings.TrimSpace(r.FormValue("type")),
		)
		if err != nil {
			apps, _ := cfg.Apps.List()
			users, _ := cfg.Users.List(r.Context())
			writeHTML(w, cfg.Logger, http.StatusBadRequest, appsPage(apps, users, err.Error()))
			return
		}

		http.Redirect(w, r, "/apps", http.StatusSeeOther)
	})))
}

func appsPage(apps []panelapp.App, users []linuxuser.User, message string) string {
	var userOptions strings.Builder
	for _, u := range users {
		fmt.Fprintf(&userOptions, `<option value="%s">%s</option>`,
			html.EscapeString(u.Username),
			html.EscapeString(u.Username),
		)
	}

	var rows strings.Builder
	for _, app := range apps {
		port := "—"
		if app.Port > 0 {
			port = fmt.Sprintf("%d", app.Port)
		}

		fmt.Fprintf(&rows, `
		<tr data-resource-app="%d">
			<td><a href="/apps/%d"><strong>%s</strong></a><div class="muted">#%d</div></td>
			<td>%s</td>
			<td><span class="badge">%s</span></td>
			<td><code>%s</code></td>
			<td><span class="resource-value" data-resource="cpu">—</span></td>
			<td><span class="resource-value" data-resource="memory">—</span></td>
			<td><span class="resource-value" data-resource="tasks">—</span></td>
			<td><code>%s</code></td>
		</tr>`,
			app.ID,
			app.ID,
			html.EscapeString(app.Name),
			app.ID,
			html.EscapeString(app.User),
			html.EscapeString(app.Type),
			port,
			html.EscapeString(app.Root),
		)
	}

	if rows.Len() == 0 {
		rows.WriteString(`<tr><td colspan="8" class="empty">No applications yet.</td></tr>`)
	}

	alert := ""
	if message != "" {
		alert = `<div class="alert">` + html.EscapeString(message) + `</div>`
	}

	disabled := ""
	hint := ""
	if len(users) == 0 {
		disabled = " disabled"
		hint = `<p class="note" style="padding:0 16px 16px">Create a Linux user first. Every application needs an owner.</p>`
	}

	return pageHead("Applications") + `<body>` + appHeader("apps") + `
	<main class="shell">
		<div class="page-head">
			<div>
				<p class="eyebrow">Workloads</p>
				<h1>Applications</h1>
				<p class="sub">Apps belong to Linux users. Web runtimes receive an internal port automatically.</p>
			</div>
		</div>
		` + alert + `
		<section class="panel">
			<form class="toolbar" method="post" action="/apps" style="grid-template-columns:1fr 1fr 1fr auto">
				<select name="user" required` + disabled + `>
					<option value="">Owner</option>
					` + userOptions.String() + `
				</select>
				<input name="name" pattern="[a-z0-9][a-z0-9_-]{0,63}" placeholder="App name" required` + disabled + `>
				<select name="type" required` + disabled + `>
					<option value="go">Go web app</option>
					<option value="node">Node.js web app</option>
					<option value="static">Static site</option>
					<option value="worker">Worker / bot</option>
				</select>
				<button class="button"` + disabled + `>Create app</button>
			</form>
			` + hint + `
			<table>
				<thead><tr><th>App</th><th>Owner</th><th>Type</th><th>Port</th><th>CPU</th><th>Memory</th><th>Tasks</th><th>Root</th></tr></thead>
				<tbody>` + rows.String() + `</tbody>
			</table>
		</section>
	</main>
	` + appResourcesScript() + `
</body>
</html>`
}

func appPage(app panelapp.App, status, unit, message string, extras ...string) string {
	alert := ""
	if message != "" {
		alert = `<div class="alert">` + html.EscapeString(message) + `</div>`
	}

	port := "—"
	if app.Port > 0 {
		port = fmt.Sprintf("%d", app.Port)
	}

	extraBlock := ""
	if len(extras) > 0 {
		extraBlock = strings.Join(extras, "")
	}

	statusClass := " warn"
	statusLabel := strings.TrimSpace(status)
	switch status {
	case "active":
		statusClass = " ok"
		statusLabel = "Running"
	case "activating":
		statusLabel = "Starting"
	case "failed":
		statusLabel = "Failed"
	case "inactive", "":
		statusLabel = "Stopped"
	default:
		if statusLabel == "" {
			statusLabel = "Unknown"
		}
	}

	svc := app.Service
	if svc.Mode == "" {
		svc.Mode = "form"
		svc.AutoStart = true
	}
	if svc.RunMode == "" {
		svc.RunMode = defaultRunMode(app.Type)
	}
	if svc.Restart == "" {
		svc.Restart = "on-failure"
	}
	if svc.RestartSec == 0 {
		svc.RestartSec = 3
	}
	if svc.TimeoutStopSec == 0 {
		svc.TimeoutStopSec = 15
	}
	if svc.WorkingDirectory == "" {
		svc.WorkingDirectory = app.Root
	}
	if svc.Path == "" {
		svc.Path = defaultServicePath(app)
	}
	if svc.LimitNOFILE == 0 {
		svc.LimitNOFILE = 65535
	}
	if svc.TasksMax == 0 {
		svc.TasksMax = 256
	}
	if svc.LogRetentionDays == 0 {
		svc.LogRetentionDays = 7
	}

	runMode := "Caddy static"
	resourceLimit := "not applicable"
	autoStart := "not applicable"
	serviceName := "served directly by Caddy"
	runtimeActions := ""
	serviceBlock := ""
	if app.Type != "static" {
		runMode = svc.RunMode
		autoStart = "disabled"
		if svc.AutoStart {
			autoStart = "enabled"
		}
		resourceLimit = "server defaults"
		if svc.CPUQuota != "" || svc.MemoryMax != "" {
			var parts []string
			if svc.CPUQuota != "" {
				parts = append(parts, "CPU "+svc.CPUQuota)
			}
			if svc.MemoryMax != "" {
				parts = append(parts, "RAM "+svc.MemoryMax)
			}
			resourceLimit = strings.Join(parts, " · ")
		}
		serviceName = "open-go-panel-app-" + fmt.Sprintf("%d", app.ID) + ".service"

		primaryAction := `<form method="post" action="/apps/` + fmt.Sprintf("%d", app.ID) + `/start"><button class="button">Start</button></form>`
		if status == "active" {
			primaryAction = `<form method="post" action="/apps/` + fmt.Sprintf("%d", app.ID) + `/restart"><button class="button">Restart</button></form>`
		}
		runtimeActions = `
			<div class="actions runtime-actions">
				` + primaryAction + `
				<form method="post" action="/apps/` + fmt.Sprintf("%d", app.ID) + `/stop"><button class="secondary">Stop</button></form>
				<a class="secondary" href="/apps/` + fmt.Sprintf("%d", app.ID) + `/logs">Logs</a>
				<a class="secondary" href="/terminal?user=` + html.EscapeString(app.User) + `&app=` + fmt.Sprintf("%d", app.ID) + `">Terminal</a>
				<a class="secondary" href="#service-settings" onclick="document.getElementById('service-settings').open=true">Service settings</a>
			</div>`

		rawUnit := svc.RawUnit
		if rawUnit == "" {
			rawUnit = unit
		}
		serviceBlock = `
		<details id="service-settings" class="panel panel-pad advanced-block app-card-wide service-settings-card" style="margin-top:16px">
			<summary class="section-title" style="margin:0;cursor:pointer">
				<div>
					<h2>Service settings</h2>
					<p class="note" style="margin:6px 0 0">systemd runtime, limits, environment and advanced unit configuration.</p>
				</div>
				<span class="secondary">Configure</span>
			</summary>

			<div class="service-settings-body">
				<div class="service-summary">
					<div><span>Restart policy</span><strong>` + html.EscapeString(svc.Restart) + `</strong></div>
					<div><span>Autostart</span><strong>` + autoStart + `</strong></div>
					<div><span>Stop timeout</span><strong>` + fmt.Sprintf("%ds", svc.TimeoutStopSec) + `</strong></div>
					<div><span>Log retention</span><strong>` + fmt.Sprintf("%d days", svc.LogRetentionDays) + `</strong></div>
				</div>

				<div class="actions" style="justify-content:flex-start;margin:14px 0 18px">
					<form method="post" action="/apps/` + fmt.Sprintf("%d", app.ID) + `/service/auto">
						<button class="secondary">Apply server defaults</button>
					</form>
				</div>

				<form method="post" action="/apps/` + fmt.Sprintf("%d", app.ID) + `/service">
					<input type="hidden" name="mode" value="form">
					<input type="hidden" name="auto_start" value="0">
					<div class="service-form-grid">
						<div>
							<label>Run mode</label>
							<select name="run_mode">` + runModeOptions(app.Type, svc.RunMode) + `</select>
						</div>
						<div>
							<label>Restart policy</label>
							<select name="restart">
								<option value="on-failure"` + selected(svc.Restart, "on-failure") + `>On failure</option>
								<option value="always"` + selected(svc.Restart, "always") + `>Always</option>
								<option value="no"` + selected(svc.Restart, "no") + `>No restart</option>
							</select>
						</div>
						<div class="span-2">
							<label>Working directory</label>
							<input name="working_directory" value="` + html.EscapeString(svc.WorkingDirectory) + `">
						</div>
						<div class="span-2">
							<label>PATH</label>
							<input name="path" value="` + html.EscapeString(svc.Path) + `">
						</div>
						<div class="span-2">
							<label>Custom command</label>
							<input name="command" value="` + html.EscapeString(svc.Command) + `" placeholder="./app, go run ., npm run dev">
							<p class="note" style="margin:6px 0 0">Used only when Run mode = Custom.</p>
						</div>
						<div><label>CPU quota</label><input name="cpu_quota" value="` + html.EscapeString(svc.CPUQuota) + `" placeholder="100%"></div>
						<div><label>Memory max</label><input name="memory_max" value="` + html.EscapeString(svc.MemoryMax) + `" placeholder="512M"></div>
						<div><label>Restart delay</label><input name="restart_sec" type="number" min="0" max="300" value="` + fmt.Sprintf("%d", svc.RestartSec) + `"></div>
						<div><label>Stop timeout</label><input name="timeout_stop_sec" type="number" min="0" max="3600" value="` + fmt.Sprintf("%d", svc.TimeoutStopSec) + `"></div>
						<div><label>LimitNOFILE</label><input name="limit_nofile" type="number" min="0" value="` + fmt.Sprintf("%d", svc.LimitNOFILE) + `"></div>
						<div><label>TasksMax</label><input name="tasks_max" type="number" min="0" value="` + fmt.Sprintf("%d", svc.TasksMax) + `"></div>
						<div><label>Log retention, days</label><input name="log_retention_days" type="number" min="0" max="3650" value="` + fmt.Sprintf("%d", svc.LogRetentionDays) + `"></div>
						<div style="display:flex;align-items:end"><label class="check-row" style="margin:0;width:100%"><input type="checkbox" name="auto_start" value="1"` + checked(svc.AutoStart) + `><span>Start automatically on boot</span></label></div>
						<div class="span-2">
							<label>Environment</label>
							<textarea class="codearea" style="min-height:150px" name="environment" placeholder="APP_ENV=production&#10;DATABASE_URL=...">` + html.EscapeString(svc.Environment) + `</textarea>
						</div>
					</div>
					<div class="actions" style="justify-content:flex-start;margin-top:14px">
						<button class="button">Save service settings</button>
					</div>
				</form>

				<details class="advanced-block" style="margin-top:16px">
					<summary class="secondary">Advanced: raw systemd unit</summary>
					<form method="post" action="/apps/` + fmt.Sprintf("%d", app.ID) + `/service" style="margin-top:14px">
						<input type="hidden" name="mode" value="raw">
						<textarea class="codearea" name="raw_unit" spellcheck="false">` + html.EscapeString(rawUnit) + `</textarea>
						<p class="note" style="margin:8px 0 0">The complete unit is validated with systemd-analyze before replacing the active unit.</p>
						<div class="actions" style="justify-content:flex-start;margin-top:12px"><button class="button">Save raw unit</button></div>
					</form>
				</details>
			</div>
		</details>`
	}

	portFact := `<div><span>Port</span><code>` + port + `</code></div>`
	if app.Type == "go" || app.Type == "node" {
		portFact = `
			<div class="runtime-port-fact">
				<span>Port</span>
				<div class="runtime-port-row">
					<code>` + port + `</code>
					<details>
						<summary class="mini-link">Change</summary>
						<form method="post" action="/apps/` + fmt.Sprintf("%d", app.ID) + `/port" class="inline-popover">
							<label>Internal port</label>
							<input type="number" name="port" min="1024" max="65535" value="` + port + `" required>
							<button class="button">Change port</button>
						</form>
					</details>
				</div>
			</div>`
	}

	return pageHead(app.Name) + `<body>` + appHeader("apps") + `
	<main class="shell app-shell">
		<div class="app-page-head">
			<div>
				<a class="app-breadcrumb" href="/apps">Applications</a>
				<p class="eyebrow" style="margin-top:10px">Application #` + fmt.Sprintf("%d", app.ID) + `</p>
				<div class="app-title-row">
					<h1>` + html.EscapeString(app.Name) + `</h1>
					<span class="badge">` + html.EscapeString(app.Type) + `</span>
				</div>
				<p class="sub">` + html.EscapeString(app.User) + ` · ` + html.EscapeString(app.Root) + `</p>
			</div>
			<a class="secondary" href="/apps">Back to apps</a>
		</div>

		` + alert + `

		<section class="panel panel-pad app-runtime-card">
			<div class="runtime-head">
				<div>
					<p class="eyebrow">Runtime</p>
					<div class="runtime-state"><strong>` + html.EscapeString(statusLabel) + `</strong><span class="status-badge` + statusClass + `">` + html.EscapeString(status) + `</span></div>
					<p class="note" style="margin:7px 0 0"><code>` + html.EscapeString(serviceName) + `</code></p>
				</div>
				` + runtimeActions + `
			</div>
			<div class="runtime-facts">
				<div><span>Owner</span><strong>` + html.EscapeString(app.User) + `</strong></div>
				` + portFact + `
				<div><span>Run mode</span><strong>` + html.EscapeString(runMode) + `</strong></div>
				<div><span>Autostart</span><strong>` + html.EscapeString(autoStart) + `</strong></div>
				<div><span>Limits</span><strong>` + html.EscapeString(resourceLimit) + `</strong></div>
				<div data-resource-app="` + fmt.Sprintf("%d", app.ID) + `"><span>CPU now</span><strong data-resource="cpu">—</strong></div>
				<div data-resource-app="` + fmt.Sprintf("%d", app.ID) + `"><span>Memory now</span><strong data-resource="memory">—</strong></div>
				<div data-resource-app="` + fmt.Sprintf("%d", app.ID) + `"><span>Tasks</span><strong data-resource="tasks">—</strong></div>
			</div>
		</section>

		<div class="app-dashboard-grid">
			` + extraBlock + `
		</div>

		` + serviceBlock + `

		<details class="panel panel-pad advanced-block danger-zone" style="margin-top:16px">
			<summary class="section-title" style="margin:0;cursor:pointer">
				<div><h2>Danger zone</h2><p class="note" style="margin:6px 0 0">Destructive panel actions are kept out of the normal workflow.</p></div>
				<span class="danger">Open</span>
			</summary>
			<div class="danger-zone-body">
				<div>
					<strong>Remove application from Open Go Panel</strong>
					<p class="note" style="margin:5px 0 0">The systemd unit and panel state are removed. Project files in <code>` + html.EscapeString(app.Root) + `</code> are preserved.</p>
				</div>
				<form method="post" action="/apps/` + fmt.Sprintf("%d", app.ID) + `/delete" onsubmit="return confirm('Remove this app from Open Go Panel? Application files will be preserved.')">
					<button class="danger">Remove app</button>
				</form>
			</div>
		</details>
	</main>
	` + appResourcesScript() + `
</body></html>`
}

func appLogsPage(app panelapp.App, filters logFilters, lines []string, hasNext bool, message string) string {
	alert := ""
	if message != "" {
		alert = `<div class="alert">` + html.EscapeString(message) + `</div>`
	}
	path := "/apps/" + fmt.Sprintf("%d", app.ID) + "/logs"
	return pageHead(app.Name+" logs") + `<body>` + appHeader("apps") + `
	<main class="shell">
		<div class="page-head">
			<div>
				<p class="eyebrow">Journal</p>
				<h1>` + html.EscapeString(app.Name) + ` logs</h1>
				<p class="sub">Search retained systemd journal history by period, or switch to the live stream.</p>
			</div>
			<div class="actions">
				<a class="secondary" href="/apps/` + fmt.Sprintf("%d", app.ID) + `">App</a>
				<a class="button" href="/apps/` + fmt.Sprintf("%d", app.ID) + `/logs/live">Live stream</a>
			</div>
		</div>
		` + alert + `
		<section class="panel panel-pad">
			` + logToolbar(path, filters, nil) + `
			<div class="logbox">` + logLinesHTML(lines) + `</div>
			` + logPagerHTML(path, filters, nil, hasNext) + `
		</section>
	</main>
</body></html>`
}

func appLiveLogsPage(app panelapp.App) string {
	return pageHead(app.Name+" live logs") + `<body>` + appHeader("apps") + `
	<main class="shell">
		<div class="page-head">
			<div><p class="eyebrow">Journal</p><h1>` + html.EscapeString(app.Name) + ` live logs</h1><p class="sub">Live stream. Use history for search, periods and pagination.</p></div>
			<div class="actions"><span class="status-badge ok">live</span><a class="secondary" href="/apps/` + fmt.Sprintf("%d", app.ID) + `/logs">History</a><button class="secondary" type="button" onclick="document.getElementById('logbox').textContent=''">Clear view</button></div>
		</div>
		<pre class="logbox" id="logbox"></pre>
	</main>
	<script>
		const box = document.getElementById('logbox');
		const source = new EventSource('/apps/` + fmt.Sprintf("%d", app.ID) + `/logs/stream');
		source.onmessage = (event) => {
			const stick = box.scrollTop + box.clientHeight >= box.scrollHeight - 32;
			if (box.textContent && !box.textContent.endsWith('\n')) box.textContent += '\n';
			box.textContent += event.data + '\n';
			if (stick) box.scrollTop = box.scrollHeight;
		};
	</script>
</body></html>`
}

func currentUnit(cfg Config, id int64) string {
	unit, _ := cfg.Apps.Unit(id)
	return unit
}

func defaultRunMode(appType string) string {
	switch appType {
	case "go":
		return "go-build"
	case "node":
		return "node-npm"
	default:
		return "custom"
	}
}

func selected(got, want string) string {
	if got == want {
		return " selected"
	}
	return ""
}

func runModeOptions(appType, current string) string {
	options := []struct{ value, label string }{{"custom", "Custom command"}}
	if appType == "go" {
		options = append([]struct{ value, label string }{
			{"go-build", "Go: build + run binary"},
			{"go-run", "Go: go run ."},
			{"go-air", "Go: Air live reload"},
		}, options...)
	}
	if appType == "node" {
		options = append([]struct{ value, label string }{{"node-npm", "Node: npm start"}}, options...)
	}

	var b strings.Builder
	for _, option := range options {
		fmt.Fprintf(&b, `<option value="%s"%s>%s</option>`,
			option.value,
			selected(current, option.value),
			html.EscapeString(option.label),
		)
	}
	return b.String()
}

func checked(v bool) string {
	if v {
		return " checked"
	}
	return ""
}

func defaultServicePath(app panelapp.App) string {
	if app.Type == "go" {
		return "/home/" + app.User + "/go/bin:/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin"
	}
	return "/usr/local/bin:/usr/bin:/bin"
}

func recommendedServiceConfig(app panelapp.App, info systeminfo.Info) panelapp.ServiceConfig {
	cpuQuota := "100%"
	if info.CPUs >= 4 {
		cpuQuota = "200%"
	}

	memoryMB := int(info.MemoryTotal / 1024 / 1024 / 4)
	if memoryMB < 256 {
		memoryMB = 256
	}
	if memoryMB > 2048 {
		memoryMB = 2048
	}

	runMode := app.Service.RunMode
	if runMode == "" {
		runMode = defaultRunMode(app.Type)
	}

	return panelapp.ServiceConfig{
		Mode:             "form",
		RunMode:          runMode,
		Command:          app.Service.Command,
		WorkingDirectory: app.Root,
		Path:             defaultServicePath(app),
		Restart:          "on-failure",
		RestartSec:       3,
		TimeoutStopSec:   15,
		CPUQuota:         cpuQuota,
		MemoryMax:        fmt.Sprintf("%dM", memoryMB),
		LimitNOFILE:      65535,
		TasksMax:         256,
		LogRetentionDays: 7,
		AutoStart:        true,
		Environment:      app.Service.Environment,
	}
}


func deployBlock(cfg Config, app panelapp.App) string {
	deploy, err := cfg.Apps.DeployConfig(app.ID)
	if err != nil {
		return `<div class="alert" style="margin-top:18px">` + html.EscapeString(err.Error()) + `</div>`
	}
	keyInfo, keyErr := cfg.Apps.DeployKeyInfo(app.ID)

	deployed := "never"
	if !deploy.DeployedAt.IsZero() {
		deployed = deploy.DeployedAt.Local().Format("2006-01-02 15:04")
	}
	current := deploy.CurrentCommit
	if len(current) > 12 {
		current = current[:12]
	}
	previous := deploy.PreviousCommit
	if len(previous) > 12 {
		previous = previous[:12]
	}
	rollbackDisabled := ""
	if deploy.PreviousCommit == "" {
		rollbackDisabled = " disabled"
	}

	sshPanel := ""
	if keyErr == nil {
		if !keyInfo.Generated {
			sshPanel = `
				<p class="note">For private repositories use an SSH URL such as <code>git@github.com:org/repo.git</code>. Generate a unique key for this app, then add its public key as a Deploy key in GitHub/GitLab.</p>
				<form method="post" action="/apps/` + fmt.Sprintf("%d", app.ID) + `/deploy/key">
					<button class="secondary">Generate SSH deploy key</button>
				</form>`
		} else {
			hostState := `<span class="status-badge warn">host not trusted</span>`
			hostAction := ""
			if keyInfo.HostKnown {
				hostState = `<span class="status-badge ok">host trusted</span>`
			} else if keyInfo.Host != "" {
				hostAction = `
					<form method="post" action="/apps/` + fmt.Sprintf("%d", app.ID) + `/deploy/trust-host">
						<button class="secondary">Trust ` + html.EscapeString(keyInfo.Host) + `</button>
					</form>`
			}
			hostLabel := keyInfo.Host
			if hostLabel == "" {
				hostLabel = "Save an SSH repository URL to detect the Git host."
				hostState = ""
			}
			sshPanel = `
				<div class="deploy-key-head">
					<div><strong>SSH deploy key</strong><p class="note" style="margin:5px 0 0">` + html.EscapeString(hostLabel) + `</p></div>
					` + hostState + `
				</div>
				<textarea class="codearea deploy-key-value" readonly id="deploy-key-` + fmt.Sprintf("%d", app.ID) + `">` + html.EscapeString(keyInfo.PublicKey) + `</textarea>
				<div class="actions" style="justify-content:flex-start;margin-top:10px">
					<button type="button" class="secondary" onclick="navigator.clipboard.writeText(document.getElementById('deploy-key-` + fmt.Sprintf("%d", app.ID) + `').value)">Copy public key</button>
					` + hostAction + `
				</div>`
		}
	}

	return `
		<section class="panel panel-pad app-card app-card-wide">
			<div class="section-title">
				<div><h2>Deploy</h2><p class="note" style="margin:6px 0 0">Pull a Git branch into the app directory, prepare dependencies and restart the service.</p></div>
				<span class="badge">` + html.EscapeString(deployed) + `</span>
			</div>
			<form method="post" action="/apps/` + fmt.Sprintf("%d", app.ID) + `/deploy/config">
				<div class="app-form-row app-form-row-deploy">
					<input name="repository" value="` + html.EscapeString(deploy.Repository) + `" placeholder="git@github.com:org/private-repo.git or https://..." required>
					<input name="branch" value="` + html.EscapeString(defaultString(deploy.Branch, "main")) + `" placeholder="main" required>
					<button class="secondary">Save Git settings</button>
				</div>
			</form>
			<div class="actions" style="justify-content:flex-start;margin-top:10px">
				<form method="post" action="/apps/` + fmt.Sprintf("%d", app.ID) + `/deploy"><button class="button">Deploy now</button></form>
				<form method="post" action="/apps/` + fmt.Sprintf("%d", app.ID) + `/rollback" onsubmit="return confirm('Rollback to the previous deployed commit?')"><button class="secondary"` + rollbackDisabled + `>Rollback</button></form>
			</div>
			<p class="note" style="margin:10px 0 0">Current: <code>` + html.EscapeString(defaultString(current, "—")) + `</code> · Previous: <code>` + html.EscapeString(defaultString(previous, "—")) + `</code></p>

			<details class="advanced-block deploy-key-block" style="margin-top:16px">
				<summary class="secondary">Private Git / SSH deploy key</summary>
				<div style="margin-top:14px">` + sshPanel + `</div>
			</details>
		</section>`
}

func appResourcesScript() string {
	return `<script>
	(() => {
		const formatBytes = (bytes) => {
			if (!Number.isFinite(bytes) || bytes <= 0) return '0 B';
			const units = ['B','KB','MB','GB','TB'];
			let value = bytes;
			let unit = 0;
			while (value >= 1024 && unit < units.length - 1) {
				value /= 1024;
				unit++;
			}
			const digits = unit >= 2 && value < 10 ? 1 : 0;
			return value.toFixed(digits) + ' ' + units[unit];
		};
		const render = (usage) => {
			const row = document.querySelectorAll('[data-resource-app="' + usage.app_id + '"]');
			row.forEach((scope) => {
				const cpu = scope.querySelector('[data-resource="cpu"]');
				const memory = scope.querySelector('[data-resource="memory"]');
				const tasks = scope.querySelector('[data-resource="tasks"]');
				if (usage.state === 'static') {
					if (cpu) cpu.textContent = 'Caddy';
					if (memory) memory.textContent = 'shared';
					if (tasks) tasks.textContent = '—';
					return;
				}
				if (!usage.available) {
					if (cpu) cpu.textContent = usage.state === 'active' ? '—' : '0%';
					if (memory) memory.textContent = usage.state === 'active' ? '—' : '0 B';
					if (tasks) tasks.textContent = usage.state === 'active' ? '—' : '0';
					return;
				}
				if (cpu) cpu.textContent = usage.cpu_percent.toFixed(1) + '%';
				if (memory) {
					memory.textContent = formatBytes(usage.memory_bytes);
					if (usage.memory_limit > 0) memory.title = 'Limit: ' + formatBytes(usage.memory_limit);
				}
				if (tasks) {
					tasks.textContent = String(usage.tasks);
					if (usage.tasks_limit > 0) tasks.title = 'Limit: ' + usage.tasks_limit;
				}
			});
		};
		const refresh = async () => {
			try {
				const response = await fetch('/apps/resources', {cache:'no-store'});
				if (!response.ok) return;
				const data = await response.json();
				data.forEach(render);
			} catch (_) {}
		};
		refresh();
		const timer = setInterval(refresh, 5000);
		document.addEventListener('visibilitychange', () => {
			if (!document.hidden) refresh();
		});
		window.addEventListener('pagehide', () => clearInterval(timer), {once:true});
	})();
	</script>`
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func appHealthBlock(r *http.Request, cfg Config, app panelapp.App) string {
	health := cfg.Apps.RuntimeHealth(r.Context(), app.ID)

	statusCard := func(state, label, detail string) string {
		className := "warn"
		if state == "ok" {
			className = "ok"
		}
		return `<div class="metric"><span>` + html.EscapeString(label) + `</span><strong>` + html.EscapeString(state) + `</strong><small>` + html.EscapeString(detail) + ` · <span class="status-badge ` + className + `">` + html.EscapeString(state) + `</span></small></div>`
	}
	naCard := func(label, detail string) string {
		return `<div class="metric"><span>` + html.EscapeString(label) + `</span><strong>n/a</strong><small>` + html.EscapeString(detail) + `</small></div>`
	}

	processCard := statusCard("down", "Process", health.ProcessStatus)
	if health.ProcessStatus == "active" {
		processCard = statusCard("ok", "Process", "systemd active")
	}
	if app.Type == "static" {
		processCard = naCard("Process", "served directly by Caddy")
	}

	portCard := naCard("Port", "not required")
	localHTTPCard := naCard("Local HTTP", "not required")
	if app.Port > 0 {
		if health.PortListening {
			portCard = statusCard("ok", "Port", "127.0.0.1:"+strconv.Itoa(app.Port))
		} else {
			portCard = statusCard("down", "Port", "not listening")
		}

		localState := "down"
		localDetail := "unreachable"
		if health.HTTPReachable {
			localDetail = "HTTP " + strconv.Itoa(health.HTTPStatus)
			if health.HTTPStatus < 500 {
				localState = "ok"
			}
		}
		localHTTPCard = statusCard(localState, "Local HTTP", localDetail)
	}

	publicCard := naCard("Public URL", "domain not connected")
	if cfg.Caddy != nil {
		if site, ok, _ := cfg.Caddy.SiteForApp(app.ID); ok {
			scheme := "https"
			label := "Public HTTPS"
			if settings, err := cfg.Caddy.GlobalSettings(); err == nil && !settings.HTTPS {
				scheme = "http"
				label = "Public HTTP"
			}
			client := &http.Client{Timeout: 4 * time.Second}
			req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, scheme+"://"+site.Domain+"/", nil)
			state := "down"
			detail := site.Domain + " · unreachable"
			if err == nil {
				resp, requestErr := client.Do(req)
				if requestErr == nil {
					detail = site.Domain + " · HTTP " + strconv.Itoa(resp.StatusCode)
					if resp.StatusCode < 500 {
						state = "ok"
					}
					_ = resp.Body.Close()
				}
			}
			publicCard = statusCard(state, label, detail)
		}
	}

	return `
		<section class="panel panel-pad app-card app-card-wide">
			<div class="section-title"><div><h2>Health</h2><p class="note" style="margin:6px 0 0">Each layer is checked independently. HTTP 5xx is treated as unhealthy.</p></div></div>
			<div class="health-grid">
				` + processCard + portCard + localHTTPCard + publicCard + `
			</div>
		</section>`
}

func databaseBlock(cfg Config, app panelapp.App) string {
	if cfg.Databases == nil || app.Type == "static" {
		return ""
	}
	items, err := cfg.Databases.List()
	if err != nil {
		return `<div class="alert" style="margin-top:18px">` + html.EscapeString(err.Error()) + `</div>`
	}
	attachments, err := cfg.Databases.AttachmentsForApp(app.ID)
	if err != nil {
		return `<div class="alert" style="margin-top:18px">` + html.EscapeString(err.Error()) + `</div>`
	}

	var current strings.Builder
	for _, attachment := range attachments {
		db, err := cfg.Databases.Get(attachment.DatabaseID)
		if err != nil {
			continue
		}
		fmt.Fprintf(&current, `
			<div class="attachment-row">
				<div class="section-title" style="margin:0">
					<div>
						<strong>%s</strong>
						<div class="note" style="margin-top:4px"><code>%s</code> · %s</div>
					</div>
					<form method="post" action="/apps/%d/database/detach">
						<input type="hidden" name="env_name" value="%s">
						<button class="secondary">Detach</button>
					</form>
				</div>
			</div>`,
			html.EscapeString(db.Name),
			html.EscapeString(attachment.EnvName),
			html.EscapeString(db.Engine),
			app.ID,
			html.EscapeString(attachment.EnvName),
		)
	}

	var options strings.Builder
	for _, item := range items {
		label := item.Name + " · " + item.Engine
		fmt.Fprintf(&options, `<option value="%d">%s</option>`, item.ID, html.EscapeString(label))
	}

	create := `<p class="note" style="margin:6px 0 10px">No managed databases yet.</p><a class="secondary" href="/databases">Open Databases</a>`
	if options.Len() > 0 {
		create = `
			<form method="post" action="/apps/` + fmt.Sprintf("%d", app.ID) + `/database" class="app-form-row app-form-row-db">
				<select name="database_id" required>` + options.String() + `</select>
				<input name="env_name" value="DATABASE_URL" placeholder="ENV name" required>
				<button class="button">Attach</button>
			</form>`
	}

	return `
		<section class="panel panel-pad app-card">
			<div class="section-title" style="margin-bottom:10px">
				<div>
					<h2>Databases</h2>
					<p class="note" style="margin:5px 0 0">Attachments are tracked by Open Go Panel and written into the app environment.</p>
				</div>
				<a class="secondary" href="/databases">Manage databases</a>
			</div>
			` + current.String() + create + `
		</section>`
}
