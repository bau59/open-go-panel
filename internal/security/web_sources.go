package security

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	panelCaddyAcquisition = "source: journalctl\njournalctl_filter:\n  - \"_SYSTEMD_UNIT=caddy.service\"\nlabels:\n  type: caddy\n"
	// This rule precedes Ubuntu's 50-default.conf. Caddy HTTP JSON remains in
	// journald for CrowdSec, incident investigation and on-demand reports.
	// Only Caddy's HTTP JSON is dropped from rsyslog; unrelated system, SSH
	// and Caddy service/error messages still follow normal syslog rules.
	panelCaddyRsyslog = "# Open Go Panel: avoid a second copy of Caddy HTTP events in syslog.\n" +
		"if ($programname == \"caddy\" and $msg contains '\"logger\":\"http.log.') then {\n  stop\n}\n"
)

var webSourceLock sync.Mutex

type webPaths struct {
	acquisDir     string
	acquisLegacy  string
	rsyslogDir    string
}

func systemWebPaths() webPaths {
	return webPaths{
		acquisDir: "/etc/crowdsec/acquis.d",
		acquisLegacy: "/etc/crowdsec/acquis.yaml",
		rsyslogDir: "/etc/rsyslog.d",
	}
}

func (p webPaths) panelSource() string {
	return filepath.Join(p.acquisDir, "open-go-panel-caddy.yaml")
}

func (p webPaths) syslogFilter() string {
	return filepath.Join(p.rsyslogDir, "20-open-go-panel-caddy.conf")
}

// WebSources describes configured acquisitions, not a guarantee that a
// particular HTTP event has passed every parser, scenario and firewall rule.
type WebSources struct {
	Panel                bool
	External             []string
	SyslogAcquired       bool
	SyslogFiltered       bool
	NeedsRepair          bool
	Error                string
}

func (s WebSources) HasCaddy() bool {
	return s.Panel || len(s.External) > 0
}

func (s WebSources) ExternallyManaged() bool {
	return len(s.External) > 0
}

var yamlSeparator = regexp.MustCompile(`(?m)^---\s*$`)

func documentCaddyJournal(document string) bool {
	var source, unit, label bool
	for _, raw := range strings.Split(document, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line == "source: journalctl" {
			source = true
		}
		if strings.Contains(line, "_SYSTEMD_UNIT=caddy.service") {
			unit = true
		}
		if line == "type: syslog" || line == "type: caddy" {
			label = true
		}
	}
	return source && unit && label
}

func documentReadsSyslog(document string) bool {
	file, syslog := false, false
	for _, raw := range strings.Split(document, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line == "source: file" {
			file = true
		}
		if strings.Trim(line, " -\"'") == "/var/log/syslog" {
			syslog = true
		}
	}
	return file && syslog
}

func inspectWebSources(p webPaths) (WebSources, error) {
	var result WebSources
	paths := []string{p.acquisLegacy}
	entries, err := os.ReadDir(p.acquisDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return result, fmt.Errorf("read CrowdSec acquisitions: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}
		paths = append(paths, filepath.Join(p.acquisDir, name))
	}
	for _, path := range paths {
		data, err := readSimpleConfig(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return result, fmt.Errorf("read CrowdSec acquisition %s: %w", path, err)
		}
		for _, doc := range yamlSeparator.Split(string(data), -1) {
			if documentReadsSyslog(doc) {
				result.SyslogAcquired = true
			}
			if !documentCaddyJournal(doc) {
				continue
			}
			if path == p.panelSource() {
				result.Panel = true
			} else {
				result.External = append(result.External, path)
			}
		}
	}
	sort.Strings(result.External)
	filter, err := readSimpleConfig(p.syslogFilter())
	if err == nil {
		result.SyslogFiltered = string(filter) == panelCaddyRsyslog
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, err
	}
	result.NeedsRepair = (result.Panel && len(result.External) > 0) ||
		len(result.External) > 1 ||
		(result.HasCaddy() && result.SyslogAcquired && !result.SyslogFiltered)
	return result, nil
}

// Refuse directories and symlinks when touching root-owned service configs.
func readSimpleConfig(path string) ([]byte, error) {
	stat, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() || stat.Size() > 1024*1024 {
		return nil, fmt.Errorf("unsafe configuration file %s", path)
	}
	return os.ReadFile(path)
}

type originalConfig struct {
	present bool
	data    []byte
	mode    os.FileMode
}

func saveOriginal(path string) (originalConfig, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return originalConfig{}, nil
	}
	if err != nil {
		return originalConfig{}, err
	}
	data, err := readSimpleConfig(path)
	if err != nil {
		return originalConfig{}, err
	}
	return originalConfig{present: true, data: data, mode: info.Mode().Perm()}, nil
}

