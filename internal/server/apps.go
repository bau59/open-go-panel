package server

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"strings"
	"strconv"

	panelapp "github.com/bau59/open-go-panel/internal/app"
	"github.com/bau59/open-go-panel/internal/linuxuser"
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
		writeHTML(w, cfg.Logger, http.StatusOK, appPage(app, status, unit, ""))
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
		serviceCfg := panelapp.ServiceConfig{
			Mode:        strings.TrimSpace(r.FormValue("mode")),
			RunMode:     strings.TrimSpace(r.FormValue("run_mode")),
			Command:     strings.TrimSpace(r.FormValue("command")),
			Restart:     strings.TrimSpace(r.FormValue("restart")),
			RestartSec:  restartSec,
			CPUQuota:    strings.TrimSpace(r.FormValue("cpu_quota")),
			MemoryMax:   strings.TrimSpace(r.FormValue("memory_max")),
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
		logs, err := cfg.Apps.Logs(r.Context(), id, 300)
		if err != nil {
			logs = err.Error()
		}
		writeHTML(w, cfg.Logger, http.StatusOK, appLogsPage(app, logs))
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
		<tr>
			<td><a href="/apps/%d"><strong>%s</strong></a><div class="muted">#%d</div></td>
			<td>%s</td>
			<td><span class="badge">%s</span></td>
			<td><code>%s</code></td>
			<td><code>%s</code></td>
		</tr>`,
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
		rows.WriteString(`<tr><td colspan="5" class="empty">No applications yet.</td></tr>`)
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
				<thead><tr><th>App</th><th>Owner</th><th>Type</th><th>Port</th><th>Root</th></tr></thead>
				<tbody>` + rows.String() + `</tbody>
			</table>
		</section>
	</main>
</body>
</html>`
}

func appPage(app panelapp.App, status, unit, message string) string {
	alert := ""
	if message != "" {
		alert = `<div class="alert">` + html.EscapeString(message) + `</div>`
	}

	port := "—"
	if app.Port > 0 {
		port = fmt.Sprintf("%d", app.Port)
	}

	statusClass := ""
	if status == "active" {
		statusClass = " ok"
	} else if status == "failed" {
		statusClass = " warn"
	}

	serviceBlock := `<p class="note">Static applications are served directly and do not need a systemd service.</p>`
	if app.Type != "static" {
		svc := app.Service
		if svc.Mode == "" {
			svc.Mode = "form"
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

		rawUnit := svc.RawUnit
		if rawUnit == "" {
			rawUnit = unit
		}

		serviceBlock = `
			<div class="section-title">
				<div><h2>Systemd service</h2><p class="note" style="margin:6px 0 0">Visual configuration or full raw unit.</p></div>
				<span class="status-badge` + statusClass + `">` + html.EscapeString(status) + `</span>
			</div>

			<form method="post" action="/apps/` + fmt.Sprintf("%d", app.ID) + `/service">
				<input type="hidden" name="mode" value="form">
				<div class="grid" style="grid-template-columns:1fr 1fr">
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
				</div>

				<div style="margin-top:14px">
					<label>Custom command</label>
					<input name="command" value="` + html.EscapeString(svc.Command) + `" placeholder="./app, go run ., npm run dev">
					<p class="note" style="margin:6px 0 0">Used only when Run mode = Custom.</p>
				</div>

				<div class="grid" style="grid-template-columns:1fr 1fr 1fr;margin-top:14px">
					<div><label>CPU quota</label><input name="cpu_quota" value="` + html.EscapeString(svc.CPUQuota) + `" placeholder="100%"></div>
					<div><label>Memory max</label><input name="memory_max" value="` + html.EscapeString(svc.MemoryMax) + `" placeholder="512M"></div>
					<div><label>Restart delay</label><input name="restart_sec" type="number" min="0" max="300" value="` + fmt.Sprintf("%d", svc.RestartSec) + `"></div>
				</div>

				<div style="margin-top:14px">
					<label>Environment</label>
					<textarea class="codearea" style="min-height:130px" name="environment" placeholder="APP_ENV=production&#10;DATABASE_URL=...">` + html.EscapeString(svc.Environment) + `</textarea>
				</div>

				<div class="actions" style="justify-content:flex-start;margin-top:14px">
					<button class="button">Save visual config</button>
				</div>
			</form>

			<details style="margin-top:22px">
				<summary class="secondary">Advanced: edit raw systemd unit</summary>
				<form method="post" action="/apps/` + fmt.Sprintf("%d", app.ID) + `/service" style="margin-top:14px">
					<input type="hidden" name="mode" value="raw">
					<textarea class="codearea" name="raw_unit" spellcheck="false">` + html.EscapeString(rawUnit) + `</textarea>
					<p class="note" style="margin:8px 0 0">Advanced mode writes the complete unit after systemd-analyze verify.</p>
					<div class="actions" style="justify-content:flex-start;margin-top:12px"><button class="button">Save raw unit</button></div>
				</form>
			</details>

			<div class="actions" style="justify-content:flex-start;margin-top:20px">
				<form method="post" action="/apps/` + fmt.Sprintf("%d", app.ID) + `/start"><button class="button">Start</button></form>
				<form method="post" action="/apps/` + fmt.Sprintf("%d", app.ID) + `/restart"><button class="secondary">Restart</button></form>
				<form method="post" action="/apps/` + fmt.Sprintf("%d", app.ID) + `/stop"><button class="secondary">Stop</button></form>
				<a class="secondary" href="/apps/` + fmt.Sprintf("%d", app.ID) + `/logs">View logs</a>
			</div>`
	}

	return pageHead(app.Name) + `<body>` + appHeader("apps") + `
	<main class="shell">
		<div class="page-head">
			<div>
				<p class="eyebrow">Application #` + fmt.Sprintf("%d", app.ID) + `</p>
				<h1>` + html.EscapeString(app.Name) + `</h1>
				<p class="sub">` + html.EscapeString(app.User) + ` · ` + html.EscapeString(app.Type) + `</p>
			</div>
			<a class="secondary" href="/apps">Back to apps</a>
		</div>
		` + alert + `
		<div class="grid" style="grid-template-columns:minmax(280px,.7fr) minmax(0,1.5fr)">
			<section class="panel panel-pad">
				<div class="section-title"><h2>Overview</h2><span class="badge">` + html.EscapeString(app.Type) + `</span></div>
				<div class="meta-grid">
					<span>Owner</span><strong>` + html.EscapeString(app.User) + `</strong>
					<span>Port</span><code>` + port + `</code>
					<span>Root</span><code>` + html.EscapeString(app.Root) + `</code>
					<span>Service</span><code>open-go-panel-app-` + fmt.Sprintf("%d", app.ID) + `.service</code>
				</div>
			</section>
			<section class="panel panel-pad">` + serviceBlock + `</section>
		</div>
	</main>
</body>
</html>`
}

func appLogsPage(app panelapp.App, logs string) string {
	return pageHead(app.Name+" logs") + `<body>` + appHeader("apps") + `
	<main class="shell">
		<div class="page-head">
			<div><p class="eyebrow">Journal</p><h1>` + html.EscapeString(app.Name) + ` logs</h1><p class="sub">Last 300 lines from ` + fmt.Sprintf("open-go-panel-app-%d.service", app.ID) + `.</p></div>
			<div class="actions"><a class="secondary" href="/apps/` + fmt.Sprintf("%d", app.ID) + `">App</a><a class="button" href="/apps/` + fmt.Sprintf("%d", app.ID) + `/logs">Refresh</a></div>
		</div>
		<pre class="logbox">` + html.EscapeString(logs) + `</pre>
	</main>
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
