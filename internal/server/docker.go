package server

import (
    "fmt"
    "html"
    "net/http"
    "strings"

    paneldocker "github.com/bau59/open-go-panel/internal/docker"
)

func registerDockerRoutes(mux *http.ServeMux, store *sessionStore, cfg Config) {
    mux.Handle("GET /docker", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        writeDockerPage(w, r, cfg, http.StatusOK, "")
    })))

    mux.Handle("POST /docker/install", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if cfg.Software == nil {
            http.Error(w, "software manager is unavailable", http.StatusServiceUnavailable)
            return
        }
        if err := cfg.Software.Start("docker", "install"); err != nil {
            writeDockerPage(w, r, cfg, http.StatusBadRequest, err.Error())
            return
        }
        http.Redirect(w, r, "/docker", http.StatusSeeOther)
    })))

    mux.Handle("POST /docker/create", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if err := r.ParseForm(); err != nil {
            http.Error(w, "invalid request", http.StatusBadRequest)
            return
        }
        if err := cfg.Docker.Create(
            r.Context(),
            r.FormValue("name"),
            r.FormValue("image"),
            r.FormValue("ports"),
            r.FormValue("autostart") == "1",
            r.FormValue("public_ports") == "1",
        ); err != nil {
            writeDockerPage(w, r, cfg, http.StatusBadRequest, err.Error())
            return
        }
        http.Redirect(w, r, "/docker", http.StatusSeeOther)
    })))
    mux.Handle("POST /docker/service/restart", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if err := cfg.Docker.RestartService(r.Context()); err != nil {
            writeDockerPage(w, r, cfg, http.StatusBadRequest, err.Error())
            return
        }
        http.Redirect(w, r, "/docker", http.StatusSeeOther)
    })))

    for _, action := range []struct {
        path string
        run  func(http.ResponseWriter, *http.Request, string) error
    }{
        {"start", func(_ http.ResponseWriter, r *http.Request, id string) error { return cfg.Docker.Start(r.Context(), id) }},
        {"stop", func(_ http.ResponseWriter, r *http.Request, id string) error { return cfg.Docker.Stop(r.Context(), id) }},
        {"restart", func(_ http.ResponseWriter, r *http.Request, id string) error { return cfg.Docker.Restart(r.Context(), id) }},
        {"delete", func(_ http.ResponseWriter, r *http.Request, id string) error { return cfg.Docker.Remove(r.Context(), id) }},
    } {
        action := action
        mux.Handle("POST /docker/{id}/"+action.path, requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            id := strings.TrimSpace(r.PathValue("id"))
            if id == "" {
                http.Error(w, "invalid container id", http.StatusBadRequest)
                return
            }
            if err := action.run(w, r, id); err != nil {
                writeDockerPage(w, r, cfg, http.StatusBadRequest, err.Error())
                return
            }
            http.Redirect(w, r, "/docker", http.StatusSeeOther)
        })))
    }

    mux.Handle("POST /docker/{id}/autostart", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        id := strings.TrimSpace(r.PathValue("id"))
        if id == "" {
            http.Error(w, "invalid container id", http.StatusBadRequest)
            return
        }
        if err := r.ParseForm(); err != nil {
            http.Error(w, "invalid request", http.StatusBadRequest)
            return
        }
        enabled := r.FormValue("enabled") == "1"
        if err := cfg.Docker.SetAutostart(r.Context(), id, enabled); err != nil {
            writeDockerPage(w, r, cfg, http.StatusBadRequest, err.Error())
            return
        }
        http.Redirect(w, r, "/docker", http.StatusSeeOther)
    })))
}

func writeDockerPage(w http.ResponseWriter, r *http.Request, cfg Config, statusCode int, message string) {
    status := cfg.Docker.Status(r.Context())
    var containers []paneldocker.Container
    if status.Installed && status.Active {
        items, err := cfg.Docker.Containers(r.Context())
        if err != nil && message == "" {
            message = err.Error()
        } else {
            containers = items
        }
    }
    task := cfg.Software.Task()
    writeHTML(w, cfg.Logger, statusCode, dockerPage(status, containers, task.Running && task.SoftwareID == "docker", message))
}

