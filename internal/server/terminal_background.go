package server

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	panelapp "github.com/bau59/open-go-panel/internal/app"
	"github.com/bau59/open-go-panel/internal/linuxuser"
	panelterminal "github.com/bau59/open-go-panel/internal/terminal"
)

type backgroundPageData struct {
	Users        []linuxuser.User
	Apps         []panelapp.App
	SelectedUser string
	SelectedApp  int64
	WorkDir      string
	Jobs         []panelterminal.BackgroundJob
	Message      string
	Warning      string
}

func registerBackgroundTerminalRoutes(mux *http.ServeMux, store *sessionStore, cfg Config) {
	manager := panelterminal.NewBackgroundManager(cfg.State)

	loadTargets := func(r *http.Request) ([]linuxuser.User, []panelapp.App, error) {
		users, err := cfg.Users.List(r.Context())
		if err != nil {
			return nil, nil, err
		}
		apps, err := cfg.Apps.List()
		return users, apps, err
	}

	mux.Handle("GET /terminal/background", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		users, apps, err := loadTargets(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		username, workdir, appID, resolveErr := resolveTerminalTarget(r, users, apps)
		message := ""
		if resolveErr != nil {
			message = resolveErr.Error()
			username, workdir, appID = "root", "/root", 0
		}
		if r.URL.Query().Get("started") == "1" {
			message = "Background command started. It will keep running when you close this page."
		}
		if r.URL.Query().Get("stopped") == "1" {
			message = "Background command stopped."
		}
		if r.URL.Query().Get("forgotten") == "1" {
			message = "Finished command removed from the list."
		}
		jobs, statusErr := manager.List(r.Context())
		warning := ""
		if statusErr != nil {
			warning = statusErr.Error()
		}
		writeHTML(w, cfg.Logger, http.StatusOK, backgroundPage(backgroundPageData{
			Users: users, Apps: apps, SelectedUser: username, SelectedApp: appID,
			WorkDir: workdir, Jobs: jobs, Message: message, Warning: warning,
		}))
	})))

	mux.Handle("POST /terminal/background/start", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid background command form", http.StatusBadRequest)
			return
		}
		users, apps, err := loadTargets(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		username, workdir, appID, err := resolveTerminalTargetValues(r.FormValue("user"), r.FormValue("app"), users, apps)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		job, err := manager.Start(r.Context(), username, workdir, appID, r.FormValue("command"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if cfg.State != nil {
			cfg.State.Audit(r.Context(), "terminal.background.start", job.ID, "user="+username+" workdir="+workdir)
		}
		q := url.Values{"user": {username}, "started": {"1"}}
		if appID > 0 {
			q.Set("app", strconv.FormatInt(appID, 10))
		}
		http.Redirect(w, r, "/terminal/background?"+q.Encode(), http.StatusSeeOther)
	})))

	mux.Handle("GET /terminal/background/{id}/logs", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		job, logText, err := manager.Logs(r.Context(), r.PathValue("id"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeHTML(w, cfg.Logger, http.StatusOK, backgroundLogsPage(job, logText))
	})))

	mux.Handle("POST /terminal/background/{id}/stop", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := manager.Stop(r.Context(), r.PathValue("id")); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, "/terminal/background?stopped=1", http.StatusSeeOther)
	})))

	mux.Handle("POST /terminal/background/{id}/forget", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := manager.Forget(r.Context(), r.PathValue("id")); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, "/terminal/background?forgotten=1", http.StatusSeeOther)
	})))
}

