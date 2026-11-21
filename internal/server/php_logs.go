package server

import (
    "fmt"
    "html"
    "net/http"
    "strconv"
    "strings"

    panelapp "github.com/bau59/open-go-panel/internal/app"
    "github.com/bau59/open-go-panel/internal/phpfpm"
)

func registerPHPLogRoutes(mux *http.ServeMux, store *sessionStore, cfg Config) {
    loadApp := func(w http.ResponseWriter, r *http.Request) (panelapp.App, bool) {
        id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
        if err != nil || id <= 0 {
            http.Error(w, "invalid PHP application ID", http.StatusBadRequest)
            return panelapp.App{}, false
        }
        app, err := cfg.Apps.Get(id)
        if err != nil || app.Type != "php" {
            http.NotFound(w, r)
            return panelapp.App{}, false
        }
        return app, true
    }

    mux.Handle("GET /apps/{id}/php-logs", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        app, ok := loadApp(w, r)
        if !ok { return }
        body, err := phpfpm.ReadErrorLog(app.ID)
        if err != nil {
            cfg.Logger.Warn("read PHP error log", "app_id", app.ID, "err", err)
            writeHTML(w, cfg.Logger, http.StatusInternalServerError,
                phpErrorLogsPage(app, phpfpm.ErrorLoggingConfigured(app.ID), "", "Cannot read PHP error log: "+err.Error()))
            return
        }
        w.Header().Set("Cache-Control", "no-store")
        writeHTML(w, cfg.Logger, http.StatusOK, phpErrorLogsPage(app, phpfpm.ErrorLoggingConfigured(app.ID), body, ""))
    })))

    mux.Handle("POST /apps/{id}/php-logs/enable", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        app, ok := loadApp(w, r)
        if !ok { return }
        if err := phpfpm.Ensure(r.Context(), app.ID, app.User); err != nil {
            writeHTML(w, cfg.Logger, http.StatusBadRequest,
                phpErrorLogsPage(app, phpfpm.ErrorLoggingConfigured(app.ID), "", "Enable PHP error logging: "+err.Error()))
            return
        }
        http.Redirect(w, r, fmt.Sprintf("/apps/%d/php-logs", app.ID), http.StatusSeeOther)
    })))
}

func phpErrorLogsPage(app panelapp.App, enabled bool, logs, message string) string {
    path := fmt.Sprintf("/apps/%d/php-logs", app.ID)
    alert := ""
    if message != "" {
        alert = `<div class="alert">` + html.EscapeString(message) + `</div>`
    }
    state := `<span class="status-badge ok">Enabled</span>`
    setup := ""
    if !enabled {
        state = `<span class="status-badge warn">Setup needed</span>`
        setup = `<section class="panel panel-pad" style="margin-bottom:16px">
            <h2>Enable PHP error logging</h2>
            <p class="note">This application uses a legacy PHP-FPM pool without a dedicated error log. Enable logging to update only this pool and reload PHP-FPM. Application files are not changed.</p>
            <form method="post" action="` + path + `/enable" style="margin-top:12px">
                <button class="button">Enable PHP error log</button>
            </form>
        </section>`
    }
    content := `<div class="empty">No PHP errors recorded yet. If the site still returns HTTP 500, check Caddy logs and PHP-FPM service errors.</div>`
    if strings.TrimSpace(logs) != "" {
        content = `<pre class="logbox" style="white-space:pre-wrap;overflow-wrap:anywhere;max-height:65vh">` +
            html.EscapeString(logs) + `</pre>`
    }
    return pageHead(app.Name+" · PHP errors") + `<body>` + appHeader("apps") + `
    <main class="shell">
        <div class="page-head">
            <div>
                <p class="eyebrow">PHP / Diagnostics</p>
                <h1>` + html.EscapeString(app.Name) + ` · PHP errors</h1>
                <p class="sub">Errors and uncaught exceptions from this application's PHP-FPM pool.</p>
            </div>
            <div class="actions">
                ` + state + `
                <a class="secondary" href="/apps/` + fmt.Sprintf("%d", app.ID) + `">App</a>
                <a class="secondary" href="/apps/` + fmt.Sprintf("%d", app.ID) + `/caddy-logs">Caddy logs</a>
                <a class="button" href="` + path + `">Refresh</a>
            </div>
        </div>
        ` + alert + setup + `
        <section class="panel panel-pad">
            <div class="section-title">
                <div>
                    <h2>PHP error log</h2>
                    <p class="note">Latest 200 lines (up to 256 KiB), read on refresh. PHP errors are isolated by site and rotated by logrotate. Browser error display stays disabled.</p>
                </div>
            </div>
            ` + content + `
        </section>
    </main>
</body></html>`
}
