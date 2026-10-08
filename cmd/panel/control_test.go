package main

import (
	"strings"
	"testing"
)

func TestPanelStopDisablesServiceAfterResponseDelay(t *testing.T) {
	args := strings.Join(panelStopArgs(12345), " ")
	for _, required := range []string{
		"--unit=open-go-panel-close-12345",
		"--on-active=3s",
		"/usr/bin/systemctl disable --now open-go-panel.service",
	} {
		if !strings.Contains(args, required) {
			t.Fatalf("shutdown command %q missing %q", args, required)
		}
	}
}
