package server

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bau59/open-go-panel/internal/dbmanager"
)

func registerDatabaseSlowLogRoutes(mux *http.ServeMux, store *sessionStore, cfg Config) {
	mux.Handle("GET /databases/{id}/slow-queries", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id < 1 {
			http.Error(w, "invalid database id", http.StatusBadRequest)
			return
		}
		db, err := cfg.Databases.Get(id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		defer cancel()
		info, entries, err := cfg.Databases.SlowQueries(ctx, id)
		message := ""
		if err != nil {
			message = err.Error()
		}
		writeHTML(w, cfg.Logger, http.StatusOK, databaseSlowLogPage(db, info, entries, message))
	})))

	mux.Handle("POST /databases/{id}/slow-queries/settings", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id < 1 {
			http.Error(w, "invalid database id", http.StatusBadRequest)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		threshold, err := strconv.Atoi(r.FormValue("threshold_ms"))
		if err != nil {
			http.Error(w, "invalid threshold", http.StatusBadRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		if err := cfg.Databases.ConfigureSlowQueries(ctx, id, threshold); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/databases/%d/slow-queries", id), http.StatusSeeOther)
	})))
}

func databaseSlowLogPage(db dbmanager.Database, info dbmanager.SlowLogInfo, entries []dbmanager.SlowQuery, message string) string {
	var rows strings.Builder
	for _, entry := range entries {
		notes := ""
		if strings.TrimSpace(entry.Rows) != "" {
			notes = `<div class="note">Rows examined: ` + html.EscapeString(entry.Rows) + `</div>`
		}
		fmt.Fprintf(&rows, `<tr>
			<td style="white-space:nowrap">%s</td>
			<td><strong>%.2f ms</strong>%s</td>
			<td><pre style="white-space:pre-wrap;overflow-wrap:anywhere;max-width:100%%;margin:0">%s</pre></td>
		</tr>`, html.EscapeString(entry.Time), entry.DurationMS, notes, html.EscapeString(entry.Detail))
	}
	if rows.Len() == 0 {
		rows.WriteString(`<tr><td colspan="3" class="empty">No slow queries recorded for this database in the available log window.</td></tr>`)
	}
	alert := ""
	if message != "" {
		alert = `<div class="alert">` + html.EscapeString(message) + `</div>`
	}
	notice := ""
	if info.Notice != "" {
		notice = `<p class="note" style="margin:10px 0 0">` + html.EscapeString(info.Notice) + `</p>`
	}
	controls := ""
	currentThreshold := strconv.Itoa(info.ThresholdMS)
	if currentThreshold == "0" { currentThreshold = "1000" }
	if db.Engine == "mysql" {
		controls = `<form method="post" action="/databases/` + fmt.Sprintf("%d", db.ID) + `/slow-queries/settings" class="compact-form">
			<select name="threshold_ms" aria-label="Slow query threshold">
				<option value="500"` + selected(currentThreshold, "500") + `>500 ms</option>
				<option value="1000"` + selected(currentThreshold, "1000") + `>1 second</option>
				<option value="2000"` + selected(currentThreshold, "2000") + `>2 seconds</option>
				<option value="5000"` + selected(currentThreshold, "5000") + `>5 seconds</option>
				<option value="10000"` + selected(currentThreshold, "10000") + `>10 seconds</option>
				<option value="0">Disable MySQL slow log (all databases)</option>
			</select>
			<button class="secondary" type="submit">Apply MySQL slow-log settings</button>
		</form>
		<p class="note">MySQL slow-log settings are server-wide, not per database. Enabling the table output retains the file log. Changes persist across restarts.</p>`
	} else if db.Engine == "postgres" {
		controls = `<form method="post" action="/databases/` + fmt.Sprintf("%d", db.ID) + `/slow-queries/settings" class="compact-form">
			<select name="threshold_ms" aria-label="Slow query threshold">
				<option value="500"` + selected(currentThreshold, "500") + `>500 ms</option>
				<option value="1000"` + selected(currentThreshold, "1000") + `>1 second</option>
				<option value="2000"` + selected(currentThreshold, "2000") + `>2 seconds</option>
				<option value="5000"` + selected(currentThreshold, "5000") + `>5 seconds</option>
				<option value="10000"` + selected(currentThreshold, "10000") + `>10 seconds</option>
				<option value="0">Disable for this database</option>
			</select>
			<button class="secondary" type="submit">Save PostgreSQL threshold</button>
		</form>
		<p class="note">PostgreSQL logging is configured per database. Existing connections must reconnect to receive the new threshold.</p>`
	}
	return pageHead("Slow queries") + `<body>` + appHeader("databases") + `
	<main class="shell">
		<div class="page-head">
			<div>
				<p class="eyebrow">Data / ` + html.EscapeString(db.Engine) + `</p>
				<h1>Slow queries: ` + html.EscapeString(db.Name) + `</h1>
				<p class="sub">Last 100 recorded slow queries, filtered by database.</p>
			</div>
			<div class="actions">
				<a class="secondary" href="/databases">All databases</a>
				<a class="secondary" href="/databases/` + fmt.Sprintf("%d", db.ID) + `/slow-queries">Refresh</a>
			</div>
		</div>
		` + alert + `
		<section class="panel panel-pad" style="margin-bottom:16px">
			<div class="section-title">
				<div><h2>Capture settings</h2><p class="note">Source: ` + html.EscapeString(info.Source) +
				` · Threshold: ` + html.EscapeString(info.Threshold) + `</p></div>
			</div>
			` + controls + notice + `
		</section>
		<section class="panel" style="margin-bottom:16px">
			<div class="panel-pad"><div class="section-title" style="margin-bottom:0">
				<div><h2>Recent slow queries</h2><p class="note">Query text may contain credentials or private data; visible to panel administrators only.</p></div>
			</div></div>
			<div style="overflow-x:auto"><table>
				<thead><tr><th>Time</th><th>Duration</th><th>Query</th></tr></thead>
				<tbody>` + rows.String() + `</tbody>
			</table></div>
		</section>
	</main></body></html>`
}
