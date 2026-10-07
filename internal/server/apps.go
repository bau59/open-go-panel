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
		writeHTML(w, cfg.Logger, http.StatusOK, appPage(app, status, ""))
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
			writeHTML(w, cfg.Logger, http.StatusBadRequest, appPage(app, cfg.Apps.Status(r.Context(), id), err.Error()))
			return
		}

		http.Redirect(w, r, fmt.Sprintf("/apps/%d", id), http.StatusSeeOther)
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
				writeHTML(w, cfg.Logger, http.StatusBadRequest, appPage(app, cfg.Apps.Status(r.Context(), id), err.Error()))
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

func appPage(app panelapp.App, status, message string) string {
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
		serviceBlock = fmt.Sprintf(`
			<div class="section-title">
				<h2>Service</h2>
				<span class="status-badge%s">%s</span>
			</div>
			<form method="post" action="/apps/%d/command">
				<label>Start command</label>
				<input name="command" value="%s" placeholder="./app or npm start" required>
				<div class="actions" style="justify-content:flex-start;margin-top:10px">
					<button class="button">Save command</button>
				</div>
			</form>
			<div class="actions" style="justify-content:flex-start;margin-top:18px">
				<form method="post" action="/apps/%d/start"><button class="button">Start</button></form>
				<form method="post" action="/apps/%d/restart"><button class="secondary">Restart</button></form>
				<form method="post" action="/apps/%d/stop"><button class="secondary">Stop</button></form>
			</div>`,
			statusClass,
			html.EscapeString(status),
			app.ID,
			html.EscapeString(app.Command),
			app.ID,
			app.ID,
			app.ID,
		)
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
		<div class="grid" style="grid-template-columns:minmax(0,1fr) minmax(0,1.15fr)">
			<section class="panel panel-pad">
				<div class="section-title"><h2>Overview</h2><span class="badge">` + html.EscapeString(app.Type) + `</span></div>
				<div class="meta-grid">
					<span>Owner</span><strong>` + html.EscapeString(app.User) + `</strong>
					<span>Port</span><code>` + port + `</code>
					<span>Root</span><code>` + html.EscapeString(app.Root) + `</code>
					<span>Service</span><code>open-go-panel-app-` + fmt.Sprintf("%d", app.ID) + `.service</code>
				</div>
			</section>
			<section class="panel panel-pad">
				` + serviceBlock + `
			</section>
		</div>
	</main>
</body>
</html>`
}