func backgroundPage(data backgroundPageData) string {
	var users strings.Builder
	users.WriteString(`<option value="root"` + selected(data.SelectedUser, "root") + `>root</option>`)
	for _, user := range data.Users {
		fmt.Fprintf(&users, `<option value="%s"%s>%s</option>`,
			html.EscapeString(user.Username), selected(data.SelectedUser, user.Username), html.EscapeString(user.Username))
	}

	var apps strings.Builder
	apps.WriteString(`<option value="">Home directory</option>`)
	if data.SelectedUser != "root" {
		for _, app := range data.Apps {
			if app.User != data.SelectedUser {
				continue
			}
			attr := ""
			if app.ID == data.SelectedApp {
				attr = " selected"
			}
			fmt.Fprintf(&apps, `<option value="%d"%s>%s · %s</option>`,
				app.ID, attr, html.EscapeString(app.Name), html.EscapeString(app.Root))
		}
	}

	var rows strings.Builder
	for _, job := range data.Jobs {
		status := html.EscapeString(job.Status)
		statusClass := "warn"
		if job.Status == "running" {
			statusClass = "ok"
		}
		fmt.Fprintf(&rows, `<tr>
			<td><code title="%s">%s</code><small class="muted" style="display:block;margin-top:5px">%s</small></td>
			<td>%s<small class="muted" style="display:block">%s</small></td>
			<td><span class="state-text %s"><i></i>%s</span><small class="muted" style="display:block">%s</small></td>
			<td><div class="actions table-actions">
				<a class="secondary" href="/terminal/background/%s/logs">Logs</a>
				%s
			</div></td>
		</tr>`,
			html.EscapeString(job.Command), html.EscapeString(trimBackgroundCommand(job.Command)),
			html.EscapeString(job.WorkDir), html.EscapeString(job.Username),
			html.EscapeString(job.CreatedAt), statusClass, status,
			func() string { if job.StoppedAt != "" { return "Stopped " + html.EscapeString(job.StoppedAt) }; return "" }(),
			job.ID,
			func() string {
				if job.Status == "running" || job.Status == "starting" || job.Status == "unknown" {
					return `<form method="post" action="/terminal/background/` + job.ID + `/stop" onsubmit="return confirm('Stop this background command and its child processes?')"><button class="danger">Stop</button></form>`
				}
				return `<form method="post" action="/terminal/background/` + job.ID + `/forget"><button class="secondary">Remove</button></form>`
			}(),
		)
	}
	if rows.Len() == 0 {
		rows.WriteString(`<tr><td colspan="4" class="empty">No background commands started yet.</td></tr>`)
	}

	message := ""
	if data.Message != "" {
		message = `<div class="alert" style="margin-bottom:14px">` + html.EscapeString(data.Message) + `</div>`
	}
	if data.Warning != "" {
		message += `<div class="alert" style="margin-bottom:14px">Service status unavailable: ` + html.EscapeString(data.Warning) + `</div>`
	}
	rootWarning := ""
	if data.SelectedUser == "root" {
		rootWarning = `<div class="terminal-warning">Root commands have unrestricted server access. Use a managed Linux user whenever possible.</div>`
	}

	appValue := ""
	if data.SelectedApp > 0 {
		appValue = strconv.FormatInt(data.SelectedApp, 10)
	}
	return pageHead("Background commands") + `<body>` + appHeader("terminal-background") + `
	<main class="shell">
		<div class="page-head">
			<div><p class="eyebrow">Server shell</p><h1>Background commands</h1>
				<p class="sub">Start a command as a Linux user. It continues independently of the browser and panel service.</p>
			</div>
			<div class="actions"><a class="secondary" href="/terminal">Interactive terminal</a></div>
		</div>
		` + message + `
		<section class="panel panel-pad">
			<div class="section-title"><div><h2>New command</h2><p class="note">Commands run in a separate systemd service. They stop on server reboot and do not restart automatically.</p></div></div>
			<form method="get" action="/terminal/background" class="terminal-toolbar" style="margin-top:14px">
				<div class="terminal-target-meta" style="flex:1;display:flex;gap:12px;align-items:flex-end;flex-wrap:wrap">
					<div><label>User</label><select name="user" onchange="this.form.elements.app.value='';this.form.submit()">` + users.String() + `</select></div>
					<div><label>Working directory</label><select name="app" onchange="this.form.submit()"` + func() string { if data.SelectedUser == "root" { return " disabled" }; return "" }() + `>` + apps.String() + `</select></div>
					<code>` + html.EscapeString(data.WorkDir) + `</code>
				</div>
			</form>
			` + rootWarning + `
			<form method="post" action="/terminal/background/start" style="margin-top:16px">
				<input type="hidden" name="user" value="` + html.EscapeString(data.SelectedUser) + `">
				<input type="hidden" name="app" value="` + appValue + `">
				<label for="background-command">Command</label>
				<textarea id="background-command" name="command" class="codearea" rows="3" maxlength="8192" placeholder="go run ./cmd/worker" required spellcheck="false"></textarea>
				<div class="actions" style="margin-top:12px"><button class="button">Start in background</button></div>
			</form>
		</section>
		<section class="panel panel-pad" style="margin-top:18px">
			<div class="section-title"><div><h2>Saved commands</h2><p class="note">Only this page refresh reads their status. Output is available from the journal; stopping terminates the unit's process group.</p></div><a class="secondary" href="/terminal/background">Refresh</a></div>
			<div class="table-scroll" style="margin-top:14px"><table><thead><tr><th>Command</th><th>User</th><th>Status</th><th>Actions</th></tr></thead><tbody>` + rows.String() + `</tbody></table></div>
		</section>
	</main></body></html>`
}

func trimBackgroundCommand(command string) string {
	command = strings.Join(strings.Fields(command), " ")
	if len(command) > 130 {
		return command[:130] + "…"
	}
	return command
}

func backgroundLogsPage(job panelterminal.BackgroundJob, logText string) string {
	if strings.TrimSpace(logText) == "" {
		logText = "No output recorded for this command."
	}
	return pageHead("Background command logs") + `<body>` + appHeader("terminal-background") + `
	<main class="shell">
		<div class="page-head">
			<div><p class="eyebrow">Background commands</p><h1>Command output</h1>
				<p class="sub">` + html.EscapeString(job.Username) + ` · ` + html.EscapeString(job.WorkDir) + `</p>
			</div>
			<div class="actions"><a class="secondary" href="/terminal/background">Back to commands</a></div>
		</div>
		<section class="panel panel-pad">
			<p><code>` + html.EscapeString(job.Command) + `</code></p>
			<pre class="security-output" style="white-space:pre-wrap;overflow-wrap:anywhere;max-height:70vh;overflow:auto;margin-top:14px">` + html.EscapeString(logText) + `</pre>
		</section>
	</main></body></html>`
}
