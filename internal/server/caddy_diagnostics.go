package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"html"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	panelcaddy "github.com/bau59/open-go-panel/internal/caddy"
)

func registerCaddyDiagnostics(mux *http.ServeMux, store *sessionStore, cfg Config) {
	mux.Handle("GET /caddy/site/{id}/diagnostics", requireAuth(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid site id", http.StatusBadRequest)
			return
		}
		sites, err := cfg.Caddy.Sites()
		if err != nil {
			http.Error(w, "unable to load domains", http.StatusInternalServerError)
			return
		}
		var site panelcaddy.Site
		found := false
		for _, item := range sites {
			if item.AppID == id {
				site = item
				found = true
				break
			}
		}
		if !found {
			http.NotFound(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		var dnsText string
		addrs, err := net.DefaultResolver.LookupIPAddr(ctx, site.Domain)
		if err != nil {
			dnsText = "DNS lookup failed: " + err.Error()
		} else {
			var ips []string
			for _, addr := range addrs {
				ips = append(ips, addr.IP.String())
			}
			dnsText = strings.Join(ips, ", ")
		}

		tlsText := "Certificate unavailable"
		dialer := &net.Dialer{Timeout: 3 * time.Second}
		conn, err := tls.DialWithDialer(dialer, "tcp", net.JoinHostPort(site.Domain, "443"), &tls.Config{
			ServerName: site.Domain,
			MinVersion: tls.VersionTLS12,
		})
		if err != nil {
			tlsText = "TLS connection failed: " + err.Error()
		} else {
			state := conn.ConnectionState()
			if len(state.PeerCertificates) > 0 {
				cert := state.PeerCertificates[0]
				tlsText = fmt.Sprintf("Valid through %s (issuer: %s)", cert.NotAfter.Format("2006-01-02 15:04 UTC"), cert.Issuer.CommonName)
			}
			_ = conn.Close()
		}
		traffic := "Access logs are unavailable."
		result, logErr := cfg.Caddy.QueryStructuredLogs(ctx, panelcaddy.LogQuery{
			Domain: site.Domain,
			Kind: "access",
			Since: time.Now().Add(-24*time.Hour).Format("2006-01-02 15:04:05"),
			PerPage: 200,
		})
		if logErr == nil {
			var success, redirects, clientErrors, serverErrors int
			for _, entry := range result.Entries {
				switch {
				case entry.Status >= 500:
					serverErrors++
				case entry.Status >= 400:
					clientErrors++
				case entry.Status >= 300:
					redirects++
				case entry.Status >= 200:
					success++
				}
			}
			traffic = fmt.Sprintf("Last %d matching requests (maximum 200): 2xx %d · 3xx %d · 4xx %d · 5xx %d",
				len(result.Entries), success, redirects, clientErrors, serverErrors)
			if result.HasNext { traffic += ". More than 200 requests exist; counts are a sample." }
		}
		body := pageHead("Domain diagnostics") + `<body>` + appHeader("caddy") + `
		<main class="shell">
			<div class="page-head">
				<div><p class="eyebrow">Caddy / Diagnostics</p><h1>` + html.EscapeString(site.Domain) + `</h1><p class="sub">Live DNS lookup and HTTPS certificate verification.</p></div>
				<a class="secondary" href="/caddy?app=` + strconv.FormatInt(id, 10) + `">Domain settings</a>
			</div>
			<section class="panel panel-pad" style="margin-bottom:16px">
				<h2>DNS records (A / AAAA)</h2>
				<p style="overflow-wrap:anywhere">` + html.EscapeString(dnsText) + `</p>
			</section>
			<section class="panel panel-pad" style="margin-bottom:16px">
				<h2>HTTPS certificate</h2>
				<p style="overflow-wrap:anywhere">` + html.EscapeString(tlsText) + `</p>
			</section>
			<section class="panel panel-pad">
				<h2>HTTP traffic (past 24 hours)</h2>
				<p>` + html.EscapeString(traffic) + `</p>
				<a class="secondary" href="/caddy/logs?domain=` + html.EscapeString(site.Domain) + `">View traffic logs</a>
			</section>
		</main></body></html>`
		writeHTML(w, cfg.Logger, http.StatusOK, body)
	})))
}
