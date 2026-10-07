package server

import (
	"fmt"
	"html"
	"net/http"
	"strings"

	panelapp "github.com/bau59/open-go-panel/internal/app"
	"github.com/bau59/open-go-panel/internal/linuxuser"
)

func registerAppRoutes(mux *http.ServeMux, store *sessionStore, cfg Config) {
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
			<td><strong>%s</strong><div class="muted">#%d</div></td>
			<td>%s</td>
			<td><span class="badge">%s</span></td>
			<td>%s</td>
			<td><code>%s</code></td>
		</tr>`,
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

	createDisabled := ""
	createHint := ""
	if len(users) == 0 {
		createDisabled = " disabled"
		createHint = `<p class="note">Create a Linux user first. Every application must have an owner.</p>`
	}

	return `<!doctype html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>Applications · Open Go Panel</title>
	<style>
		*{box-sizing:border-box}
		body{margin:0;background:#0b1020;color:#e5e7eb;font-family:system-ui,-apple-system,sans-serif}
		header{height:64px;display:flex;align-items:center;justify-content:space-between;padding:0 28px;border-bottom:1px solid #1e293b;background:#0f172a}
		a{color:inherit;text-decoration:none}
		.brand{font-weight:800}
		nav{display:flex;gap:18px;color:#94a3b8}
		nav a.active{color:#fff}
		main{max-width:1180px;margin:0 auto;padding:38px 28px 60px}
		h1{margin:0;font-size:30px}
		.sub{margin:7px 0 24px;color:#94a3b8}
		.panel{padding:22px;border:1px solid #253047;border-radius:16px;background:#111827}
		.create{display:grid;grid-template-columns:1fr 1fr 1fr auto;gap:10px;margin-bottom:24px}
		input,select{width:100%;height:42px;padding:0 12px;border:1px solid #334155;border-radius:9px;background:#0f172a;color:#fff}
		button{height:42px;padding:0 15px;border:1px solid #4338ca;border-radius:9px;background:#4f46e5;color:#fff;font-weight:700;cursor:pointer}
		button.secondary{border-color:#334155;background:#111827}
		button:disabled{opacity:.45;cursor:not-allowed}
		table{width:100%;border-collapse:collapse}
		th,td{padding:14px 10px;border-top:1px solid #1e293b;text-align:left;vertical-align:top}
		th{color:#94a3b8;font-size:12px;text-transform:uppercase;letter-spacing:.05em}
		.muted,.note{color:#64748b;font-size:12px}
		.badge{display:inline-flex;padding:4px 8px;border-radius:999px;background:#1e293b;color:#cbd5e1;font-size:12px}
		code{color:#cbd5e1;font-size:12px}
		.alert{margin-bottom:18px;padding:11px 13px;border:1px solid #7f1d1d;border-radius:10px;background:#450a0a;color:#fecaca}
		.empty{text-align:center;color:#64748b;padding:32px}
		@media(max-width:800px){.create{grid-template-columns:1fr}table{display:block;overflow-x:auto}}
	</style>
</head>
<body>
	<header>
		<a class="brand" href="/">Open Go Panel</a>
		<nav><a href="/">Overview</a><a href="/users">Users</a><a class="active" href="/apps">Apps</a></nav>
		<form method="post" action="/logout"><button class="secondary">Logout</button></form>
	</header>
	<main>
		<h1>Applications</h1>
		<p class="sub">Applications belong to Linux users. Ports are allocated to web runtimes automatically.</p>
		` + alert + `
		<section class="panel">
			<form class="create" method="post" action="/apps">
				<select name="user" required` + createDisabled + `>
					<option value="">Owner</option>
					` + userOptions.String() + `
				</select>
				<input name="name" pattern="[a-z0-9][a-z0-9_-]{0,63}" placeholder="app name" required` + createDisabled + `>
				<select name="type" required` + createDisabled + `>
					<option value="go">Go web app</option>
					<option value="node">Node.js web app</option>
					<option value="static">Static site</option>
					<option value="worker">Worker / bot</option>
				</select>
				<button` + createDisabled + `>Create app</button>
			</form>
			` + createHint + `
			<table>
				<thead><tr><th>App</th><th>Owner</th><th>Type</th><th>Port</th><th>Root</th></tr></thead>
				<tbody>` + rows.String() + `</tbody>
			</table>
		</section>
	</main>
</body>
</html>`
}
