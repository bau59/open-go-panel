package server

import (
	"fmt"
	"html"
	"strings"

	panelcaddy "github.com/bau59/open-go-panel/internal/caddy"
)

// caddyAliasEditor manages canonical host redirects separately from custom
// proxy/static config so a site's settings remain intact.
func caddyAliasEditor(site panelcaddy.Site) string {
	if site.Kind == "parked" || site.Kind == "redirect" {
		return ""
	}
	var checkbox string
	if site.WWWRedirect {
		checkbox = " checked"
	}
	mirrorDomains := html.EscapeString(strings.Join(site.Aliases, "\n"))
	return fmt.Sprintf(`
		<section class="caddy-setting-section" style="margin-top:16px">
			<div class="section-title" style="margin-bottom:10px">
				<div><h3>Primary domain &amp; mirrors</h3>
					<p class="note">Only the primary domain serves the site. Additional hostnames permanently redirect to it.</p>
				</div>
			</div>
			<div class="domain-summary" style="margin-bottom:14px">
				<div><span>Primary domain</span><strong>%s</strong></div>
				<div><span>Redirect</span><strong>301 · keeps path and query</strong></div>
			</div>
			<form method="post" action="/caddy/site/%d/aliases">
				<label class="setting-switch">
					<div>
						<strong>www / non-www redirect</strong>
						<span>Automatically redirect the other version to the primary domain.</span>
					</div>
					<span class="switch"><input type="checkbox" name="www_redirect" value="1"%s><i></i></span>
				</label>
				<label for="caddy-mirrors-%d" style="display:block;margin-top:16px">Mirror domains</label>
				<textarea id="caddy-mirrors-%d" class="codearea" name="mirrors" rows="4"
					placeholder="longer-example-domain.com&#10;another-example.net">%s</textarea>
				<p class="note">One hostname per line, without https://, paths or ports. Each mirror must point to this server in DNS. Caddy will provision HTTPS certificates when Automatic HTTPS is enabled. Conflicting domains are rejected.</p>
				<div class="actions" style="justify-content:flex-start;margin-top:12px">
					<button class="button">Save domain redirects</button>
				</div>
			</form>
		</section>`,
		html.EscapeString(site.Domain), site.AppID, checkbox, site.AppID, site.AppID, mirrorDomains)
}
