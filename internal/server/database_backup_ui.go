package server

import (
    "fmt"
    "html"
    "strings"

    "github.com/bau59/open-go-panel/internal/dbmanager"
)

func databaseBackupTaskHTML(task dbmanager.BackupTask) string {
    if task.State == "" {
        return `<div id="database-backup-task" class="db-backup-task muted">No active operation.</div>`
    }
    cls := "db-backup-task"
    poll := ""
    title := "Completed"
    switch task.State {
    case "running":
        cls += " db-backup-running"
        title = "Working in background"
        poll = ` hx-get="/databases/backups/task" hx-trigger="every 8s" hx-swap="outerHTML"`
    case "failed":
        cls += " db-backup-failed"
        title = "Operation failed"
    case "success":
        cls += " db-backup-success"
    }
    detail := ""
    if task.Error != "" {
        detail = `<div class="note db-backup-error">`+html.EscapeString(task.Error)+`</div>`
    }
    start := task.StartedAt.UTC().Format("2006-01-02 15:04 UTC")
    if !task.FinishedAt.IsZero() {
        start = task.FinishedAt.UTC().Format("2006-01-02 15:04 UTC")
    }
    return `<div id="database-backup-task" class="`+cls+`" role="status"`+poll+`><strong>`+
        html.EscapeString(title)+`</strong><span>`+
        html.EscapeString(task.Description)+` · `+html.EscapeString(start)+
        `</span>`+detail+`</div>`
}

func offsiteSelected(current, value string) string {
    if current == value { return " selected" }
    return ""
}

