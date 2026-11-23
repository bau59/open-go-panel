package server

import (
	"html"
	"strings"
)

func pageHead(title string) string {
	return `<!doctype html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>` + html.EscapeString(title) + ` · Open Go Panel</title>
	<meta name="htmx-config" content='{"historyCacheSize":0,"refreshOnHistoryMiss":false,"selfRequestsOnly":true}'>
	<link rel="stylesheet" href="/assets/panel.css">
	<script defer src="/assets/htmx.min.js"></script>
	<script defer src="/assets/panel.js"></script>
</head>`
}


func appHeader(active string) string {
	type menuItem struct { key, href, label string }
	icon := func(key string) string {
		var shape string
		switch key {
		case "overview":
			shape = `<rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/>`
		case "apps":
			shape = `<rect x="3" y="4" width="18" height="16" rx="3"/><path d="M3 9h18M8 15h4"/>`
		case "databases":
			shape = `<ellipse cx="12" cy="5.5" rx="8" ry="3"/><path d="M4 5.5v13c0 4 16 4 16 0v-13M4 12c0 4 16 4 16 0"/>`
		case "docker":
			shape = `<rect x="3" y="9" width="5" height="5" rx="1"/><rect x="9.5" y="9" width="5" height="5" rx="1"/><rect x="9.5" y="3" width="5" height="5" rx="1"/><rect x="16" y="9" width="5" height="5" rx="1"/><path d="M3 17c2 3 6 4 9 4s7-1 9-6"/>`
		case "caddy":
			shape = `<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3c-5 5-5 13 0 18M12 3c5 5 5 13 0 18"/>`
		case "users":
			shape = `<circle cx="9" cy="8" r="3"/><path d="M3 20v-2a6 6 0 0 1 12 0v2M17 5a3 3 0 0 1 0 6M18 14a5 5 0 0 1 3 5v1"/>`
		case "backup":
			shape = `<path d="M4 4h16v16H4zM8 8h8M8 12h8M8 16h5"/>`
		case "software":
			shape = `<rect x="6" y="6" width="12" height="12" rx="2"/><path d="M9 2v4M15 2v4M9 18v4M15 18v4M2 9h4M2 15h4M18 9h4M18 15h4"/>`
		case "terminal":
			shape = `<rect x="2" y="4" width="20" height="16" rx="3"/><path d="m6 9 4 3-4 3M12 16h5"/>`
		case "performance":
			shape = `<path d="M3 19V5M3 19h18M6 15l5-6 4 3 5-7"/>`
		case "log-retention":
			shape = `<path d="M6 3h9l4 4v14H6a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2ZM15 3v5h4M8 12h8M8 16h8"/>`
		case "activity":
			shape = `<circle cx="12" cy="12" r="9"/><path d="M12 7v5l4 3"/>`
		case "security":
			shape = `<path d="m12 2 8 4v6c0 5-3.5 8.5-8 10-4.5-1.5-8-5-8-10V6l8-4Z"/><path d="m9 12 2 2 4-4"/>`
		}
		return `<span class="nav-icon" aria-hidden="true"><svg viewBox="0 0 24 24">` + shape + `</svg></span>`
	}
	link := func(item menuItem) string {
		class := ""
		if item.key == active { class = ` class="active" aria-current="page"` }
		return `<a` + class + ` href="` + item.href + `">` + icon(item.key) + `<span>` + item.label + `</span></a>`
	}
	group := func(title string, children ...menuItem) string {
		current := false
		var contents strings.Builder
		for _, item := range children {
			if item.key == active { current = true }
			contents.WriteString(link(item))
		}
		openClass := ""
		if current { openClass = ` class="current" open` }
		return `<details` + openClass + `><summary>` + title + `</summary><div class="nav-dropdown">` +
			contents.String() + `</div></details>`
	}
	var nav strings.Builder
	for _, item := range []menuItem{
		{"overview", "/", "Overview"},
		{"apps", "/apps", "Apps"},
		{"databases", "/databases", "Databases"},
		{"docker", "/docker", "Docker"},
	} { nav.WriteString(link(item)) }
	nav.WriteString(group("Infrastructure",
		menuItem{"caddy", "/caddy", "Caddy & domains"},
		menuItem{"users", "/users", "Users"},
		menuItem{"software", "/software", "Software"},
		menuItem{"backup", "/backup", "Backup"},
		menuItem{"terminal", "/terminal", "Terminal"},
	))
	nav.WriteString(group("Observability",
		menuItem{"performance", "/performance", "Performance"},
		menuItem{"log-retention", "/log-retention", "Logs & retention"},
		menuItem{"activity", "/activity", "Activity"},
	))
	nav.WriteString(link(menuItem{"security", "/security", "Security"}))
	return `<div class="topbar-wrap"><div class="shell"><header class="topbar">
		<a class="brand" href="/"><span class="brand-mark">OG</span><span class="brand-copy"><strong>Open Go Panel</strong><small>SERVER CONTROL</small></span></a>
		<nav class="nav" aria-label="Main navigation">` + nav.String() + `</nav>
		<div class="sidebar-note"><span class="sidebar-note-dot"></span>Server management</div>
		<div class="header-actions">
			<form method="post" action="/panel/close" onsubmit="return confirm('Stop and disable the panel service? To reopen it, use SSH: sudo systemctl start open-go-panel.service')"><button class="secondary compact-action" type="submit" title="Stop the panel service and close its port">Close panel</button></form>
			<form method="post" action="/logout"><button class="secondary" type="submit">Logout</button></form>
		</div>
	</header></div></div>`
}