func writeConfigAtomic(path string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ogp-web-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func restoreOriginal(path string, old originalConfig) error {
	if !old.present {
		err := os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return writeConfigAtomic(path, old.data, old.mode)
}

// repairCaddyAcquisition chooses a single journald acquisition for Caddy and
// prevents a second copy of its HTTP events from being written to syslog.
// It does not touch CrowdSec-generated setup.* files, UFW, nftables, SSH logs,
// Caddy configuration or existing decisions.
func repairCaddyAcquisition(ctx context.Context, paths webPaths) error {
	webSourceLock.Lock()
	defer webSourceLock.Unlock()

	before, err := inspectWebSources(paths)
	if err != nil {
		return err
	}
	if len(before.External) > 1 {
		return fmt.Errorf("multiple externally managed Caddy journald acquisitions: %s; resolve them before using Repair", strings.Join(before.External, ", "))
	}

	panel := paths.panelSource()
	syslog := paths.syslogFilter()
	panelOld, err := saveOriginal(panel)
	if err != nil {
		return err
	}
	syslogOld, err := saveOriginal(syslog)
	if err != nil {
		return err
	}
	if panelOld.present && strings.TrimSpace(string(panelOld.data)) != strings.TrimSpace(panelCaddyAcquisition) {
		return errors.New("Open Go Panel Caddy acquisition has custom changes; refusing to overwrite it")
	}

	if syslogOld.present && string(syslogOld.data) != panelCaddyRsyslog {
		return errors.New("Open Go Panel rsyslog drop-in has custom changes; refusing to overwrite it")
	}

	var modifiedPanel, modifiedSyslog bool
	restore := func(cause error) error {
		// Rollback must still run if the originating request was cancelled, but
		// service restarts are bounded to avoid hanging a privileged HTTP handler.
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		var rollback []string
		if modifiedPanel {
			if err := restoreOriginal(panel, panelOld); err != nil {
				rollback = append(rollback, "CrowdSec config: "+err.Error())
			}
		}
		if modifiedSyslog {
			if err := restoreOriginal(syslog, syslogOld); err != nil {
				rollback = append(rollback, "rsyslog config: "+err.Error())
			}
			if serviceActive(rollbackCtx, "rsyslog.service") {
				if err := run(rollbackCtx, "systemctl", "restart", "rsyslog.service"); err != nil {
					rollback = append(rollback, "rsyslog restart: "+err.Error())
				}
			}
		}
		if modifiedPanel && serviceActive(rollbackCtx, "crowdsec.service") {
			if err := run(rollbackCtx, "systemctl", "restart", "crowdsec.service"); err != nil {
				rollback = append(rollback, "CrowdSec restart: "+err.Error())
			}
		}
		if len(rollback) > 0 {
			return fmt.Errorf("%w; rollback errors: %s", cause, strings.Join(rollback, "; "))
		}
		return fmt.Errorf("%w (previous files restored)", cause)
	}

	if len(before.External) == 1 {
		if panelOld.present {
			if err := os.Remove(panel); err != nil {
				return err
			}
			modifiedPanel = true
		}
	} else if !panelOld.present {
		if err := writeConfigAtomic(panel, []byte(panelCaddyAcquisition), 0644); err != nil {
			return err
		}
		modifiedPanel = true
	}

	// The filter is relevant only if CrowdSec acquires the syslog file and
	// rsyslog is installed. It is harmless to retain old syslog history.
	if before.SyslogAcquired {
		if _, err := exec.LookPath("rsyslogd"); err == nil && !syslogOld.present {
			if err := writeConfigAtomic(syslog, []byte(panelCaddyRsyslog), 0644); err != nil {
				return restore(err)
			}
			modifiedSyslog = true
		}
	}
	if modifiedSyslog {
		if err := run(ctx, "rsyslogd", "-N1"); err != nil {
			return restore(err)
		}
		if serviceActive(ctx, "rsyslog.service") {
			if err := run(ctx, "systemctl", "restart", "rsyslog.service"); err != nil {
				return restore(err)
			}
		}
	}
	// Always validate and restart to activate a newly installed Caddy parser,
	// including when the generated setup.caddy.yaml was already present.
	if err := run(ctx, "crowdsec", "-t"); err != nil {
		return restore(err)
	}
	if serviceActive(ctx, "crowdsec.service") {
		if err := run(ctx, "systemctl", "restart", "crowdsec.service"); err != nil {
			return restore(err)
		}
	}
	return nil
}
