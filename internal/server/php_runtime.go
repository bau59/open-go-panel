package server

import (
    "context"
    "errors"
    "fmt"
    "html"
    "net/http"
    "strconv"
    "sync"
    "time"

    panelapp "github.com/bau59/open-go-panel/internal/app"
    "github.com/bau59/open-go-panel/internal/phpfpm"
)

// Only one runtime switch can reserve a FastCGI port and mutate Caddy at once.
// Existing PHP applications and their settings are unaffected by a failed switch.
var phpRuntimeSwitchMu sync.Mutex

func ensurePHPAppRuntime(ctx context.Context, app panelapp.App) error {
    if app.Service.PHPVersion == phpfpm.LegacyVersion {
        return phpfpm.EnsureLegacy(ctx, app.ID, app.User, app.Root, app.Port)
    }
    return phpfpm.Ensure(ctx, app.ID, app.User)
}

func registerPHPRuntimeRoutes(mux *http.ServeMux, store *sessionStore, cfg Config) {
    mux.Handle("POST /apps/{id}/php-version", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
        if err != nil || id <= 0 {
            http.Error(w, "invalid PHP application ID", http.StatusBadRequest)
            return
        }
        version := r.FormValue("version")
        if version != "" && version != phpfpm.LegacyVersion {
            http.Error(w, "unsupported PHP version", http.StatusBadRequest)
            return
        }
        ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
        defer cancel()
        phpRuntimeSwitchMu.Lock()
        defer phpRuntimeSwitchMu.Unlock()
        app, err := cfg.Apps.Get(id)
        if err != nil || app.Type != "php" { http.NotFound(w, r); return }
        if err := switchPHPAppVersion(ctx, cfg, app, version); err != nil {
            writeHTML(w, cfg.Logger, http.StatusBadRequest,
                appPage(app, cfg.Apps.Status(r.Context(), app.ID), currentUnit(cfg, app.ID), err.Error(), appDomainBlock(cfg, app)))
            return
        }
        http.Redirect(w, r, fmt.Sprintf("/apps/%d", id), http.StatusSeeOther)
    })))
}

func switchPHPAppVersion(ctx context.Context, cfg Config, app panelapp.App, version string) error {
    if app.Type != "php" || (version != "" && version != phpfpm.LegacyVersion) {
        return errors.New("unsupported PHP application version")
    }
    if version == app.Service.PHPVersion {
        return ensurePHPAppRuntime(ctx, app)
    }
    port := 0
    if version == phpfpm.LegacyVersion {
        var err error
        port, err = cfg.Apps.AvailablePHP74Port()
        if err != nil { return err }
        if err := phpfpm.EnsureLegacy(ctx, app.ID, app.User, app.Root, port); err != nil {
            return err
        }
    } else if err := phpfpm.Ensure(ctx, app.ID, app.User); err != nil {
        return err
    }

    // On failure, do not remove a runtime still serving the previous version.
    cleanupCandidate := func() {
        cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
        defer cancel()
        if version == phpfpm.LegacyVersion {
            _ = phpfpm.RemoveLegacy(cleanup, app.ID)
        } else if app.Service.PHPVersion == phpfpm.LegacyVersion {
            _ = phpfpm.Remove(cleanup, app.ID)
        }
    }
    site, hasSite, err := cfg.Caddy.SiteForApp(app.ID)
    if err != nil { cleanupCandidate(); return err }
    if hasSite {
        if site.Kind != "php" || site.Template != "" {
            cleanupCandidate()
            return errors.New("PHP version switching requires a managed PHP Caddy template; reset the custom override first")
        }
        if err := cfg.Caddy.SetPHPBackend(ctx, app.ID, port); err != nil {
            cleanupCandidate()
            return fmt.Errorf("switch Caddy FastCGI upstream: %w", err)
        }
    }
    if err := cfg.Apps.SetPHPVersion(app.ID, version, port); err != nil {
        if hasSite {
            rollback, cancel := context.WithTimeout(context.Background(), 20*time.Second)
            defer cancel()
            if rollbackErr := cfg.Caddy.SetPHPBackend(rollback, app.ID, site.Port); rollbackErr != nil {
                return errors.Join(err, fmt.Errorf("Caddy rollback failed: %w", rollbackErr))
            }
        }
        cleanupCandidate()
        return err
    }

    if err := cfg.Apps.RefreshCronRunners(ctx, app.ID); err != nil {
        return fmt.Errorf("PHP runtime switched but scheduled tasks could not be refreshed: %w", err)
    }

    // Stop only the runtime that is no longer selected.
    cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
    defer cancel()
    if version == phpfpm.LegacyVersion {
        if err := phpfpm.Remove(cleanup, app.ID); err != nil {
            return fmt.Errorf("PHP 7.4 activated, but previous pool cleanup failed: %w", err)
        }
    } else if err := phpfpm.RemoveLegacy(cleanup, app.ID); err != nil {
        return fmt.Errorf("system PHP activated, but legacy container cleanup failed: %w", err)
    }
    return nil
}

func phpRuntimeBlock(app panelapp.App) string {
    if app.Type != "php" { return "" }
    systemSelected, legacySelected := " selected", ""
    current := "System PHP-FPM"
    if app.Service.PHPVersion == phpfpm.LegacyVersion {
        systemSelected, legacySelected = "", " selected"
        current = "PHP 7.4 · isolated Docker"
    }
    action := fmt.Sprintf("/apps/%d/php-version", app.ID)
    return `<section class="panel panel-pad" style="margin-top:16px" data-panel-tab="settings">
        <div class="section-title">
            <div>
                <h2>PHP runtime</h2>
                <p class="note">Selected for this application only. Other PHP sites keep their current version.</p>
            </div>
            <span class="meta-chip">` + html.EscapeString(current) + `</span>
        </div>
        <form method="post" action="` + action + `" class="compact-form" style="max-width:700px">
            <select name="version" aria-label="PHP version">
                <option value=""` + systemSelected + `>System PHP-FPM (latest installed)</option>
                <option value="7.4"` + legacySelected + `>PHP 7.4 + ionCube (isolated Docker)</option>
            </select>
            <button class="button" type="submit">Apply PHP version</button>
        </form>
        <p class="note" style="margin:12px 0 0">PHP 7.4 is no longer supported upstream. Install the Docker image and upload the official ionCube archive in <a href="/software">Software</a> first. The switch validates the new backend before changing Caddy. The built-in terminal still uses the host PHP CLI; site HTTP requests use the selected PHP-FPM runtime.</p>
    </section>`
}
