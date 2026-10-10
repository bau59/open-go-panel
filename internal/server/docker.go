package server

import (
    "encoding/json"
    "fmt"
    "html"
    "net/http"
    "net/url"
    "strconv"
    "strings"

    paneldocker "github.com/bau59/open-go-panel/internal/docker"
)

func registerDockerRoutes(mux *http.ServeMux, store *sessionStore, cfg Config) {
    mux.Handle("GET /docker", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        writeDockerPage(w, r, cfg, http.StatusOK, "")
    })))

    mux.Handle("GET /docker/stats", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        stats, err := cfg.Docker.Stats(r.Context())
        if err != nil {
            http.Error(w, "Docker usage statistics unavailable", http.StatusServiceUnavailable)
            return
        }
        w.Header().Set("Content-Type", "application/json; charset=utf-8")
        w.Header().Set("Cache-Control", "no-store")
        _ = json.NewEncoder(w).Encode(stats)
    })))

    mux.Handle("GET /docker/{id}/logs", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        lines := 300
        if raw := r.URL.Query().Get("tail"); raw != "" {
            n, err := strconv.Atoi(raw)
            if err != nil || n < 1 || n > 1000 {
                http.Error(w, "tail must be between 1 and 1000", http.StatusBadRequest)
                return
            }
            lines = n
        }
        w.Header().Set("Cache-Control", "no-store")
        logs, err := cfg.Docker.Logs(r.Context(), r.PathValue("id"), lines)
        if err != nil {
            http.Error(w, err.Error(), http.StatusBadGateway)
            return
        }
        w.Header().Set("Content-Type", "text/plain; charset=utf-8")
        _, _ = w.Write([]byte(logs))
    })))

    mux.Handle("POST /docker/{id}/limits", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if err := r.ParseForm(); err != nil {
            http.Error(w, "invalid form", http.StatusBadRequest)
            return
        }
        if err := cfg.Docker.UpdateLimits(r.Context(), r.PathValue("id"),
            r.FormValue("cpu"), r.FormValue("memory_mib")); err != nil {
            writeDockerPage(w, r, cfg, http.StatusBadRequest, err.Error())
            return
        }
        http.Redirect(w, r, "/docker", http.StatusSeeOther)
    })))

    mux.Handle("POST /docker/{id}/rebuild", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if err := r.ParseForm(); err != nil {
            http.Error(w, "invalid request", http.StatusBadRequest)
            return
        }
        if err := cfg.Docker.StartGitHubRebuild(r.Context(),
            r.PathValue("id"), r.FormValue("repository"),
            r.FormValue("branch"), r.FormValue("dockerfile")); err != nil {
            writeDockerPage(w, r, cfg, http.StatusBadRequest, err.Error())
            return
        }
        http.Redirect(w, r, "/docker", http.StatusSeeOther)
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
        if err := cfg.Docker.CreateConfigured(
            r.Context(),
            r.FormValue("name"),
            r.FormValue("image"),
            r.FormValue("ports"),
            r.FormValue("autostart") == "1",
            r.FormValue("public_ports") == "1",
            dockerRuntimeForm(r),
        ); err != nil {
            writeDockerPage(w, r, cfg, http.StatusBadRequest, err.Error())
            return
        }
        http.Redirect(w, r, "/docker", http.StatusSeeOther)
    })))
    mux.Handle("POST /docker/github/key", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if err := r.ParseForm(); err != nil {
            http.Error(w, "invalid form", http.StatusBadRequest)
            return
        }
        info, err := cfg.Docker.EnsureGitHubKey(r.Context(), r.FormValue("repository"))
        if err != nil {
            writeDockerPage(w, r, cfg, http.StatusBadRequest, err.Error())
            return
        }
        http.Redirect(w, r, "/docker?github_repo="+url.QueryEscape(info.Repository), http.StatusSeeOther)
    })))

    mux.Handle("POST /docker/github/build", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if err := r.ParseForm(); err != nil {
            http.Error(w, "invalid form", http.StatusBadRequest)
            return
        }
        if err := cfg.Docker.StartGitHubBuildConfigured(
            r.FormValue("name"),
            r.FormValue("repository"),
            r.FormValue("branch"),
            r.FormValue("dockerfile"),
            r.FormValue("ports"),
            r.FormValue("autostart") == "1",
            r.FormValue("public_ports") == "1",
            dockerRuntimeForm(r),
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

func dockerRuntimeForm(r *http.Request) paneldocker.RuntimeConfig {
    return paneldocker.RuntimeConfig{
        Environment:     r.FormValue("environment"),
        EnvironmentFile: r.FormValue("environment_file"),
        Volumes:         r.FormValue("volumes"),
        Init:        r.FormValue("init") == "1",
        ShmSize:     r.FormValue("shm_size"),
    }
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
    requestedRepo := r.URL.Query().Get("github_repo")
    var key paneldocker.GitHubKeyInfo
    if requestedRepo != "" {
        info, err := cfg.Docker.GitHubKey(requestedRepo)
        if err != nil && message == "" {
            message = err.Error()
        } else {
            key = info
        }
    }
    writeHTML(w, cfg.Logger, statusCode, dockerPage(status, containers, task.Running && task.SoftwareID == "docker", cfg.Docker.BuildStatus(), key, message))
}

// dockerPortDisplay makes published addresses explicit, including the
// distinction between loopback-only and publicly bound ports.
func dockerPortDisplay(value string) string {
    if value == "" || value == "—" {return `<span class="muted">No published ports</span>`}
    var out strings.Builder
    for _, raw := range strings.Split(value,", ") {
        parts := strings.SplitN(raw,"→",2)
        if len(parts)!=2 {
            out.WriteString(`<div class="docker-port"><code>`+html.EscapeString(raw)+`</code><small>Container-only / not published</small></div>`)
            continue
        }
        address := parts[0]
        target := parts[1]
        label := "Host"
        if strings.HasPrefix(address,"127.0.0.1:") || strings.HasPrefix(address,"[::1]:"){
            label = "Local only"
        } else if strings.HasPrefix(address,"*:"){
            label = "Public / all interfaces"
        }
        out.WriteString(`<div class="docker-port"><strong>`+html.EscapeString(address)+`</strong><small>`+
            html.EscapeString(label)+` → container `+html.EscapeString(target)+`</small></div>`)
    }
    return out.String()
}

func dockerPage(status paneldocker.Status, containers []paneldocker.Container, installing bool, build paneldocker.BuildTask, key paneldocker.GitHubKeyInfo, message string) string {
    alert := ""
    if message != "" {
        alert = `<div class="alert">` + html.EscapeString(message) + `</div>`
    }

    refresh := ""
    if installing {
        alert += `<div class="software-task"><span class="status-badge warn">installing</span><div><strong>Docker installation is running</strong><p class="note">The page refreshes automatically.</p></div></div>`
        refresh = `<script>setTimeout(() => location.reload(), 3000)</script>`
    }

    if build.Running {
        refresh = `<script>setTimeout(() => location.reload(), 4000)</script>`
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
    var backupRows strings.Builder
    var rebuildDialogs strings.Builder
    runningCount := 0
    backupCount := 0
    activeCount := 0
    for _, item := range containers {
        if strings.HasPrefix(item.Name, "ogp-prev-") {
            backupCount++
            backupRows.WriteString(`<tr><td><strong>`+html.EscapeString(item.Name)+`</strong><div class="muted">`+html.EscapeString(item.ID)+`</div></td><td><code>`+
                html.EscapeString(item.Image)+`</code></td><td>Retained for rollback</td><td><form method="post" action="/docker/`+
                html.EscapeString(item.ID)+`/delete" onsubmit="return confirm('Permanently remove this previous Docker container? The active container and volumes will not be deleted.')"><button class="danger compact-action">Delete backup</button></form></td></tr>`)
            continue
        }
        activeCount++
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
        dialogID := "docker-rebuild-" + item.ID
        rebuildButton := `<button class="secondary compact-action" type="button" onclick="document.getElementById('`+dialogID+`').showModal()">Rebuild</button>`
        rebuildDialogs.WriteString(`<dialog id="`+dialogID+`" class="docker-rebuild-dialog">
            <div class="docker-dialog-head"><div><h2>Rebuild `+html.EscapeString(item.Name)+`</h2>
                <p class="note">Build the newest GitHub revision, then replace the running version.</p></div>
                <button type="button" class="secondary compact-action" onclick="this.closest('dialog').close()">Close</button></div>
            <form method="post" action="/docker/`+html.EscapeString(item.ID)+`/rebuild" class="docker-rebuild-form"
                onsubmit="return confirm('Deploy the new version? Existing ports, secrets and volumes will be preserved.')">
                <label>GitHub repository<input name="repository" required placeholder="owner/repository" value="`+html.EscapeString(item.Repository)+`"></label>
                <div class="docker-dialog-grid"><label>Branch<input name="branch" placeholder="Default branch" value="`+html.EscapeString(item.Branch)+`"></label>
                <label>Dockerfile<input name="dockerfile" required value="`+html.EscapeString(func()string{if item.Dockerfile!=""{return item.Dockerfile};return "Dockerfile"}())+`"></label></div>
                <p class="note">Image builds first. The existing container is preserved as a rollback backup, hidden from the main list. Bind mounts, environment and published ports stay unchanged.</p>
                <button class="button" type="submit">Rebuild &amp; deploy</button>
            </form>
        </dialog>`)
        rows.WriteString(`<tr>
            <td><strong>` + html.EscapeString(item.Name) + `</strong><div class="muted"><code>` + html.EscapeString(item.ID) + `</code></div>` + func() string { if item.Commit != "" {return `<div class="muted">Git ` + html.EscapeString(item.Commit[:min(7,len(item.Commit))]) + `</div>`}; return "" }() + `</td>
            <td><code>` + html.EscapeString(item.Image) + `</code></td>
            <td><span class="status-badge ` + stateClass + `">` + html.EscapeString(item.State) + `</span></td>
            <td class="docker-usage" data-docker-id="` + html.EscapeString(item.ID) + `"><span data-field="cpu">—</span></td>
            <td class="docker-usage" data-docker-id="` + html.EscapeString(item.ID) + `"><span data-field="memory">—</span><small data-field="mem_pct"></small></td>
            <td>` + dockerPortDisplay(item.Ports) + `</td>
            <td><span class="meta-chip">` + html.EscapeString(item.RestartPolicy) + `</span></td>
            <td><div class="actions docker-actions">` + actionButton + rebuildButton + `
                <form method="post" action="/docker/` + html.EscapeString(item.ID) + `/autostart"><input type="hidden" name="enabled" value="` + toggleValue + `"><button class="secondary compact-action">` + toggleLabel + `</button></form>
                <form method="post" action="/docker/` + html.EscapeString(item.ID) + `/delete" onsubmit="return confirm('Remove this Docker container? Volumes are not removed.')"><button class="danger compact-action">Delete</button></form>
            </div></td>
        </tr>`)
    }
    if rows.Len() == 0 {
        rows.WriteString(`<tr><td colspan="8" class="empty">No active containers yet.</td></tr>`)
    }

    githubBuildStatus := ""
    if build.Running {
        githubBuildStatus = `<div class="software-task"><span class="status-badge warn">building</span><div><strong>` +
            html.EscapeString(build.Container) + `</strong><p class="note">` + html.EscapeString(build.Step) +
            ` — ` + html.EscapeString(build.Repository) + `. Keep the panel running until build completes.</p></div></div>`
    } else if build.Error != "" {
        githubBuildStatus = `<div class="alert"><strong>GitHub build failed:</strong> ` + html.EscapeString(build.Error) + `</div>`
    } else if build.Image != "" {
        githubBuildStatus = `<div class="software-task"><span class="status-badge ok">built</span><div><strong>` + html.EscapeString(build.Container) +
            `</strong><p class="note">Container created from <code>` + html.EscapeString(build.Image) + `</code></p></div></div>`
    }

    githubKeyCard := ""
    if key.Generated {
        githubKeyCard = `<div style="margin:12px 0"><label>Public deploy key — copy into GitHub → Repository Settings → Deploy keys (read-only)</label>
            <textarea readonly rows="3" style="width:100%;font-family:monospace">` + html.EscapeString(key.PublicKey) + `</textarea>
            <p class="note">Key is unique to <strong>` + html.EscapeString(key.Repository) +
                `</strong>. The private key stays on the server and is never included in the Docker build context.</p></div>`
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
        ` + alert + serviceAlert + githubBuildStatus + `
        <section class="metrics-grid docker-metrics" style="margin-bottom:16px">
            <div class="metric"><span>Containers</span><strong>` + fmt.Sprintf("%d", activeCount) + `</strong><small>all containers</small></div>
            <div class="metric"><span>Running</span><strong>` + fmt.Sprintf("%d", runningCount) + `</strong><small>` + fmt.Sprintf("%d",activeCount-runningCount) + ` stopped</small></div>
            <div class="metric"><span>CPU</span><strong id="docker-total-cpu">—</strong><small>running containers</small></div>
            <div class="metric"><span>Memory</span><strong id="docker-total-memory">—</strong><small>running containers</small></div>
        </section>
                <section class="panel" id="docker-containers" style="margin-bottom:16px">
            <div class="database-list-head panel-pad"><div><h2>Containers</h2><p class="note" style="margin:6px 0 0">CPU and memory refresh every 10 seconds; rebuild reuses existing settings.</p></div>
            <a href="/docker" class="secondary compact-action">Refresh</a></div>
            <div class="table-scroll"><table><thead><tr><th>Container</th><th>Image</th><th>Status</th><th>CPU</th><th>Memory</th><th>Ports</th><th>Autostart</th><th>Actions</th></tr></thead><tbody>` + rows.String() + `</tbody></table></div>
        </section>
        ` + rebuildDialogs.String() + `
        ` + func() string {if backupCount==0{return ""};return `<details class="panel docker-backups">
            <summary>Previous versions (`+fmt.Sprintf("%d",backupCount)+`) <span class="note">Kept for rollback; not active deployments</span></summary>
            <div class="table-scroll"><table><thead><tr><th>Backup container</th><th>Image</th><th>Purpose</th><th>Actions</th></tr></thead><tbody>`+backupRows.String()+`</tbody></table></div>
        </details>`}() + `
        <div class="docker-add-toolbar">
            <button type="button" class="secondary" onclick="const d=document.getElementById('docker-add-image');d.open=!d.open;if(d.open)d.scrollIntoView({behavior:'smooth',block:'start'})">+ Run container</button>
            <button type="button" class="secondary" onclick="const d=document.getElementById('docker-add-github');d.open=!d.open;if(d.open)d.scrollIntoView({behavior:'smooth',block:'start'})">+ Build from GitHub</button>
        </div>
        <details class="panel panel-pad docker-create-card docker-fold" id="docker-add-image" style="margin-bottom:16px">
            <summary><strong>Run container</strong><span>Pull an image and launch a new Docker container</span></summary>
            <form method="post" action="/docker/create" class="docker-create-grid">
                <div><label>Container name</label><input name="name" placeholder="my-container" required></div>
                <div><label>Image / URL</label><input name="image" placeholder="redis:7, ghcr.io/org/app:latest or Docker Hub URL" required></div>
                <div><label>Ports</label><input name="ports" placeholder="8080:80, 8443:443"></div>
                <div class="docker-toggle-row">
                    <label class="check-row"><input type="checkbox" name="autostart" value="1" checked><span>Autostart</span></label>
                    <label class="check-row"><input type="checkbox" name="public_ports" value="1"><span>Public ports (0.0.0.0)</span></label>
                    <label class="check-row"><input type="checkbox" name="init" value="1"><span>Init process (--init)</span></label>
                    <div class="docker-shm-field"><label>Shared memory (--shm-size)</label><input name="shm_size" placeholder="512m"></div>
                </div>
                <div class="docker-runtime-fields">
                    <div><label>Environment variables (one KEY=value per line)</label><textarea name="environment" rows="5" maxlength="65536" spellcheck="false" placeholder="BRIDGE_API_KEYS=sk-...&#10;STATE_ENCRYPTION_KEY=..."></textarea><p class="note">Stored in the container environment, not in the Docker image or build context. Avoid putting real secrets in repository files.</p>
                    <label style="margin-top:10px">Or existing .env file on the server</label><input name="environment_file" placeholder="/opt/deepseek-bridge/.env"><p class="note">Use either variables above or the existing .env file (0600 permissions). Do not rotate saved encryption keys.</p></div>
                    <div><label>Persistent mounts (one source:destination per line)</label><textarea name="volumes" rows="5" maxlength="8192" spellcheck="false" placeholder="/opt/deepseek-bridge/data:/app/data&#10;or: deepseek_data:/app/data"></textarea><p class="note">Host directories are created with restricted permissions if missing. Named Docker volumes are also supported.</p></div>
                </div>
                <div class="docker-create-submit"><button class="button"` + createDisabled + `>Pull & run</button></div>
            </form>
            <p class="note" style="margin-top:12px">Ports bind to 127.0.0.1 by default. Public ports may bypass UFW rules; enable only when external access is required.</p>
        </details>
        <details class="panel panel-pad docker-create-card docker-fold" id="docker-add-github" style="margin-bottom:16px">
            <summary><strong>Build from GitHub</strong><span>Clone, build and deploy from a GitHub repository</span></summary>
            <form method="post" action="/docker/github/key" class="docker-key-form" style="margin:16px 0 22px">
                <div><label>GitHub repository for deploy key</label><input name="repository" value="` + html.EscapeString(key.Repository) + `" placeholder="owner/repository" required></div>
                <div class="docker-create-submit"><button class="secondary" type="submit">Generate SSH deploy key</button></div>
            </form>
            ` + githubKeyCard + `
            <form method="post" action="/docker/github/build" class="docker-create-grid">
                <div><label>Container name</label><input name="name" placeholder="my-project" required></div>
                <div><label>GitHub repository / URL</label><input name="repository" placeholder="https://github.com/owner/repository" value="` + html.EscapeString(key.Repository) + `" required></div>
                <div><label>Branch (blank = default)</label><input name="branch" placeholder="main"></div>
                <div><label>Dockerfile in repository</label><input name="dockerfile" value="Dockerfile" required></div>
                <div><label>Ports</label><input name="ports" placeholder="8080:80"></div>
                <div class="docker-toggle-row">
                    <label class="check-row"><input type="checkbox" name="autostart" value="1" checked><span>Autostart</span></label>
                    <label class="check-row"><input type="checkbox" name="public_ports" value="1"><span>Public ports (0.0.0.0)</span></label>
                    <label class="check-row"><input type="checkbox" name="init" value="1"><span>Init process (--init)</span></label>
                    <div class="docker-shm-field"><label>Shared memory (--shm-size)</label><input name="shm_size" placeholder="512m"></div>
                </div>
                <div class="docker-runtime-fields">
                    <div><label>Environment variables (one KEY=value per line)</label><textarea name="environment" rows="5" maxlength="65536" spellcheck="false" placeholder="BRIDGE_API_KEYS=sk-...&#10;STATE_ENCRYPTION_KEY=..."></textarea><p class="note">Stored in the container environment, not in the Docker image or build context. Avoid putting real secrets in repository files.</p>
                    <label style="margin-top:10px">Or existing .env file on the server</label><input name="environment_file" placeholder="/opt/deepseek-bridge/.env"><p class="note">Use either variables above or the existing .env file (0600 permissions). Do not rotate saved encryption keys.</p></div>
                    <div><label>Persistent mounts (one source:destination per line)</label><textarea name="volumes" rows="5" maxlength="8192" spellcheck="false" placeholder="/opt/deepseek-bridge/data:/app/data&#10;or: deepseek_data:/app/data"></textarea><p class="note">Host directories are created with restricted permissions if missing. Named Docker volumes are also supported.</p></div>
                </div>
                <div class="docker-create-submit"><button class="button"` + createDisabled + func() string { if build.Running { return " disabled" }; return "" }() + `>Build &amp; run</button></div>
            </form>
            <p class="note" style="margin-top:12px">For private repositories: add a read-only deploy key to GitHub. To update a container already running, use Rebuild GitHub in its Actions row.</p>
        </details>
    </main>
    <script>
    (function(){
      function asBytes(s){
        const m=String(s||'').trim().match(/^([0-9.]+)\s*(B|KiB|MiB|GiB|TiB|kB|MB|GB|TB)?$/);
        if(!m)return 0;
        const units={B:1,KiB:1024,MiB:1048576,GiB:1073741824,TiB:1099511627776,kB:1000,MB:1000000,GB:1000000000,TB:1000000000000};
        return parseFloat(m[1])*(units[m[2]]||1);
      }
      async function pollStats(){
        try{
          const res=await fetch('/docker/stats',{cache:'no-store'});
          if(!res.ok)throw new Error('stats unavailable');
          const stats=await res.json();
          let cpu=0,mem=0;
          document.querySelectorAll('td.docker-usage').forEach(cell=>{
            const st=stats[cell.dataset.dockerId];
            const field=cell.querySelector('[data-field]');
            if(!field)return;
            if(!st){field.textContent='—';return}
            field.textContent=field.dataset.field==='cpu'?(st.cpu||'—'):(st.memory||'—');
            const pct=cell.querySelector('[data-field="mem_pct"]');
            if(pct)pct.textContent=st.mem_pct||'';
          });
          Object.values(stats).forEach(st=>{
            cpu+=parseFloat(st.cpu)||0;
            mem+=asBytes(String(st.memory||'').split('/')[0]);
          });
          document.getElementById('docker-total-cpu').textContent=cpu.toFixed(1)+'%';
          document.getElementById('docker-total-memory').textContent=(mem/1048576).toFixed(0)+' MiB';
        }catch(e){}
        setTimeout(pollStats,10000);
      }
      pollStats();
    })();
    </script>` + refresh + `</body></html>`
}
