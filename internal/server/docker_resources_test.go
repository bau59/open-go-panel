package server

import (
    "io"
    "log/slog"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"

    paneldocker "github.com/bau59/open-go-panel/internal/docker"
)

func TestDockerResourcesVisibleInContainerTable(t *testing.T) {
    containers := []paneldocker.Container{
        {ID:"abc123", Name:"container<sample>", Image:"sample", Running:true, State:"running",
            NanoCPUs:1500000000, MemoryLimit:512*1048576},
    }
    page := dockerPage(paneldocker.Status{Installed:true, Active:true, CPUCount:2},
        containers, false, paneldocker.BuildTask{}, paneldocker.GitHubKeyInfo{}, "")
    for _, fragment := range []string{
        `data-container-id="abc123"`,
        `data-container-name="container&lt;sample&gt;"`,
        `id="docker-log-dialog"`,
        `id="docker-log-tail"`,
        `/docker/abc123/limits`,
        `id="docker-limits-abc123"`,
        `name="cpu" type="number" min="0" max="2"`,
        `Docker reports 2 available CPU cores.`,
        `0.5 = 50% of one core.`,
        `value="1.5"`,
        `name="memory_mib" type="number"`,
        `value="512"`,
        `dockerOpenLogs(this)`,
        `dockerRefreshLogs()`,
    } {
        if !strings.Contains(page, fragment) {
            t.Errorf("missing container resource element: %s", fragment)
        }
    }
}

func TestDockerResourceRoutesRequireAuthentication(t *testing.T) {
    handler := New(Config{
        Logger:slog.New(slog.NewTextHandler(io.Discard, nil)),
        AdminUser:"admin",
        AdminPassword:"secret",
        Docker:paneldocker.New(),
    })
    for _, tc := range []struct{method, path string}{
        {http.MethodGet, "/docker/abc123/logs?tail=100"},
        {http.MethodPost, "/docker/abc123/limits"},
    } {
        request:=httptest.NewRequest(tc.method,tc.path,nil)
        recorder:=httptest.NewRecorder()
        handler.ServeHTTP(recorder,request)
        if recorder.Code!=http.StatusSeeOther || recorder.Header().Get("Location")!="/login" {
            t.Errorf("%s %s should redirect to login; got %d", tc.method,tc.path,recorder.Code)
        }
    }
}
