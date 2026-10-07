package server

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const defaultPageSize = 25

type pageInfo struct {
	Page       int
	PerPage    int
	Total      int
	TotalPages int
}

func parsePage(r *http.Request, key string) int {
	page, _ := strconv.Atoi(r.URL.Query().Get(key))
	if page < 1 {
		return 1
	}
	return page
}

func parsePerPage(r *http.Request) int {
	perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	switch perPage {
	case 10, 25, 50, 100:
		return perPage
	default:
		return defaultPageSize
	}
}

func paginate[T any](items []T, page, perPage int) ([]T, pageInfo) {
	if perPage < 1 {
		perPage = defaultPageSize
	}
	total := len(items)
	totalPages := 1
	if total > 0 {
		totalPages = (total + perPage - 1) / perPage
	}
	if page < 1 {
		page = 1
	}
	if page > totalPages {
		page = totalPages
	}
	start := (page - 1) * perPage
	if start > total {
		start = total
	}
	end := start + perPage
	if end > total {
		end = total
	}
	return items[start:end], pageInfo{Page: page, PerPage: perPage, Total: total, TotalPages: totalPages}
}

func pagerHTML(path, pageKey string, query url.Values, info pageInfo) string {
	if info.Total <= info.PerPage && info.Page == 1 {
		return ""
	}
	if pageKey == "" {
		pageKey = "page"
	}

	values := cloneValues(query)
	values.Del(pageKey)

	link := func(page int, label string, disabled bool) string {
		if disabled {
			return `<span class="pager-button disabled">` + html.EscapeString(label) + `</span>`
		}
		q := cloneValues(values)
		q.Set(pageKey, strconv.Itoa(page))
		href := path
		if encoded := q.Encode(); encoded != "" {
			href += "?" + encoded
		}
		return `<a class="pager-button" href="` + html.EscapeString(href) + `">` + html.EscapeString(label) + `</a>`
	}

	return `
		<div class="pager">
			<div class="pager-info">` + fmt.Sprintf("%d items · page %d of %d", info.Total, info.Page, info.TotalPages) + `</div>
			<div class="pager-actions">
				` + link(info.Page-1, "Previous", info.Page <= 1) + `
				` + link(info.Page+1, "Next", info.Page >= info.TotalPages) + `
			</div>
		</div>`
}

func cloneValues(values url.Values) url.Values {
	out := make(url.Values, len(values))
	for key, value := range values {
		out[key] = append([]string(nil), value...)
	}
	return out
}

func containsFold(parts ...string) func(string) bool {
	needle := strings.ToLower(strings.TrimSpace(parts[0]))
	return func(haystack string) bool {
		if needle == "" {
			return true
		}
		return strings.Contains(strings.ToLower(haystack), needle)
	}
}

func searchBar(path, query string, placeholder string, hidden url.Values) string {
	var fields strings.Builder
	for key, values := range hidden {
		if key == "q" || key == "page" {
			continue
		}
		for _, value := range values {
			fmt.Fprintf(&fields, `<input type="hidden" name="%s" value="%s">`, html.EscapeString(key), html.EscapeString(value))
		}
	}
	return `
		<form class="list-toolbar" method="get" action="` + html.EscapeString(path) + `">
			` + fields.String() + `
			<input name="q" value="` + html.EscapeString(query) + `" placeholder="` + html.EscapeString(placeholder) + `">
			<button class="secondary">Search</button>
			` + func() string {
				if strings.TrimSpace(query) == "" {
					return ""
				}
				return `<a class="secondary" href="` + html.EscapeString(path) + `">Clear</a>`
			}() + `
		</form>`
}
