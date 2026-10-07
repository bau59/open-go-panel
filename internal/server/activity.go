package server

import (
	"fmt"
	"html"
	"net/http"
	"strings"

	"github.com/bau59/open-go-panel/internal/state"
)

func registerActivityRoutes(mux *http.ServeMux, store *sessionStore, cfg Config) {
	mux.Handle("GET /activity", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entries, err := cfg.State.RecentAudit(r.Context(), 200)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeHTML(w, cfg.Logger, http.StatusOK, activityPage(entries))
	})))
}

func activityPage(entries []state.AuditEntry) string {
	var rows strings.Builder
	for _, entry := range entries {
		fmt.Fprintf(&rows, `
			<tr>
				<td><code>%s</code></td>
				<td><strong>%s</strong></td>
				<td><code>%s</code></td>
				<td>%s</td>
			</tr>`,
			html.EscapeString(entry.CreatedAt),
			html.EscapeString(entry.Action),
			html.EscapeString(entry.Target),
			html.EscapeString(entry.Details),
		)
	}
	if rows.Len() == 0 {
		rows.WriteString(`<tr><td colspan="4" class="empty">No recorded changes yet.</td></tr>`)
	}

	return pageHead("Activity") + `<body>` + appHeader("activity") + `
	<main class="shell">
		<div class="page-head">
			<div>
				<p class="eyebrow">Audit</p>
				<h1>Activity</h1>
				<p class="sub">Successful state-changing actions performed through Open Go Panel. Form contents and passwords are never stored in this log.</p>
			</div>
		</div>
		<section class="panel">
			<table>
				<thead><tr><th>Time UTC</th><th>Action</th><th>Target</th><th>Details</th></tr></thead>
				<tbody>` + rows.String() + `</tbody>
			</table>
		</section>
	</main>
</body></html>`
}
