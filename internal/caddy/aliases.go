package caddy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const siteAliasesSettingKey = "caddy.site_aliases"

type siteAliasSettings struct {
	WWW     bool     `json:"www,omitempty"`
	Mirrors []string `json:"mirrors,omitempty"`
}

// canonicalAliasHost derives the www / non-www counterpart of the primary
// hostname. The primary hostname is always the redirect destination.
func canonicalAliasHost(primary string) string {
	if strings.HasPrefix(primary, "www.") {
		return strings.TrimPrefix(primary, "www.")
	}
	return "www." + primary
}

func isValidSiteHostname(domain string) bool {
	return len(domain) <= 253 && domainRE.MatchString(domain)
}

func parseMirrorDomains(raw string) ([]string, error) {
	var mirrors []string
	seen := make(map[string]bool)
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == ' ' || r == '\t'
	}) {
		domain := strings.ToLower(strings.TrimSpace(part))
		if !isValidSiteHostname(domain) {
			return nil, fmt.Errorf("invalid mirror domain %q: enter a hostname without scheme, path or port", part)
		}
		if seen[domain] {
			return nil, fmt.Errorf("duplicate mirror domain %q", domain)
		}
		seen[domain] = true
		mirrors = append(mirrors, domain)
		if len(mirrors) > 20 {
			return nil, errors.New("a site can have at most 20 mirror domains")
		}
	}
	sort.Strings(mirrors)
	return mirrors, nil
}

func siteAliasHosts(site Site) []string {
	var aliases []string
	if site.WWWRedirect {
		aliases = append(aliases, canonicalAliasHost(site.Domain))
	}
	return append(aliases, site.Aliases...)
}

// validateSiteHosts rejects overlaps in canonical domains, auto www variants
// and explicit mirrors, including conflicts with parked standalone domains.
func validateSiteHosts(sites []Site) error {
	used := make(map[string]int64)
	for _, site := range sites {
		if !isValidSiteHostname(site.Domain) {
			return fmt.Errorf("invalid primary domain %q", site.Domain)
		}
		if len(site.Aliases) > 20 {
			return fmt.Errorf("too many mirrors for %s", site.Domain)
		}
		names := append([]string{site.Domain}, siteAliasHosts(site)...)
		for _, hostname := range names {
			if !isValidSiteHostname(hostname) {
				return fmt.Errorf("invalid hostname %q", hostname)
			}
			if owner, present := used[hostname]; present {
				return fmt.Errorf("domain %q is already configured (site %d)", hostname, owner)
			}
			used[hostname] = site.AppID
		}
		if (site.Kind == "parked" || site.Kind == "redirect") &&
			(site.WWWRedirect || len(site.Aliases) != 0) {
			return fmt.Errorf("domain %q cannot have aliases while parked or redirected", site.Domain)
		}
	}
	return nil
}

// renderSiteAliases produces separate Caddy site blocks, independent of the
// application's generated or custom template. {uri} keeps path and query.
func renderSiteAliases(site Site, https bool) string {
	scheme := "https://"
	addressPrefix := ""
	if !https {
		scheme = "http://"
		addressPrefix = "http://"
	}
	var blocks []string
	for _, alias := range siteAliasHosts(site) {
		blocks = append(blocks, fmt.Sprintf("%s%s {\n\tredir %s%s{uri} 301\n}", addressPrefix, alias, scheme, site.Domain))
	}
	return strings.Join(blocks, "\n\n")
}

// SetSiteAliases atomically updates a site's www option and mirror domain set.
// Caddy validation/reload precede persistence through applyLocked.
func (m *Manager) SetSiteAliases(ctx context.Context, id int64, www bool, rawMirrors string) error {
	mirrors, err := parseMirrorDomains(rawMirrors)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	sites, err := m.load()
	if err != nil {
		return err
	}
	found := false
	for i := range sites {
		if sites[i].AppID != id {
			continue
		}
		if sites[i].Kind == "parked" || sites[i].Kind == "redirect" {
			return errors.New("connect this domain to an application before adding redirects")
		}
		sites[i].WWWRedirect = www
		sites[i].Aliases = mirrors
		found = true
		break
	}
	if !found {
		return errors.New("domain not found")
	}
	if err := validateSiteHosts(sites); err != nil {
		return err
	}
	return m.apply(ctx, sites)
}

func (m *Manager) loadSiteAliases(sites []Site) error {
	value, found, err := m.store.Setting(siteAliasesSettingKey)
	if err != nil {
		return err
	}
	if !found || strings.TrimSpace(value) == "" {
		return nil
	}
	var persisted map[string]siteAliasSettings
	if err := json.Unmarshal([]byte(value), &persisted); err != nil {
		return fmt.Errorf("decode site aliases: %w", err)
	}
	for i := range sites {
		if settings, ok := persisted[strconv.FormatInt(sites[i].AppID, 10)]; ok {
			sites[i].WWWRedirect = settings.WWW
			sites[i].Aliases = settings.Mirrors
		}
	}
	return nil
}

func marshalSiteAliases(sites []Site) (string, error) {
	result := make(map[string]siteAliasSettings)
	for _, site := range sites {
		if !site.WWWRedirect && len(site.Aliases) == 0 {
			continue
		}
		result[strconv.FormatInt(site.AppID, 10)] = siteAliasSettings{
			WWW: site.WWWRedirect, Mirrors: site.Aliases,
		}
	}
	data, err := json.Marshal(result)
	return string(data), err
}
