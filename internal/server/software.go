package server

import (
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/bau59/open-go-panel/internal/software"
	"github.com/bau59/open-go-panel/internal/phpfpm"
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

    mux.Handle("POST /software/php74/loader", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        task := cfg.Software.Task()
        if task.Running {
            http.Error(w, "software installation in progress", http.StatusConflict)
            return
        }
        file, header, err := r.FormFile("archive")
        if err != nil { http.Error(w, "upload official ionCube Linux archive", http.StatusBadRequest); return }
        defer file.Close()
        if header.Size == 0 || header.Size > 64<<20 {
            http.Error(w, "ionCube archive must be at most 64 MiB", http.StatusBadRequest)
            return
        }
        if err := phpfpm.InstallLegacyLoader(file); err != nil {
            version, extensions := cfg.Software.PHPExtensions()
            writeHTML(w, cfg.Logger, http.StatusBadRequest,
                softwarePage(cfg.Software.Items(r.Context()), cfg.Software.Task(), version, extensions, err.Error()))
            return
        }
        http.Redirect(w, r, "/software#php74-loader", http.StatusSeeOther)
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
		refresh = `<script>(function(){var route=location.pathname+location.search;setTimeout(function(){if(location.pathname+location.search!==route)return;if(window.htmx){window.htmx.ajax('GET',route,{target:'body',swap:'innerHTML'});}else{location.reload();}},3000);})()</script>`
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
		if item.ID == "php74" {
            loaderState := "Not uploaded"
            if phpfpm.LegacyLoaderPresent() { loaderState = "Uploaded" }
            phpExtra = `<div class="php-extensions" id="php74-loader">
                <h3>ionCube Loader for PHP 7.4</h3>
                <p class="note">Status: `+loaderState+`. Upload the official Linux ionCube archive (x86-64 or ARM64) from your computer. The panel extracts only ioncube_loader_lin_7.4.so and mounts it read-only into PHP containers. No download from the VPS is required.</p>
                <form method="post" action="/software/php74/loader" enctype="multipart/form-data" style="display:grid;gap:10px;margin-top:12px">
                    <input type="file" class="file-upload-input" name="archive" accept=".gz,.tgz" required>
                    <button class="secondary" type="submit"`+disabled+`>Upload PHP 7.4 Loader</button>
                </form>
            </div>`
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
