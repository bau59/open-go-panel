package server

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"

	panelapp "github.com/bau59/open-go-panel/internal/app"
)

func cronAppID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, fmt.Errorf("invalid application ID")
	}
	return id, nil
}

func cronJobID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("job"), 10, 64)
	if err != nil || id < 1 {
		return 0, fmt.Errorf("invalid scheduled task ID")
	}
	return id, nil
}

func cronError(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), http.StatusBadRequest)
}

func registerAppCronRoutes(mux *http.ServeMux, session *sessionStore, cfg Config) {
	mux.Handle("POST /apps/{id}/cron", requireAuth(session, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		appID, err := cronAppID(r)
		if err != nil {
			cronError(w, err)
			return
		}
		_, err = cfg.Apps.SaveCronJob(r.Context(), appID, 0,
			r.FormValue("name"), r.FormValue("schedule"), r.FormValue("command"), r.FormValue("enabled") == "1")
		if err != nil {
			cronError(w, err)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/apps/%d#cron-jobs", appID), http.StatusSeeOther)
	})))

	mux.Handle("POST /apps/{id}/cron/{job}/save", requireAuth(session, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		appID, err := cronAppID(r)
		if err != nil {
			cronError(w, err)
			return
		}
		jobID, err := cronJobID(r)
		if err != nil {
			cronError(w, err)
			return
		}
		_, err = cfg.Apps.SaveCronJob(r.Context(), appID, jobID,
			r.FormValue("name"), r.FormValue("schedule"), r.FormValue("command"), r.FormValue("enabled") == "1")
		if err != nil {
			cronError(w, err)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/apps/%d#cron-jobs", appID), http.StatusSeeOther)
	})))

	mux.Handle("POST /apps/{id}/cron/{job}/toggle", requireAuth(session, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		appID, err := cronAppID(r)
		if err != nil {
			cronError(w, err)
			return
		}
		jobID, err := cronJobID(r)
		if err != nil {
			cronError(w, err)
			return
		}
		if err := cfg.Apps.SetCronEnabled(r.Context(), appID, jobID, r.FormValue("enabled") == "1"); err != nil {
			cronError(w, err)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/apps/%d#cron-jobs", appID), http.StatusSeeOther)
	})))

	mux.Handle("POST /apps/{id}/cron/{job}/run", requireAuth(session, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		appID, err := cronAppID(r)
		if err != nil {
			cronError(w, err)
			return
		}
		jobID, err := cronJobID(r)
		if err != nil {
			cronError(w, err)
			return
		}
		if err := cfg.Apps.RunCronJob(r.Context(), appID, jobID); err != nil {
			cronError(w, err)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/apps/%d/cron/%d/logs", appID, jobID), http.StatusSeeOther)
	})))

	mux.Handle("POST /apps/{id}/cron/{job}/delete", requireAuth(session, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		appID, err := cronAppID(r)
		if err != nil {
			cronError(w, err)
			return
		}
		jobID, err := cronJobID(r)
		if err != nil {
			cronError(w, err)
			return
		}
		if err := cfg.Apps.DeleteCronJob(r.Context(), appID, jobID); err != nil {
			cronError(w, err)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/apps/%d#cron-jobs", appID), http.StatusSeeOther)
	})))

	mux.Handle("GET /apps/{id}/cron/{job}/logs", requireAuth(session, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		appID, err := cronAppID(r)
		if err != nil {
			cronError(w, err)
			return
		}
		jobID, err := cronJobID(r)
		if err != nil {
			cronError(w, err)
			return
		}
		app, err := cfg.Apps.Get(appID)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		content, err := cfg.Apps.CronLogs(r.Context(), appID, jobID)
		if err != nil {
			cronError(w, err)
			return
		}
		back := fmt.Sprintf("/apps/%d#cron-jobs", appID)
		body := pageHead(app.Name+" · Task logs") + "<body>" + appHeader("apps") +
			`<main class="shell"><div class="page-head"><div><p class="eyebrow">Scheduled tasks</p><h1>Task logs</h1><p class="sub">` +
			html.EscapeString(app.Name) + ` · job #` + strconv.FormatInt(jobID, 10) +
			` · latest 150 journal lines</p></div><a class="secondary" href="` + back +
			`">Back to tasks</a></div><pre class="logbox">` + html.EscapeString(content) +
			`</pre></main></body></html>`
		writeHTML(w, cfg.Logger, http.StatusOK, body)
	})))
}

