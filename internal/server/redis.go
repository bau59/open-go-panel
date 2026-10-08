package server

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bau59/open-go-panel/internal/dbmanager"
)

type redisPageData struct {
	Metrics dbmanager.RedisMetrics
	Scan    dbmanager.RedisScanResult
	Detail  *dbmanager.RedisKeyDetail
	DB      int
	Cursor  string
	Query   string
	Count   int
	Message string
}

func registerRedisRoutes(mux *http.ServeMux, store *sessionStore, cfg Config) {
	mux.Handle("GET /databases/redis", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeRedisPage(w, r, cfg, http.StatusOK, "")
	})))

	mux.Handle("POST /databases/redis/flushall", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := cfg.Databases.RedisFlushAll(r.Context()); err != nil {
			writeRedisPage(w, r, cfg, http.StatusBadRequest, err.Error())
			return
		}
		http.Redirect(w, r, "/databases/redis", http.StatusSeeOther)
	})))

	mux.Handle("POST /databases/redis/key/delete", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		db, err := strconv.Atoi(r.FormValue("db"))
		if err != nil {
			http.Error(w, "invalid Redis database", http.StatusBadRequest)
			return
		}
		if err := cfg.Databases.RedisDeleteKey(r.Context(), db, r.FormValue("key")); err != nil {
			writeRedisPage(w, r, cfg, http.StatusBadRequest, err.Error())
			return
		}
		http.Redirect(w, r, "/databases/redis?db="+strconv.Itoa(db), http.StatusSeeOther)
	})))
}

func writeRedisPage(w http.ResponseWriter, r *http.Request, cfg Config, status int, message string) {
	values := r.URL.Query()
	db, _ := strconv.Atoi(values.Get("db"))
	if db < 0 || db > 15 {
		db = 0
	}
	cursor := strings.TrimSpace(values.Get("cursor"))
	if cursor == "" {
		cursor = "0"
	}
	count, _ := strconv.Atoi(values.Get("count"))
	switch count {
	case 25, 50, 100, 200:
	default:
		count = 50
	}
	query := strings.TrimSpace(values.Get("q"))

	data := redisPageData{DB: db, Cursor: cursor, Query: query, Count: count, Message: message}
	metrics, err := cfg.Databases.RedisMetrics(r.Context())
	if err != nil {
		if data.Message == "" {
			data.Message = err.Error()
		}
		writeHTML(w, cfg.Logger, status, redisPage(data))
		return
	}
	data.Metrics = metrics

	scan, err := cfg.Databases.RedisScan(r.Context(), db, cursor, query, count)
	if err != nil {
		if data.Message == "" {
			data.Message = err.Error()
		}
	} else {
		data.Scan = scan
	}

	if key := values.Get("key"); key != "" {
		if detail, err := cfg.Databases.RedisKeyDetail(r.Context(), db, key); err == nil {
			data.Detail = &detail
		} else if data.Message == "" {
			data.Message = err.Error()
		}
	}
	writeHTML(w, cfg.Logger, status, redisPage(data))
}

