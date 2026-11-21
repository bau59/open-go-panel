package server

import (
    "context"
    "errors"
    "fmt"
    "html"
    "net/http"
    "strconv"
    "strings"
    "sync"
    "time"

    panelapp "github.com/bau59/open-go-panel/internal/app"
    "github.com/bau59/open-go-panel/internal/phpfpm"
)

var phpVersionSwitchMu sync.Mutex

func registerPHPVersionRoutes(mux *http.ServeMux, sessions *sessionStore, cfg Config) {
    mux.Handle("POST /apps/{id}/php-version", requireAuth(sessions, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
        if err != nil || id <= 0 {
            http.Error(w, "invalid app ID", http.StatusBadRequest)
            return
        }
        if err := r.ParseForm(); err != nil {
            http.Error(w, "invalid form", http.StatusBadRequest)
            return
        }
        version := strings.TrimSpace(r.FormValue("version"))
        // Only detected PHP-FPM versions can be selected, including a default
        // that preserves historical PHP 8.3 behavior for existing apps.
        if _, err := phpfpm.DetectVersion(version); err != nil {
            http.Error(w, err.Error(), http.StatusBadRequest)
            return
        }
        phpVersionSwitchMu.Lock()
        defer phpVersionSwitchMu.Unlock()

        app, err := cfg.Apps.Get(id)
        if err != nil || app.Type != "php" { http.NotFound(w, r); return }
        ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
        defer cancel()
        if err := switchPHPVersion(ctx, cfg, app, version); err != nil {
            writeHTML(w, cfg.Logger, http.StatusBadRequest,
                appPage(app, cfg.Apps.Status(r.Context(), app.ID), currentUnit(cfg, app.ID), err.Error(), appDomainBlock(cfg, app)))
            return
        }
        http.Redirect(w, r, fmt.Sprintf("/apps/%d", app.ID), http.StatusSeeOther)
    })))
}

func switchPHPVersion(ctx context.Context, cfg Config, app panelapp.App, version string) error {
    if app.Type != "php" { return errors.New("application is not PHP") }
    if cfg.Apps == nil || cfg.Caddy == nil {
        return errors.New("PHP app and Caddy managers must be available")
    }
    site, hasSite, err := cfg.Caddy.SiteForApp(app.ID)
    if err != nil { return err }
    if hasSite {
        if site.Kind != "php" { return errors.New("application domain is not a PHP site") }
        if strings.TrimSpace(site.Template) != "" {
            return errors.New("PHP runtime switching requires the managed Caddy template; remove the custom site override first")
        }
    }
    if app.Service.PHPVersion == version {
        return phpfpm.EnsureVersion(ctx, app.ID, app.User, version)
    }

    // Stage first while the previous PHP-FPM pool still serves requests.
    if err := phpfpm.EnsureVersion(ctx, app.ID, app.User, version); err != nil {
        return fmt.Errorf("prepare PHP runtime: %w", err)
    }
    cleanupCandidate := func() {
        cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
        defer cancel()
        _ = phpfpm.RemoveVersion(cleanup, app.ID, version)
    }
    old := app.Service.PHPVersion
    if err := cfg.Apps.SetPHPVersion(app.ID, version); err != nil {
        cleanupCandidate()
        return err
    }

    if hasSite {
        if err := cfg.Caddy.ReloadSites(ctx); err != nil {
            rollbackErr := cfg.Apps.SetPHPVersion(app.ID, old)
            cleanupCandidate()
            return errors.Join(fmt.Errorf("Caddy PHP routing failed: %w", err), rollbackErr)
        }
    }

    // Cleanup does not undo a successful switch: the new socket is live and
    // both Caddy and the application now use it. A failed cleanup is reported.
    cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
    defer cancel()
    if err := phpfpm.RemoveVersion(cleanup, app.ID, old); err != nil {
        return fmt.Errorf("PHP switched successfully but previous pool cleanup failed: %w", err)
    }
    return nil
}

func phpVersionBlock(app panelapp.App) string {
    if app.Type != "php" { return "" }
    installed, err := phpfpm.Installed()
    if err != nil {
        return `<section class="panel panel-pad" style="margin-top:16px"><h2>PHP version</h2><div class="alert">` + html.EscapeString(err.Error()) + `</div></section>`
    }
    latest := "none"
    if len(installed) > 0 { latest = installed[0].Version }
    option := func(value, label string, disabled bool) string {
        attrs := ""
        if value == app.Service.PHPVersion { attrs += " selected" }
        if disabled { attrs += " disabled" }
        return `<option value="` + html.EscapeString(value) + `"` + attrs + `>` + html.EscapeString(label) + `</option>`
    }
    var choices strings.Builder
    choices.WriteString(option("", "Default PHP-FPM (latest installed: "+latest+")", len(installed) == 0))
    legacyFound := false
    for _, item := range installed {
        if item.Version == "7.4" { legacyFound = true }
        choices.WriteString(option(item.Version, "PHP "+item.Version+" · dedicated FPM pool", false))
    }
    if !legacyFound {
        choices.WriteString(option("7.4", "PHP 7.4 · not installed", true))
    }
    current := "Default PHP-FPM " + latest
    if app.Service.PHPVersion != "" { current = "PHP " + app.Service.PHPVersion }
    return `<section class="panel panel-pad" style="margin-top:16px">
        <div class="section-title">
            <div><h2>PHP version</h2><p class="note">Each application has its own PHP-FPM pool, Linux user and Unix socket. Other sites are not changed.</p></div>
            <span class="meta-chip">` + html.EscapeString(current) + `</span>
        </div>
        <form method="post" action="/apps/` + strconv.FormatInt(app.ID, 10) + `/php-version" class="compact-form" style="max-width:720px">
            <select name="version" aria-label="PHP version">` + choices.String() + `</select>
            <button class="button">Apply PHP version</button>
        </form>
        <p class="note" style="margin:12px 0 0">PHP 7.4 requires the php7.4-fpm service and a compatible ionCube Loader. Install PHP packages via Software or a trusted APT source. PHP 7.4 is end-of-life; account permissions are not a chroot or complete sandbox.</p>
    </section>`
}
