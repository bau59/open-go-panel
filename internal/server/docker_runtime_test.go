package server

import (
	"strings"
	"testing"

	paneldocker "github.com/bau59/open-go-panel/internal/docker"
)

func TestDockerPageIncludesRuntimeConfiguration(t *testing.T) {
	html := dockerPage(
		paneldocker.Status{Installed: true, Active: true, Version: "test"},
		nil, false, paneldocker.BuildTask{}, paneldocker.GitHubKeyInfo{}, "",
	)
	for _, field := range []string{
		`name="environment"`, `name="environment_file"`, `name="volumes"`, `name="shm_size" placeholder`, `name="init"`,
	} {
		if count := strings.Count(html, field); count != 2 {
			t.Errorf("%s found %d times; expected both Docker forms", field, count)
		}
	}
	if !strings.Contains(html, "/opt/deepseek-bridge/data:/app/data") {
		t.Error("missing host data volume example")
	}
	if !strings.Contains(html, "STATE_ENCRYPTION_KEY") {
		t.Error("missing environment example")
	}
}
