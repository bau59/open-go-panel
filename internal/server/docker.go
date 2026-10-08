package server

import (
    "encoding/json"
    "fmt"
    "html"
    "net/http"
    "net/url"
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
        rebuildForm := `<details class="docker-row-rebuild"><summary class="secondary compact-action">Rebuild GitHub</summary>
            <form method="post" action="/docker/`+html.EscapeString(item.ID)+`/rebuild" class="docker-rebuild-form"
                onsubmit="return confirm('Build and deploy updated GitHub image? Existing volumes, ports and secrets will be reused. The previous container is kept as backup.')">
                <label>Repository<input name="repository" required placeholder="owner/repository" value="`+html.EscapeString(item.Repository)+`"></label>
                <label>Branch<input name="branch" placeholder="Default branch" value="`+html.EscapeString(item.Branch)+`"></label>
                <label>Dockerfile<input name="dockerfile" required value="`+html.EscapeString(func()string{if item.Dockerfile!=""{return item.Dockerfile};return "Dockerfile"}())+`"></label>
                <p class="note">Only switch after successful build. Existing Docker config is reused; old container is retained.</p>
                <button class="button compact-action" type="submit">Rebuild &amp; deploy</button>
            </form></details>`
        rows.WriteString(`<tr>
            <td><strong>` + html.EscapeString(item.Name) + `</strong><div class="muted"><code>` + html.EscapeString(item.ID) + `</code></div>` + func() string { if item.Commit != "" {return `<div class="muted">Git ` + html.EscapeString(item.Commit[:min(7,len(item.Commit))]) + `</div>`}; return "" }() + `</td>
            <td><code>` + html.EscapeString(item.Image) + `</code></td>
            <td><span class="status-badge ` + stateClass + `">` + html.EscapeString(item.State) + `</span></td>
            <td class="docker-usage" data-docker-id="` + html.EscapeString(item.ID) + `"><span data-field="cpu">—</span></td>
            <td class="docker-usage" data-docker-id="` + html.EscapeString(item.ID) + `"><span data-field="memory">—</span><small data-field="mem_pct"></small></td>
            <td><code>` + html.EscapeString(item.Ports) + `</code></td>
            <td><span class="meta-chip">` + html.EscapeString(item.RestartPolicy) + `</span></td>
            <td><div class="actions docker-actions">` + actionButton + rebuildForm + `
                <form method="post" action="/docker/` + html.EscapeString(item.ID) + `/autostart"><input type="hidden" name="enabled" value="` + toggleValue + `"><button class="secondary compact-action">` + toggleLabel + `</button></form>
                <form method="post" action="/docker/` + html.EscapeString(item.ID) + `/delete" onsubmit="return confirm('Remove this Docker container? Volumes are not removed.')"><button class="danger compact-action">Delete</button></form>
            </div></td>
        </tr>`)
    }
    if rows.Len() == 0 {
        rows.WriteString(`<tr><td colspan="8" class="empty">No Docker containers yet.</td></tr>`)
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
            <div class="metric"><span>Containers</span><strong>` + fmt.Sprintf("%d", len(containers)) + `</strong><small>all containers</small></div>
            <div class="metric"><span>Running</span><strong>` + fmt.Sprintf("%d", runningCount) + `</strong><small>` + fmt.Sprintf("%d",len(containers)-runningCount) + ` stopped</small></div>
            <div class="metric"><span>CPU</span><strong id="docker-total-cpu">—</strong><small>running containers</small></div>
            <div class="metric"><span>Memory</span><strong id="docker-total-memory">—</strong><small>running containers</small></div>
        </section>
                <section class="panel" id="docker-containers" style="margin-bottom:16px">
            <div class="database-list-head panel-pad"><div><h2>Containers</h2><p class="note" style="margin:6px 0 0">CPU and memory refresh every 10 seconds; rebuild reuses existing settings.</p></div>
            <a href="/docker" class="secondary compact-action">Refresh</a></div>
            <div class="table-scroll"><table><thead><tr><th>Container</th><th>Image</th><th>Status</th><th>CPU</th><th>Memory</th><th>Ports</th><th>Autostart</th><th>Actions</th></tr></thead><tbody>` + rows.String() + `</tbody></table></div>
        </section>
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
                <label class="check-row docker-autostart"><input type="checkbox" name="autostart" value="1" checked><span>Autostart</span></label>
                <label class="check-row"><input type="checkbox" name="public_ports" value="1"><span>Public ports (0.0.0.0)</span></label>
                <div class="docker-runtime-fields">
                    <div><label>Environment variables (one KEY=value per line)</label><textarea name="environment" rows="5" maxlength="65536" spellcheck="false" placeholder="BRIDGE_API_KEYS=sk-...&#10;STATE_ENCRYPTION_KEY=..."></textarea><p class="note">Stored in the container environment, not in the Docker image or build context. Avoid putting real secrets in repository files.</p>
                    <label style="margin-top:10px">Or existing .env file on the server</label><input name="environment_file" placeholder="/opt/deepseek-bridge/.env"><p class="note">Use either variables above or the existing .env file (0600 permissions). Do not rotate saved encryption keys.</p></div>
                    <div><label>Persistent mounts (one source:destination per line)</label><textarea name="volumes" rows="5" maxlength="8192" spellcheck="false" placeholder="/opt/deepseek-bridge/data:/app/data&#10;or: deepseek_data:/app/data"></textarea><p class="note">Host directories are created with restricted permissions if missing. Named Docker volumes are also supported.</p></div>
                    <div class="docker-runtime-settings"><label class="check-row"><input type="checkbox" name="init" value="1"><span>Init process (--init)</span></label><div><label>Shared memory (--shm-size)</label><input name="shm_size" placeholder="512m"></div></div>
                </div>
                <div class="docker-create-submit"><button class="button"` + createDisabled + `>Pull & run</button></div>
            </form>
            <p class="note" style="margin-top:12px">Ports bind to 127.0.0.1 by default. Public ports may bypass UFW rules; enable only when external access is required.</p>
        </details>
        <details class="panel panel-pad docker-create-card docker-fold" id="docker-add-github" style="margin-bottom:16px"` + func() string {if key.Repository!="" {return " open"};return ""}() + `>
            <summary><strong>Build from GitHub</strong><span>Clone, build and deploy from a GitHub repository</span></summary>
            <form method="post" action="/docker/github/key" class="docker-create-grid" style="margin:12px 0">
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
                <label class="check-row"><input type="checkbox" name="autostart" value="1" checked><span>Autostart</span></label>
                <label class="check-row"><input type="checkbox" name="public_ports" value="1"><span>Public ports (0.0.0.0)</span></label>
                <div class="docker-runtime-fields">
                    <div><label>Environment variables (one KEY=value per line)</label><textarea name="environment" rows="5" maxlength="65536" spellcheck="false" placeholder="BRIDGE_API_KEYS=sk-...&#10;STATE_ENCRYPTION_KEY=..."></textarea><p class="note">Stored in the container environment, not in the Docker image or build context. Avoid putting real secrets in repository files.</p>
                    <label style="margin-top:10px">Or existing .env file on the server</label><input name="environment_file" placeholder="/opt/deepseek-bridge/.env"><p class="note">Use either variables above or the existing .env file (0600 permissions). Do not rotate saved encryption keys.</p></div>
                    <div><label>Persistent mounts (one source:destination per line)</label><textarea name="volumes" rows="5" maxlength="8192" spellcheck="false" placeholder="/opt/deepseek-bridge/data:/app/data&#10;or: deepseek_data:/app/data"></textarea><p class="note">Host directories are created with restricted permissions if missing. Named Docker volumes are also supported.</p></div>
                    <div class="docker-runtime-settings"><label class="check-row"><input type="checkbox" name="init" value="1"><span>Init process (--init)</span></label><div><label>Shared memory (--shm-size)</label><input name="shm_size" placeholder="512m"></div></div>
                </div>
                <div class="docker-create-submit"><button class="button"` + createDisabled + func() string { if build.Running { return " disabled" }; return "" }() + `>Build &amp; run</button></div>
            </form>
            <p class="note" style="margin-top:12px">For private repositories: add a read-only deploy key to GitHub. To update a container already running, use Rebuild GitHub in its Actions row.</p>
        </details>
__CONTAINER_TABLE__
    </main>` + refresh + `</body></html>`
}
