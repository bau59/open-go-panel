package server

import (
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/bau59/open-go-panel/internal/software"
)

func registerSoftwareRoutes(mux *http.ServeMux, store *sessionStore, cfg Config) {
	mux.Handle("GET /software", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeHTML(w, cfg.Logger, http.StatusOK, softwarePage(cfg.Software.Items(r.Context()), cfg.Software.Task(), ""))
	})))

	mux.Handle("POST /software/{id}/{action}", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.PathValue("id"))
		action := strings.TrimSpace(r.PathValue("action"))
		if err := cfg.Software.Start(id, action); err != nil {
			writeHTML(w, cfg.Logger, http.StatusBadRequest, softwarePage(cfg.Software.Items(r.Context()), cfg.Software.Task(), err.Error()))
			return
		}
		http.Redirect(w, r, "/software", http.StatusSeeOther)
	})))
}

func softwarePage(items []software.Item, task software.Task, message string) string {
	alert := ""
	if message != "" {
		alert = `<div class="alert">` + html.EscapeString(message) + `</div>`
	}
	if !task.Running && task.Error != "" {
		alert += `<div class="alert">` + html.EscapeString(task.SoftwareID+" "+task.Action+": "+task.Error) + `</div>`
	}

	running := ""
	refresh := ""
	if task.Running {
		running = `
			<div class="software-task">
				<span class="status-badge warn">running</span>
				<div><strong>` + html.EscapeString(strings.Title(task.Action+" "+task.SoftwareID)) + `</strong><p class="note">The operation continues in the background. This page refreshes automatically.</p></div>
			</div>`
		refresh = `<script>setTimeout(() => location.reload(), 3000)</script>`
	}

	var cards strings.Builder
	for _, item := range items {
		statusClass := "warn"
		statusText := "not installed"
		action := "install"
		button := "Install"
		if item.Installed {
			statusClass = "ok"
			statusText = "installed"
			action = "update"
			button = "Update"
		}
		disabled := ""
		if task.Running {
			disabled = " disabled"
		}
		version := item.Version
		if version == "" {
			version = "—"
		}
		cards.WriteString(`
			<article class="panel panel-pad software-card">
				<div class="software-card-head">
					<div>
						<h2>` + html.EscapeString(item.Name) + `</h2>
						<p class="sub">` + html.EscapeString(item.Description) + `</p>
					</div>
					<span class="state-text ` + statusClass + `"><i></i>` + statusText + `</span>
				</div>
				<div class="software-version"><span>Version</span><strong>` + html.EscapeString(version) + `</strong></div>
				<p class="note software-detail">` + html.EscapeString(item.Detail) + `</p>
				<form method="post" action="/software/` + html.EscapeString(item.ID) + `/` + action + `">
					<button class="` + func() string { if item.Installed { return "secondary" }; return "button" }() + `"` + disabled + `>` + button + `</button>
				</form>
			</article>`)
	}

	return pageHead("Software") + `<body>` + appHeader("software") + `
	<main class="shell">
		<div class="page-head">
			<div>
				<p class="eyebrow">Server software</p>
				<h1>Software</h1>
				<p class="sub">Install and update shared developer tools. Open Go Panel configures the real system programs; it does not replace them.</p>
			</div>
		</div>
		` + alert + running + `
		<section class="software-grid">` + cards.String() + `</section>

		<section class="panel panel-pad" style="margin-top:16px">
			<div class="section-title">
				<div><h2>System-wide means shared binaries</h2><p class="note" style="margin:6px 0 0">Node.js, Go, Tailwind, Git and build tools are available to all Linux users through system paths.</p></div>
			</div>
			<p class="sub" style="margin:0">Docker is different: the CLI is available globally, but access to the Docker daemon is intentionally not granted to every user because membership in the <code>docker</code> group is effectively root-level access.</p>
		</section>
	</main>
	` + refresh + `
</body></html>`
}

func softwareTaskAge(task software.Task) time.Duration {
	if task.StartedAt.IsZero() {
		return 0
	}
	if task.Running {
		return time.Since(task.StartedAt)
	}
	return task.FinishedAt.Sub(task.StartedAt)
}
