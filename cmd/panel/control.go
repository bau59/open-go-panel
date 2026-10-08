package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// schedulePanelStop creates a transient systemd timer so the HTTP response can
// finish before the panel's own service is stopped. Disabling the service also
// keeps the listener closed across reboots until an SSH operator starts it.
func schedulePanelStop(ctx context.Context) error {
	if _, err := exec.LookPath("systemd-run"); err != nil {
		return fmt.Errorf("systemd-run is unavailable: %w", err)
	}
	args := panelStopArgs(time.Now().UnixNano())
	out, err := exec.CommandContext(ctx, "systemd-run", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("schedule panel shutdown: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func panelStopArgs(nanos int64) []string {
	return []string{
		fmt.Sprintf("--unit=open-go-panel-close-%d", nanos),
		"--on-active=3s",
		"--collect",
		"/usr/bin/systemctl", "disable", "--now", "open-go-panel.service",
	}
}
