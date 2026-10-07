package server

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bau59/open-go-panel/internal/journal"
)

type logFilters struct {
	Search  string
	Period  string
	From    string
	To      string
	Page    int
	PerPage int
}

func parseLogFilters(r *http.Request) (logFilters, journal.Query) {
	values := r.URL.Query()
	f := logFilters{
		Search: strings.TrimSpace(values.Get("q")),
		Period: strings.TrimSpace(values.Get("period")),
		From:   strings.TrimSpace(values.Get("from")),
		To:     strings.TrimSpace(values.Get("to")),
		Page:   parsePage(r, "page"),
	}
	if f.Period == "" {
		f.Period = "24h"
	}
	perPage, _ := strconv.Atoi(values.Get("per_page"))
	switch perPage {
	case 25, 50, 100, 200:
		f.PerPage = perPage
	default:
		f.PerPage = 100
	}

	query := journal.Query{
		Search:  f.Search,
		Page:    f.Page,
		PerPage: f.PerPage,
	}
	switch f.Period {
	case "1h":
		query.Since = "1 hour ago"
	case "6h":
		query.Since = "6 hours ago"
	case "24h":
		query.Since = "24 hours ago"
	case "7d":
		query.Since = "7 days ago"
	case "30d":
		query.Since = "30 days ago"
	case "all":
	case "custom":
		query.Since = parseJournalTime(f.From)
		query.Until = parseJournalTime(f.To)
	default:
		f.Period = "24h"
		query.Since = "24 hours ago"
	}
	return f, query
}

func parseJournalTime(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if t, err := time.Parse("2006-01-02T15:04", value); err == nil {
		return t.Format("2006-01-02 15:04:05")
	}
	return ""
}

func logToolbar(path string, filters logFilters, extra url.Values) string {
	hidden := strings.Builder{}
	for key, values := range extra {
		for _, value := range values {
			fmt.Fprintf(&hidden, `<input type="hidden" name="%s" value="%s">`, html.EscapeString(key), html.EscapeString(value))
		}
	}
	option := func(value, label string) string {
		selectedAttr := ""
		if filters.Period == value {
			selectedAttr = " selected"
		}
		return `<option value="` + value + `"` + selectedAttr + `>` + label + `</option>`
	}
	customStyle := "display:none"
	if filters.Period == "custom" {
		customStyle = ""
	}
	return `
		<form class="log-toolbar" method="get" action="` + html.EscapeString(path) + `">
			` + hidden.String() + `
			<input name="q" value="` + html.EscapeString(filters.Search) + `" placeholder="Search log text">
			<select name="period" onchange="this.form.submit()">
				` + option("1h", "Last hour") + option("6h", "Last 6 hours") + option("24h", "Last 24 hours") + option("7d", "Last 7 days") + option("30d", "Last 30 days") + option("all", "All retained") + option("custom", "Custom period") + `
			</select>
			<input type="datetime-local" name="from" value="` + html.EscapeString(filters.From) + `" style="` + customStyle + `">
			<input type="datetime-local" name="to" value="` + html.EscapeString(filters.To) + `" style="` + customStyle + `">
			<button class="secondary">Apply</button>
		</form>`
}

func logPagerHTML(path string, filters logFilters, extra url.Values, hasNext bool) string {
	values := cloneValues(extra)
	if filters.Search != "" {
		values.Set("q", filters.Search)
	}
	values.Set("period", filters.Period)
	if filters.From != "" {
		values.Set("from", filters.From)
	}
	if filters.To != "" {
		values.Set("to", filters.To)
	}
	values.Set("per_page", strconv.Itoa(filters.PerPage))

	link := func(page int, label string, disabled bool) string {
		if disabled {
			return `<span class="pager-button disabled">` + label + `</span>`
		}
		q := cloneValues(values)
		q.Set("page", strconv.Itoa(page))
		return `<a class="pager-button" href="` + html.EscapeString(path+"?"+q.Encode()) + `">` + label + `</a>`
	}
	return `
		<div class="pager">
			<div class="pager-info">Page ` + strconv.Itoa(filters.Page) + ` · ` + strconv.Itoa(filters.PerPage) + ` lines per page</div>
			<div class="pager-actions">
				` + link(filters.Page-1, "Newer", filters.Page <= 1) + `
				` + link(filters.Page+1, "Older", !hasNext) + `
			</div>
		</div>`
}

func logLinesHTML(lines []string) string {
	if len(lines) == 0 {
		return `<div class="empty">No log entries for this filter.</div>`
	}
	var b strings.Builder
	for _, line := range lines {
		b.WriteString(`<span class="log-line">` + html.EscapeString(line) + `</span>`)
	}
	return b.String()
}