func redisPage(data redisPageData) string {
	alert := ""
	if data.Message != "" {
		alert = `<div class="alert">` + html.EscapeString(data.Message) + `</div>`
	}

	ttlText := func(ttl int64) string {
		switch ttl {
		case -1:
			return "no expiry"
		case -2:
			return "missing"
		default:
			if ttl < 0 {
				return "—"
			}
			return formatDuration(time.Duration(ttl) * time.Second)
		}
	}

	var rows strings.Builder
	for _, key := range data.Scan.Keys {
		params := url.Values{}
		params.Set("db", strconv.Itoa(data.DB))
		params.Set("cursor", data.Cursor)
		params.Set("count", strconv.Itoa(data.Count))
		params.Set("key", key.Name)
		if data.Query != "" {
			params.Set("q", data.Query)
		}
		fmt.Fprintf(&rows, `
			<tr>
				<td><a href="/databases/redis?%s"><code>%s</code></a></td>
				<td><span class="badge">%s</span></td>
				<td>%s</td>
				<td>%s</td>
				<td>
					<form method="post" action="/databases/redis/key/delete" onsubmit="return confirm('Delete this Redis key?')">
						<input type="hidden" name="db" value="%d">
						<input type="hidden" name="key" value="%s">
						<button class="danger">Delete</button>
					</form>
				</td>
			</tr>`,
			html.EscapeString(params.Encode()),
			html.EscapeString(key.Name),
			html.EscapeString(key.Type),
			html.EscapeString(ttlText(key.TTLSeconds)),
			formatBytes(uint64(maxInt64(key.MemoryBytes, 0))),
			data.DB,
			html.EscapeString(key.Name),
		)
	}
	if rows.Len() == 0 {
		rows.WriteString(`<tr><td colspan="5" class="empty">No Redis keys for this scan.</td></tr>`)
	}

	var dbOptions strings.Builder
	for i := 0; i < 16; i++ {
		selectedAttr := ""
		if i == data.DB {
			selectedAttr = " selected"
		}
		fmt.Fprintf(&dbOptions, `<option value="%d"%s>db%d</option>`, i, selectedAttr, i)
	}

	next := ""
	if data.Scan.Cursor != "" && data.Scan.Cursor != "0" {
		params := url.Values{}
		params.Set("db", strconv.Itoa(data.DB))
		params.Set("cursor", data.Scan.Cursor)
		params.Set("count", strconv.Itoa(data.Count))
		if data.Query != "" {
			params.Set("q", data.Query)
		}
		next = `<a class="secondary" href="/databases/redis?` + html.EscapeString(params.Encode()) + `">Next batch</a>`
	}
	startParams := url.Values{}
	startParams.Set("db", strconv.Itoa(data.DB))
	startParams.Set("count", strconv.Itoa(data.Count))
	if data.Query != "" {
		startParams.Set("q", data.Query)
	}

	detail := ""
	if data.Detail != nil {
		truncated := ""
		if data.Detail.Truncated {
			truncated = `<span class="status-badge warn">truncated</span>`
		}
		detail = `
			<section class="panel panel-pad redis-detail" style="margin-bottom:16px">
				<div class="section-title">
					<div>
						<h2><code>` + html.EscapeString(data.Detail.Key.Name) + `</code></h2>
						<p class="note" style="margin:6px 0 0">` + html.EscapeString(data.Detail.Key.Type) + ` · TTL ` + html.EscapeString(ttlText(data.Detail.Key.TTLSeconds)) + ` · ` + formatBytes(uint64(maxInt64(data.Detail.Key.MemoryBytes, 0))) + `</p>
					</div>
					` + truncated + `
				</div>
				<pre class="security-output redis-preview">` + html.EscapeString(data.Detail.Preview) + `</pre>
			</section>`
	}

	used := "—"
	peak := "—"
	if data.Metrics.UsedMemoryBytes > 0 {
		used = formatBytes(uint64(data.Metrics.UsedMemoryBytes))
	}
	if data.Metrics.PeakMemoryBytes > 0 {
		peak = formatBytes(uint64(data.Metrics.PeakMemoryBytes))
	}

	return pageHead("Redis") + `<body>` + appHeader("databases") + `
	<main class="shell">
		<div class="page-head">
			<div>
				<p class="eyebrow">Cache / key-value store</p>
				<h1>Redis</h1>
				<p class="sub">Browse keys through Redis SCAN, inspect values, restart the service or clear the cache.</p>
			</div>
			<div class="actions">
				<a class="secondary" href="/databases">Databases</a>
				<form method="post" action="/databases/redis/restart" onsubmit="return confirm('Restart Redis? Existing client connections will be interrupted.')"><button class="secondary">Restart Redis</button></form>
			</div>
		</div>

		` + alert + `

		<section class="metrics-grid" style="margin-bottom:16px">
			<div class="metric"><span>Redis version</span><strong>` + html.EscapeString(data.Metrics.Version) + `</strong><small>uptime ` + formatDuration(time.Duration(data.Metrics.UptimeSeconds)*time.Second) + `</small></div>
			<div class="metric"><span>Memory</span><strong>` + used + `</strong><small>peak ` + peak + `</small></div>
			<div class="metric"><span>Clients</span><strong>` + fmt.Sprintf("%d", data.Metrics.ConnectedClients) + `</strong><small>connected</small></div>
			<div class="metric"><span>Keys</span><strong>` + fmt.Sprintf("%d", data.Metrics.TotalKeys) + `</strong><small>all logical databases</small></div>
		</section>

		` + detail + `

		<section class="panel">
			<form method="get" action="/databases/redis" class="toolbar redis-toolbar">
				<select name="db">` + dbOptions.String() + `</select>
				<input name="q" value="` + html.EscapeString(data.Query) + `" placeholder="Search keys or Redis glob pattern">
				<select name="count">
					<option value="25"` + selected(strconv.Itoa(data.Count), "25") + `>25 keys</option>
					<option value="50"` + selected(strconv.Itoa(data.Count), "50") + `>50 keys</option>
					<option value="100"` + selected(strconv.Itoa(data.Count), "100") + `>100 keys</option>
					<option value="200"` + selected(strconv.Itoa(data.Count), "200") + `>200 keys</option>
				</select>
				<button class="secondary">Scan</button>
			</form>
			<div class="table-scroll">
				<table data-no-pager="1">
					<thead><tr><th>Key</th><th>Type</th><th>TTL</th><th>Memory</th><th></th></tr></thead>
					<tbody>` + rows.String() + `</tbody>
				</table>
			</div>
			<div class="pager">
				<div class="pager-info">Cursor ` + html.EscapeString(data.Cursor) + ` → ` + html.EscapeString(data.Scan.Cursor) + `</div>
				<div class="pager-actions">
					<a class="secondary" href="/databases/redis?` + html.EscapeString(startParams.Encode()) + `">Start</a>
					` + next + `
				</div>
			</div>
		</section>

		<section class="panel panel-pad danger-zone" style="margin-top:16px">
			<div class="section-title">
				<div><h2>Clear Redis cache</h2><p class="note" style="margin:6px 0 0">Deletes every key from every Redis logical database using FLUSHALL ASYNC.</p></div>
				<form method="post" action="/databases/redis/flushall" onsubmit="return confirm('Delete ALL Redis keys from ALL logical databases? This cannot be undone.')"><button class="danger">Flush all keys</button></form>
			</div>
		</section>
	</main>
</body></html>`
}