func cronBlock(cfg Config, app panelapp.App) string {
	if cfg.Apps == nil {
		return ""
	}
	jobs, err := cfg.Apps.CronJobs(context.Background(), app.ID)
	if err != nil {
		return `<section class="panel panel-pad app-card-wide" id="cron-jobs" style="margin-top:16px"><h2>Scheduled tasks</h2><p class="alert">` +
			html.EscapeString(err.Error()) + `</p></section>`
	}
	base := fmt.Sprintf("/apps/%d/cron", app.ID)
	var rows, editForms strings.Builder
	for _, job := range jobs {
		action := fmt.Sprintf("%s/%d", base, job.ID)
		state, next, nextLabel := `<span class="status-badge warn">Paused</span>`, "1", "Enable"
		if job.Enabled {
			state, next, nextLabel = `<span class="status-badge ok">Enabled</span>`, "0", "Pause"
		}
		fmt.Fprintf(&rows, `<tr><td><strong>%s</strong><div class="muted">#%d</div></td><td><code>%s</code></td><td><code class="table-path" title="%s">%s</code></td><td>%s</td><td><div class="actions">
			<form method="post" action="%s/run"><button class="secondary compact-action">Run now</button></form>
			<a class="secondary compact-action" href="%s/logs">Logs</a>
			<form method="post" action="%s/toggle"><input type="hidden" name="enabled" value="%s"><button class="secondary compact-action">%s</button></form>
			<button class="secondary compact-action" type="button" onclick="document.getElementById('cron-edit-%d').open=true;document.getElementById('cron-edit-%d').scrollIntoView({behavior:'smooth',block:'center'})">Edit</button>
			<form method="post" action="%s/delete" onsubmit="return confirm('Delete this scheduled task and its timer?')"><button class="danger compact-action">Delete</button></form>
			</div></td></tr>`,
			html.EscapeString(job.Name), job.ID, html.EscapeString(job.Schedule),
			html.EscapeString(job.Command), html.EscapeString(job.Command), state,
			action, action, action, next, nextLabel, job.ID, job.ID, action)
		fmt.Fprintf(&editForms, `<details id="cron-edit-%d" class="advanced-block" style="margin-top:12px"><summary class="secondary">Edit: %s</summary>
			<form method="post" action="%s/save" class="service-form-grid" style="margin-top:14px">
				<div><label>Task name</label><input name="name" maxlength="100" value="%s" required></div>
				<div><label>Cron schedule</label><input name="schedule" maxlength="150" value="%s" required></div>
				<div class="span-2"><label>Command</label><textarea name="command" rows="3" maxlength="4096" required>%s</textarea></div>
				<div class="span-2"><label class="check-row"><input type="checkbox" name="enabled" value="1"%s>Enabled</label></div>
				<div class="span-2"><button class="button">Save task</button></div>
			</form></details>`, job.ID, html.EscapeString(job.Name), action,
			html.EscapeString(job.Name), html.EscapeString(job.Schedule), html.EscapeString(job.Command), checked(job.Enabled))
	}
	table := `<p class="note">No scheduled tasks. Add a command below to run it independently of the app's web service.</p>`
	if len(jobs) > 0 {
		table = `<div class="table-scroll"><table data-no-pager="1"><thead><tr><th>Task</th><th>Schedule</th><th>Command</th><th>Configured</th><th>Actions</th></tr></thead><tbody>` +
			rows.String() + `</tbody></table></div>` + editForms.String()
	}
	phpNote := ""
	if app.Type == "php" && app.Service.PHPVersion == "7.4" {
		phpNote = `<p class="note">PHP 7.4 tasks execute inside this application's isolated Docker container.</p>`
	}
	return `<section class="panel panel-pad app-card-wide" id="cron-jobs" style="margin-top:16px">
		<div class="section-title"><div><h2>Scheduled tasks</h2><p class="note" style="margin-top:6px">Five-field cron expressions, server-local timezone. Jobs use systemd timers and run without the panel.</p></div><span class="meta-chip">` +
		fmt.Sprintf("%d tasks", len(jobs)) + `</span></div>` +
		table + phpNote +
		`<details class="advanced-block" style="margin-top:16px"><summary class="secondary">Add scheduled task</summary>
			<form method="post" action="` + base + `" class="service-form-grid" style="margin-top:14px">
				<div><label>Task name</label><input name="name" maxlength="100" placeholder="Cleanup sessions" required></div>
				<div><label>Schedule</label><input name="schedule" maxlength="150" placeholder="*/5 * * * *" required></div>
				<div class="span-2"><label>Command (app working directory)</label><textarea name="command" rows="3" maxlength="4096" placeholder="php cron.php" required></textarea></div>
				<div class="span-2"><p class="note">Examples: */5 * * * * (every 5 minutes), 0 3 * * * (daily at 03:00), @hourly. Tasks do not overlap; maximum runtime is 15 minutes.</p></div>
				<div class="span-2"><label class="check-row"><input type="checkbox" name="enabled" value="1" checked>Enabled</label></div>
				<div class="span-2"><button class="button">Create task</button></div>
			</form>
		</details>
	</section>`
}
