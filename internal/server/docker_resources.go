package server

import (
    "fmt"
    "html"
    "strconv"

    paneldocker "github.com/bau59/open-go-panel/internal/docker"
)

func dockerResourceButtons(item paneldocker.Container) string {
    id := html.EscapeString(item.ID)
    name := html.EscapeString(item.Name)
    return fmt.Sprintf(`<button type="button" class="secondary compact-action" data-container-id="%s" data-container-name="%s" onclick="dockerOpenLogs(this)">Logs</button>
        <button type="button" class="secondary compact-action" onclick="document.getElementById('docker-limits-%s').showModal()">Limits</button>`, id, name, id)
}

func dockerResourceDialog(item paneldocker.Container, availableCores int) string {
    id := html.EscapeString(item.ID)
    name := html.EscapeString(item.Name)
    cpu := "0"
    if item.NanoCPUs > 0 {
        cpu = strconv.FormatFloat(float64(item.NanoCPUs)/1e9, 'f', -1, 64)
    }
    maxCores := 256
    cpuHint := "0 = unlimited; 1 = one full CPU core; 0.5 = 50% of one core."
    if availableCores > 0 {
        if availableCores < maxCores {
            maxCores = availableCores
        }
        cpuHint += fmt.Sprintf(" Docker reports %d available CPU cores.", availableCores)
    } else {
        cpuHint += " Docker CPU count is unavailable; the limit will be checked on save."
    }
    memory := "0"
    if item.MemoryLimit > 0 {
        memory = strconv.FormatInt((item.MemoryLimit+1048575)/1048576, 10)
    }
    return fmt.Sprintf(`<dialog id="docker-limits-%s" class="docker-rebuild-dialog">
        <div class="docker-dialog-head"><div><h2>Resource limits: %s</h2>
            <p class="note">Apply changes to the existing container without a restart.</p></div>
            <button type="button" class="secondary compact-action" onclick="this.closest('dialog').close()">Close</button></div>
        <form method="post" action="/docker/%s/limits" class="docker-rebuild-form">
            <div class="docker-dialog-grid">
                <label>CPU cores (0 = unlimited) <input name="cpu" type="number" min="0" max="%d" step="any" required value="%s"></label>
                <label>Memory (MiB) <input name="memory_mib" type="number" min="0" max="1048576" step="1" required value="%s"></label>
            </div>
            <p class="note">%s Minimum positive values: 0.01 CPU, 6 MiB memory. Reducing RAM below current usage can terminate processes. When RAM changes, Docker's combined RAM+swap limit is updated automatically while preserving the existing swap allowance. Setting RAM to 0 removes both limits. Docker Compose may overwrite these settings when recreating a container.</p>
            <button type="submit" class="button">Save limits</button>
        </form>
    </dialog>`, id, name, id, maxCores, html.EscapeString(cpu), html.EscapeString(memory), html.EscapeString(cpuHint))
}

func dockerLogsDialog() string {
    return `<dialog id="docker-log-dialog" class="docker-rebuild-dialog docker-logs-dialog">
        <div class="docker-dialog-head"><div><h2>Logs: <span id="docker-log-name"></span></h2>
            <p class="note">Logs are read from Docker on demand. No extra copies are stored.</p></div>
            <button type="button" class="secondary compact-action" onclick="this.closest('dialog').close()">Close</button></div>
        <div class="docker-log-toolbar">
            <label>Last <select id="docker-log-tail" onchange="dockerRefreshLogs()">
                <option value="100">100 lines</option>
                <option value="300" selected>300 lines</option>
                <option value="1000">1000 lines</option>
            </select></label>
            <button type="button" class="secondary compact-action" onclick="dockerRefreshLogs()">Refresh</button>
        </div>
        <pre id="docker-log-output" class="docker-log-output" aria-live="polite">Select a container to view logs.</pre>
    </dialog>`
}

func dockerLogsScript() string {
    return `<script>
    let dockerLogsAbort;
    function dockerOpenLogs(button) {
        const dialog = document.getElementById('docker-log-dialog');
        dialog.dataset.containerId = button.dataset.containerId;
        document.getElementById('docker-log-name').textContent = button.dataset.containerName;
        dialog.showModal();
        dockerRefreshLogs();
    }
    async function dockerRefreshLogs() {
        const dialog = document.getElementById('docker-log-dialog');
        const output = document.getElementById('docker-log-output');
        const id = dialog.dataset.containerId;
        if (!id) return;
        if (dockerLogsAbort) dockerLogsAbort.abort();
        const controller = new AbortController();
        dockerLogsAbort = controller;
        output.textContent = 'Loading logs…';
        try {
            const tail = document.getElementById('docker-log-tail').value;
            const res = await fetch('/docker/' + encodeURIComponent(id) + '/logs?tail=' + tail,
                {cache:'no-store',signal:controller.signal});
            const body = await res.text();
            if (dialog.dataset.containerId !== id) return;
            output.textContent = res.ok ? (body || 'No log entries.') : 'Could not read logs: ' + body;
        } catch (error) {
            if (error.name !== 'AbortError') output.textContent = 'Could not read container logs.';
        }
    }
    </script>`
}