func databaseBackupSections(data databasePageData) string {
    localLast := "Not run yet"
    if data.LastLocalDate != "" { localLast = html.EscapeString(data.LastLocalDate) }
    remoteLast := "Not run yet"
    if data.LastOffsiteDate != "" { remoteLast = html.EscapeString(data.LastOffsiteDate) }
    externalSuccess := "No completed transfer yet"
    if data.OffsiteSuccess != "" { externalSuccess = html.EscapeString(data.OffsiteSuccess) }

    provider := data.Offsite.Provider
    if provider == "" { provider = "existing" }
    options := []struct{value, label string}{
        {"existing", "Configured rclone remote"},
        {"s3", "Amazon S3 / S3-compatible"},
        {"r2", "Cloudflare R2"},
        {"b2", "Backblaze B2 (S3 API)"},
        {"sftp", "SFTP (SSH key)"},
        {"yandex", "Yandex Disk (OAuth)"},
    }
    var providerOptions strings.Builder
    for _, option := range options {
        fmt.Fprintf(&providerOptions, `<option value="%s"%s>%s</option>`,
            option.value, offsiteSelected(provider, option.value), html.EscapeString(option.label))
    }
    s3Hidden, sftpHidden, yandexHidden := "", "", ""
    if provider != "yandex" { yandexHidden = " hidden" }
    if provider != "s3" && provider != "r2" && provider != "b2" { s3Hidden = " hidden" }
    if provider != "sftp" { sftpHidden = " hidden" }
    port := data.Offsite.Port
    if port == 0 { port = 22 }
    enabled := checked(data.OffsiteSchedule.Enabled)
    externalConfigured := data.BackupRemote != ""
    buttonsDisabled := ""
    if !externalConfigured { buttonsDisabled = " disabled" }

    return `
        <section class="panel panel-pad db-backup-section" id="db-local-backups">
            <div class="section-title"><div>
                <p class="eyebrow">1 · Local storage</p>
                <h2>Local backup history</h2>
                <p class="note">Create compressed SQL snapshots on this server. Each database keeps its own versions. External storage failures never affect local retention.</p>
            </div></div>
            <div class="db-backup-destination">
                <span>Directory</span><code>/var/lib/open-go-panel/backups/databases/</code>
                <span>Last completed daily run</span><strong>`+localLast+`</strong>
            </div>
            <form method="post" action="/databases/backups/schedule" class="backup-settings-grid">
                <label class="check-row backup-enabled"><input type="checkbox" name="enabled" value="1"`+checked(data.Schedule.Enabled)+`><span>Automatic daily snapshots</span></label>
                <div><label>Start after (UTC hour)</label><input type="number" name="hour_utc" min="0" max="23" value="`+fmt.Sprintf("%d", data.Schedule.HourUTC)+`" required></div>
                <div><label>Versions per database</label><input type="number" name="keep" min="1" max="100" value="`+fmt.Sprintf("%d", data.Schedule.Keep)+`" required></div>
                <div class="backup-save"><button class="button">Save local schedule</button></div>
            </form>
            <p class="note">Manual “Backup now” runs separately, keeps the same version limit and never starts an external upload.</p>
        </section>

        <section class="panel panel-pad db-backup-section" id="db-offsite-backups">
            <div class="section-title"><div>
                <p class="eyebrow">2 · External storage</p>
                <h2>Off-site backup export</h2>
                <p class="note">Transfer an existing local snapshot to a separate storage destination. Connection and upload schedule are independent of local history.</p>
            </div></div>
            <div class="db-backup-destination">
                <span>Destination</span><code>`+html.EscapeString(func()string{if data.BackupRemote==""{return "Not configured"};return data.BackupRemote}())+`</code>
                <span>Last completed daily run</span><strong>`+remoteLast+`</strong>
                <span>Last successful upload</span><strong>`+externalSuccess+`</strong>
            </div>

            <details class="db-offsite-connection" open>
                <summary>Connection settings</summary>
                <form method="post" action="/databases/backups/offsite/connection" class="db-offsite-form">
                    <label>Storage provider
                        <select name="provider" id="db-offsite-provider">`+providerOptions.String()+`</select>
                    </label>
                    <label>Destination (remote:bucket/path for existing, bucket/path for S3, folder/path for SFTP or Yandex Disk)
                        <input name="destination" value="`+html.EscapeString(data.Offsite.Destination)+`" autocomplete="off" placeholder="backup-bucket/my-server">
                    </label>
                    <div class="db-offsite-fields" data-offsite-yandex`+yandexHidden+`>
                        <label>Yandex Disk access
                            <select name="yandex_scope"><option value="app"`+offsiteSelected(data.Offsite.YandexScope,"app")+`>Application folder (restricted)</option><option value="disk"`+offsiteSelected(data.Offsite.YandexScope,"disk")+`>Entire Disk (selected folder)</option></select>
                        </label>
                        <label>Yandex Disk OAuth token
                            <input name="oauth_token" type="password" autocomplete="new-password" placeholder="Leave blank to retain saved token">
                        </label>
                        <p class="note">Archives are split into 900 MiB parts. The manifest is published after all parts are uploaded.</p>
                    </div>
                    <div class="db-offsite-fields" data-offsite-s3`+s3Hidden+`>
                        <label>HTTPS S3 API endpoint (required for R2 / B2)
                            <input name="endpoint" value="`+html.EscapeString(data.Offsite.Endpoint)+`" placeholder="https://account.r2.cloudflarestorage.com" autocomplete="off">
                        </label>
                        <label>Region (optional)
                            <input name="region" value="`+html.EscapeString(data.Offsite.Region)+`" placeholder="auto or eu-west-1" autocomplete="off">
                        </label>
                        <label>Access key ID
                            <input name="access_key" type="password" autocomplete="new-password" placeholder="Leave blank to retain saved key">
                        </label>
                        <label>Secret access key
                            <input name="secret_key" type="password" autocomplete="new-password" placeholder="Leave blank to retain saved secret">
                        </label>
                    </div>
                    <div class="db-offsite-fields" data-offsite-sftp`+sftpHidden+`>
                        <label>SFTP host
                            <input name="host" value="`+html.EscapeString(data.Offsite.Host)+`" placeholder="backup.example.com" autocomplete="off">
                        </label>
                        <label>Port
                            <input name="port" type="number" min="1" max="65535" value="`+fmt.Sprintf("%d", port)+`">
                        </label>
                        <label>SSH username
                            <input name="user" value="`+html.EscapeString(data.Offsite.User)+`" placeholder="backups" autocomplete="off">
                        </label>
                        <label>Private SSH key path (server-side)
                            <input name="key_file" value="`+html.EscapeString(data.Offsite.KeyFile)+`" placeholder="/root/.ssh/backup_ed25519" autocomplete="off">
                        </label>
                        <label>Verified known_hosts file (required)
                            <input name="known_hosts_file" value="`+html.EscapeString(data.Offsite.KnownHostsFile)+`" placeholder="/root/.ssh/known_hosts" autocomplete="off">
                        </label>
                    </div>
                    <div class="db-offsite-actions">
                        <button class="button">Save connection</button>
                        <span class="note">Managed credentials are stored in a root-only 0600 credential file, never in SQLite. Empty destination disables exports.</span>
                    </div>
                </form>
            </details>

            <div class="db-offsite-operation">
                <h3>Automatic export</h3>
                <form method="post" action="/databases/backups/offsite/schedule" class="backup-settings-grid">
                    <label class="check-row backup-enabled"><input type="checkbox" name="enabled" value="1"`+enabled+`><span>Upload daily</span></label>
                    <div><label>Start after (UTC hour)</label><input name="hour_utc" type="number" min="0" max="23" value="`+fmt.Sprintf("%d",data.OffsiteSchedule.HourUTC)+`" required></div>
                    <div class="backup-save"><button class="button">Save export schedule</button></div>
                </form>
                <p class="note">The latest available local version of each database is uploaded. Remote copies are not pruned automatically; local version retention is independent.</p>
                <div class="actions db-offsite-actions">
                    <form method="post" action="/databases/backups/offsite/test"><button class="secondary"`+buttonsDisabled+`>Test connection</button></form>
                    <form method="post" action="/databases/backups/offsite/run"><button class="secondary"`+buttonsDisabled+`>Upload latest snapshots now</button></form>
                </div>
            </div>
        </section>
        <section class="panel panel-pad db-backup-section">
            <div class="section-title"><div><h2>Backup / export activity</h2>
                <p class="note">Long-running operations continue after you leave this page. One database maintenance operation runs at a time.</p></div></div>
            `+databaseBackupTaskHTML(data.BackupTask)+`
        </section>`
}

