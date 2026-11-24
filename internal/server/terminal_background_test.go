package server

import (
	"strings"
	"testing"

	panelterminal "github.com/bau59/open-go-panel/internal/terminal"
)

func TestBackgroundCommandPageDoesNotOpenWebSocket(t *testing.T) {
	body := backgroundPage(backgroundPageData{
		SelectedUser: "root",
		WorkDir:      "/root",
		Jobs: []panelterminal.BackgroundJob{{
			ID: "0123456789abcdef01234567", Username: "root",
			Command: "<script>alert(1)</script> && sleep 30",
			Status: "running", WorkDir: "/root",
		}},
	})
	for _, want := range []string{
		"/terminal/background/start",
		"/terminal/background/0123456789abcdef01234567/stop",
		"/terminal/background/0123456789abcdef01234567/logs",
		"&lt;script&gt;alert(1)&lt;/script&gt;",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q", want)
		}
	}
	for _, forbidden := range []string{"/terminal/ws", "new WebSocket", "<script>alert(1)</script>"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("unexpected %q", forbidden)
		}
	}
}

func TestBackgroundCommandNavigationUsesNativeRequests(t *testing.T) {
	if !shouldKeepNativeLink("/terminal/background") || !shouldKeepNativeLink("/terminal/background/0123456789abcdef01234567/logs") {
		t.Fatal("background command link was boosted")
	}
	if !shouldKeepNativeForm(`<form method="post" action="/terminal/background/start">`) ||
		!shouldKeepNativeForm(`<form method="post" action="/terminal/background/0123456789abcdef01234567/stop">`) {
		t.Fatal("background command action was boosted")
	}
}
