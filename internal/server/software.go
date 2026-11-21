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
		version, extensions := cfg.Software.PHPExtensions()
		writeHTML(w, cfg.Logger, http.StatusOK, softwarePage(cfg.Software.Items(r.Context()), cfg.Software.Task(), version, extensions, ""))
	})))

	mux.Handle("POST /software/{id}/{action}", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.PathValue("id"))
		action := strings.TrimSpace(r.PathValue("action"))
		if err := cfg.Software.Start(id, action); err != nil {
			version, extensions := cfg.Software.PHPExtensions()
			writeHTML(w, cfg.Logger, http.StatusBadRequest, softwarePage(cfg.Software.Items(r.Context()), cfg.Software.Task(), version, extensions, err.Error()))
			return
		}
		http.Redirect(w, r, "/software", http.StatusSeeOther)
	})))

	mux.Handle("POST /software/php/extensions/{extension}", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := cfg.Software.StartPHPExtension(r.PathValue("extension")); err != nil {
			version, extensions := cfg.Software.PHPExtensions()
			writeHTML(w, cfg.Logger, http.StatusBadRequest, softwarePage(cfg.Software.Items(r.Context()), cfg.Software.Task(), version, extensions, err.Error()))
			return
		}
		http.Redirect(w, r, "/software", http.StatusSeeOther)
	})))
}

func softwarePage(items []software.Item, task software.Task, phpVersion string, extensions []software.PHPExtension, message string) string {
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
		phpExtra := ""
		if item.ID == "php" && item.Installed && phpVersion != "" {
			var list strings.Builder
			for _, ext := range extensions {
				label := "Install"
				status := "not installed"
				state := "warn"
				extDisabled := disabled
				if ext.Installed {
					label = "Installed"
					status = "enabled"
					state = "ok"
					extDisabled = " disabled"
					if ext.ID == "ioncube" {
						label = "Update"
						extDisabled = disabled
					}
				}
				list.WriteString(`<div class="php-extension-row">
					<div><strong>`+html.EscapeString(ext.Name)+`</strong><span class="state-text `+state+`"><i></i>`+status+`</span></div>
					<form method="post" action="/software/php/extensions/`+html.EscapeString(ext.ID)+`">
						<button type="submit" class="secondary"`+extDisabled+`>`+label+`</button>
					</form>
				</div>`)
			}
			phpExtra = `<details id="php-extensions" class="php-extensions">
				<summary class="secondary">Manage PHP `+html.EscapeString(phpVersion)+` extensions</summary>
				<p class="note">Extensions are shared by all applications using this PHP-FPM version. Installing a new extension reloads PHP-FPM.</p>
				<div class="php-extension-grid">`+list.String()+`</div>
			</details>`
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
				` + phpExtra + `
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
		<style>
		.php-extensions{margin-top:16px;padding-top:15px;border-top:1px solid var(--border)}
		.php-extensions>summary{display:inline-flex;align-items:center;min-height:38px;padding:0 12px;font-size:12px;font-weight:700}
		.php-extensions>.note{margin:12px 0;line-height:1.5}
		.php-extension-grid{display:grid;gap:0}
		.php-extension-row{display:flex;align-items:center;justify-content:space-between;gap:12px;padding:10px 0;border-top:1px solid rgba(146,184,235,.12)}
		.php-extension-row>div{display:flex;align-items:center;flex-wrap:wrap;gap:10px;min-width:0}
		.php-extension-row strong{font-size:13px}
		.php-extension-row .state-text{font-size:11px}
		.php-extension-row form{flex:none;margin:0}
		.php-extension-row button{min-height:34px;padding:0 12px;font-size:12px}
		</style>
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