func dockerPage(status paneldocker.Status, containers []paneldocker.Container, installing bool, message string) string {
    alert := ""
    if message != "" {
        alert = `<div class="alert">` + html.EscapeString(message) + `</div>`
    }

    refresh := ""
    if installing {
        alert += `<div class="software-task"><span class="status-badge warn">installing</span><div><strong>Docker installation is running</strong><p class="note">The page refreshes automatically.</p></div></div>`
        refresh = `<script>setTimeout(() => location.reload(), 3000)</script>`
    }

    if !status.Installed {
        return pageHead("Docker") + `<body>` + appHeader("docker") + `
        <main class="shell">
            <div class="page-head"><div><p class="eyebrow">Containers</p><h1>Docker</h1><p class="sub">Install Docker Engine and manage containers through the Docker CLI.</p></div></div>
            ` + alert + `
            <section class="panel panel-pad empty-state-card">
                <h2>Docker is not installed</h2>
                <p class="sub">Open Go Panel will install Docker Engine, Buildx and the Compose plugin from Docker’s Ubuntu repository.</p>
                <form method="post" action="/docker/install" style="margin-top:18px"><button class="button"` + func() string { if installing { return " disabled" }; return "" }() + `>Install Docker</button></form>
            </section>
        </main>` + refresh + `</body></html>`
    }

    var rows strings.Builder
    runningCount := 0
    for _, item := range containers {
        stateClass := "warn"
        if item.Running {
            stateClass = "ok"
            runningCount++
        }
        autostart := item.RestartPolicy != "no"
        toggleValue := "1"
        toggleLabel := "Enable autostart"
        if autostart {
            toggleValue = "0"
            toggleLabel = "Disable autostart"
        }
        actionButton := `<form method="post" action="/docker/` + html.EscapeString(item.ID) + `/start"><button class="secondary compact-action">Start</button></form>`
        if item.Running {
            actionButton = `<form method="post" action="/docker/` + html.EscapeString(item.ID) + `/restart"><button class="secondary compact-action">Restart</button></form><form method="post" action="/docker/` + html.EscapeString(item.ID) + `/stop"><button class="secondary compact-action">Stop</button></form>`
        }
        rows.WriteString(`<tr>
            <td><strong>` + html.EscapeString(item.Name) + `</strong><div class="muted"><code>` + html.EscapeString(item.ID) + `</code></div></td>
            <td><code>` + html.EscapeString(item.Image) + `</code></td>
            <td><span class="status-badge ` + stateClass + `">` + html.EscapeString(item.State) + `</span></td>
            <td><code>` + html.EscapeString(item.Ports) + `</code></td>
            <td><span class="meta-chip">` + html.EscapeString(item.RestartPolicy) + `</span></td>
            <td><div class="actions docker-actions">` + actionButton + `
                <form method="post" action="/docker/` + html.EscapeString(item.ID) + `/autostart"><input type="hidden" name="enabled" value="` + toggleValue + `"><button class="secondary compact-action">` + toggleLabel + `</button></form>
                <form method="post" action="/docker/` + html.EscapeString(item.ID) + `/delete" onsubmit="return confirm('Remove this Docker container? Volumes are not removed.')"><button class="danger compact-action">Delete</button></form>
            </div></td>
        </tr>`)
    }
    if rows.Len() == 0 {
        rows.WriteString(`<tr><td colspan="6" class="empty">No Docker containers yet.</td></tr>`)
    }

    serviceAlert := ""
    dockerStateClass := "ok"
    dockerStateText := "running"
    createDisabled := ""
    if !status.Active {
        dockerStateClass = "warn"
        dockerStateText = "stopped"
        createDisabled = " disabled"
        serviceAlert = `<div class="alert">Docker is installed but the daemon is not available.` + func() string { if status.Error != "" { return ` ` + html.EscapeString(status.Error) }; return "" }() + `</div>`
    }

    return pageHead("Docker") + `<body>` + appHeader("docker") + `
    <main class="shell">
        <div class="page-head">
            <div><p class="eyebrow">Containers</p><h1>Docker</h1><p class="sub">Start, stop, restart, remove and configure container autostart.</p></div>
            <div class="page-control-cluster">
                <div class="service-state"><span>Docker ` + html.EscapeString(status.Version) + `</span><span class="state-text ` + dockerStateClass + `"><i></i>` + dockerStateText + `</span></div>
                <form method="post" action="/docker/service/restart"><button class="secondary">Restart Docker</button></form>
            </div>
        </div>
        ` + alert + serviceAlert + `
        <section class="metrics-grid docker-metrics" style="margin-bottom:16px">
            <div class="metric"><span>Containers</span><strong>` + fmt.Sprintf("%d", len(containers)) + `</strong><small>all containers</small></div>
            <div class="metric"><span>Running</span><strong>` + fmt.Sprintf("%d", runningCount) + `</strong><small>currently active</small></div>
        </section>
        <section class="panel panel-pad docker-create-card" style="margin-bottom:16px">
            <div class="section-title">
                <div><h2>Run container</h2><p class="note" style="margin:6px 0 0">Paste an image reference or URL. Examples: <code>nginx:latest</code>, <code>ghcr.io/org/app:latest</code> or a Docker Hub page URL.</p></div>
            </div>
            <form method="post" action="/docker/create" class="docker-create-grid">
                <div><label>Container name</label><input name="name" placeholder="my-container" required></div>
                <div><label>Image / URL</label><input name="image" placeholder="redis:7, ghcr.io/org/app:latest or Docker Hub URL" required></div>
                <div><label>Ports</label><input name="ports" placeholder="8080:80, 8443:443"></div>
                <label class="check-row docker-autostart"><input type="checkbox" name="autostart" value="1" checked><span>Autostart</span></label>
                <label class="check-row"><input type="checkbox" name="public_ports" value="1"><span>Public ports (0.0.0.0)</span></label>
                <div class="docker-create-submit"><button class="button"` + createDisabled + `>Pull & run</button></div>
            </form>
            <p class="note" style="margin-top:12px">Ports bind to 127.0.0.1 by default. Public ports may bypass UFW rules; enable only when external access is required.</p>
        </section>
        <section class="panel">
            <div class="database-list-head panel-pad"><div><h2>Containers</h2><p class="note" style="margin:6px 0 0">Autostart maps to Docker restart policy <code>unless-stopped</code>.</p></div></div>
            <div class="table-scroll"><table><thead><tr><th>Container</th><th>Image</th><th>Status</th><th>Ports</th><th>Autostart</th><th>Actions</th></tr></thead><tbody>` + rows.String() + `</tbody></table></div>
        </section>
    </main>` + refresh + `</body></html>`
}
