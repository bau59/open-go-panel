package server

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"
)

func registerRedisSlowLogRoutes(mux *http.ServeMux, store *sessionStore, cfg Config) {
	mux.Handle("GET /databases/redis/slow-queries", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
		entries, err := cfg.Databases.RedisSlowQueries(ctx)
		notice := ""
		if err != nil {
			notice = `<div class="alert">` + html.EscapeString(err.Error()) + `</div>`
		}
		var rows strings.Builder
		for _, item := range entries {
			fmt.Fprintf(&rows, `<tr>
				<td>%s</td><td>%.2f ms</td>
				<td><pre style="white-space:pre-wrap;overflow-wrap:anywhere;max-width:100%%;margin:0">%s</pre></td>
				<td>%s</td>
			</tr>`, html.EscapeString(item.Time.Format("2006-01-02 15:04:05")),
				float64(item.DurationUS)/1000, html.EscapeString(item.Command), html.EscapeString(item.Client))
		}
		if rows.Len() == 0 {
			rows.WriteString(`<tr><td colspan="4" class="empty">No Redis slow commands recorded.</td></tr>`)
		}
		page := pageHead("Redis slow commands") + `<body>` + appHeader("databases") + `
		<main class="shell">
			<div class="page-head">
				<div><p class="eyebrow">Data / Redis</p><h1>Slow commands</h1>
					<p class="sub">Latest 100 commands from Redis SLOWLOG.</p></div>
				<div class="actions"><a class="secondary" href="/databases/redis">Back to Redis</a>
					<a class="secondary" href="/databases/redis/slow-queries">Refresh</a></div>
			</div>
			` + notice + `
			<section class="panel panel-pad" style="margin-bottom:16px">
				<p class="note">Redis SLOWLOG is server-wide; it does not record the logical database index.
				Commands may contain sensitive values. Access is restricted to panel administrators.</p>
			</section>
			<section class="panel" style="overflow-x:auto">
				<table><thead><tr><th>Time</th><th>Duration</th><th>Command</th><th>Client</th></tr></thead>
				<tbody>` + rows.String() + `</tbody></table>
			</section>
		</main></body></html>`
		writeHTML(w, cfg.Logger, http.StatusOK, page)
	})))
}
