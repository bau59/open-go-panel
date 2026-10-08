package software

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Item struct {
	ID          string
	Name        string
	Description string
	Version     string
	Installed   bool
	Detail      string
}

type Task struct {
	SoftwareID string
	Action     string
	Running    bool
	Error      string
	StartedAt  time.Time
	FinishedAt time.Time
}

type Manager struct {
	mu   sync.Mutex
	task Task
}

func New() *Manager { return &Manager{} }

func (m *Manager) Items(ctx context.Context) []Item {
	return []Item{
		dockerStatus(ctx),
		nodeStatus(ctx),
		tailwindStatus(ctx),
		goStatus(ctx),
		airStatus(ctx),
		gitStatus(ctx),
		buildToolsStatus(ctx),
		cliToolsStatus(ctx),
	}
}

func (m *Manager) Task() Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.task
}

func (m *Manager) Start(id, action string) error {
	if action != "install" && action != "update" {
		return errors.New("unsupported software action")
	}
	if !supportedID(id) {
		return errors.New("unsupported software")
	}

	m.mu.Lock()
	if m.task.Running {
		current := m.task
		m.mu.Unlock()
		return fmt.Errorf("%s %s is already running", current.Action, current.SoftwareID)
	}
	m.task = Task{SoftwareID: id, Action: action, Running: true, StartedAt: time.Now()}
	m.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
		defer cancel()
		err := m.run(ctx, id)

		m.mu.Lock()
		m.task.Running = false
		m.task.FinishedAt = time.Now()
		if err != nil {
			m.task.Error = err.Error()
		} else {
			m.task.Error = ""
		}
		m.mu.Unlock()
	}()
	return nil
}

func supportedID(id string) bool {
	switch id {
	case "docker", "node", "tailwind", "go", "air", "git", "build-tools", "cli-tools":
		return true
	default:
		return false
	}
}

func (m *Manager) run(ctx context.Context, id string) error {
	switch id {
	case "docker":
		return installDocker(ctx)
	case "node":
		return installNode(ctx)
	case "tailwind":
		return installTailwind(ctx)
	case "go":
		return installGo(ctx)
	case "air":
		return installAir(ctx)
	case "git":
		return aptInstall(ctx, "git")
	case "build-tools":
		return aptInstall(ctx, "build-essential", "pkg-config", "unzip", "xz-utils")
	case "cli-tools":
		return aptInstall(ctx, "jq", "rsync", "zip", "unzip", "curl", "wget", "lsof", "htop", "tree", "ripgrep", "netcat-openbsd", "dnsutils")
	default:
		return errors.New("unsupported software")
	}
}

func dockerStatus(ctx context.Context) Item {
	item := Item{
		ID: "docker", Name: "Docker Engine",
		Description: "Docker Engine, Buildx and the Docker Compose plugin from Docker's Ubuntu repository.",
		Detail: "System-wide. Docker socket access is not granted to Linux users automatically.",
	}
	if out, err := commandOutput(ctx, "docker", "--version"); err == nil {
		item.Installed = true
		item.Version = strings.TrimSpace(out)
		if compose, err := commandOutput(ctx, "docker", "compose", "version", "--short"); err == nil {
			item.Version += " · Compose " + strings.TrimSpace(compose)
		}
	}
	return item
}

func nodeStatus(ctx context.Context) Item {
	item := Item{
		ID: "node", Name: "Node.js 24 LTS",
		Description: "Official Node.js LTS binary with npm, npx and Corepack.",
		Detail: "Installed under /usr/local and available to all users.",
	}
	if out, err := commandOutput(ctx, "node", "--version"); err == nil {
		item.Installed = true
		item.Version = strings.TrimSpace(out)
		if npm, err := commandOutput(ctx, "npm", "--version"); err == nil {
			item.Version += " · npm " + strings.TrimSpace(npm)
		}
	}
	return item
}

func tailwindStatus(ctx context.Context) Item {
	item := Item{
		ID: "tailwind", Name: "Tailwind CSS CLI",
		Description: "Official standalone Tailwind CSS CLI binary.",
		Detail: "/usr/local/bin/tailwindcss · Node.js is not required to run it.",
	}
	if _, err := exec.LookPath("tailwindcss"); err == nil {
		item.Installed = true
		out, _ := commandOutput(ctx, "tailwindcss", "--help")
		item.Version = firstVersionLine(out, "tailwindcss")
		if item.Version == "" {
			item.Version = "installed"
		}
	}
	return item
}

func goStatus(ctx context.Context) Item {
	item := Item{
		ID: "go", Name: "Go",
		Description: "Latest stable Go toolchain from go.dev.",
		Detail: "/usr/local/go · used by Go application run modes.",
	}
	if out, err := commandOutput(ctx, "go", "version"); err == nil {
		item.Installed = true
		item.Version = strings.TrimSpace(out)
	}
	return item
}

func airStatus(ctx context.Context) Item {
	item := Item{
		ID: "air", Name: "Air",
		Description: "Live reload runner used by the Go Air application run mode.",
		Detail: "/usr/local/bin/air · installed with the Go toolchain.",
	}
	if out, err := commandOutput(ctx, "air", "-v"); err == nil {
		item.Installed = true
		item.Version = firstVersionLine(out, "air")
		if item.Version == "" {
			item.Version = strings.TrimSpace(out)
		}
	}
	return item
}

func gitStatus(ctx context.Context) Item {
	item := Item{
		ID: "git", Name: "Git",
		Description: "System Git client used by application deploys and repositories.",
		Detail: "Managed through Ubuntu apt.",
	}
	if out, err := commandOutput(ctx, "git", "--version"); err == nil {
		item.Installed = true
		item.Version = strings.TrimSpace(out)
	}
	return item
}

func buildToolsStatus(ctx context.Context) Item {
	item := Item{
		ID: "build-tools", Name: "Build tools",
		Description: "Compiler, make, pkg-config, unzip and xz utilities for native dependencies.",
		Detail: "build-essential · pkg-config · unzip · xz-utils",
	}
	_, gccErr := exec.LookPath("gcc")
	_, makeErr := exec.LookPath("make")
	if gccErr == nil && makeErr == nil {
		item.Installed = true
		item.Version = "installed"
	}
	return item
}

func cliToolsStatus(ctx context.Context) Item {
	item := Item{
		ID: "cli-tools", Name: "CLI tools",
		Description: "Common server utilities for diagnostics, transfers, archives and JSON processing.",
		Detail: "jq · rsync · zip/unzip · curl/wget · lsof · htop · tree · ripgrep · netcat · dnsutils",
	}
	required := []string{"jq", "rsync", "zip", "curl", "lsof", "rg", "nc", "dig"}
	missing := make([]string, 0)
	for _, name := range required {
		if _, err := exec.LookPath(name); err != nil {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		item.Installed = true
		item.Version = "installed"
	} else {
		item.Version = "missing: " + strings.Join(missing, ", ")
	}
	return item
}

func commandOutput(ctx context.Context, name string, args ...string) (string, error) {
	if _, err := exec.LookPath(name); err != nil {
		return "", err
	}
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func firstVersionLine(out, contains string) string {
	contains = strings.ToLower(contains)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && strings.Contains(strings.ToLower(line), contains) {
			return line
		}
	}
	return ""
}

func aptInstall(ctx context.Context, packages ...string) error {
	if err := run(ctx, "apt-get", "update"); err != nil {
		return err
	}
	args := append([]string{"install", "-y"}, packages...)
	return run(ctx, "apt-get", args...)
}

func installDocker(ctx context.Context) error {
	script := "set -euo pipefail\n" +
		"apt-get update\n" +
		"DEBIAN_FRONTEND=noninteractive apt-get install -y ca-certificates curl\n" +
		"install -m 0755 -d /etc/apt/keyrings\n" +
		"curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc\n" +
		"chmod a+r /etc/apt/keyrings/docker.asc\n" +
		". /etc/os-release\n" +
		"ARCH=$(dpkg --print-architecture)\n" +
		"printf 'Types: deb\\nURIs: https://download.docker.com/linux/ubuntu\\nSuites: %s\\nComponents: stable\\nArchitectures: %s\\nSigned-By: /etc/apt/keyrings/docker.asc\\n' \"$VERSION_CODENAME\" \"$ARCH\" > /etc/apt/sources.list.d/docker.sources\n" +
		"apt-get update\n" +
		"DEBIAN_FRONTEND=noninteractive apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin\n" +
		"systemctl enable --now docker.service\n"
	return runShell(ctx, script)
}

func installNode(ctx context.Context) error {
	script := "set -euo pipefail\n" +
		"apt-get update\n" +
		"DEBIAN_FRONTEND=noninteractive apt-get install -y ca-certificates curl xz-utils\n" +
		"case $(uname -m) in x86_64) ARCH=x64 ;; aarch64|arm64) ARCH=arm64 ;; *) echo unsupported architecture >&2; exit 1 ;; esac\n" +
		"BASE=https://nodejs.org/dist/latest-v24.x\n" +
		"SUMS=$(curl -fsSL \"$BASE/SHASUMS256.txt\")\n" +
		"FILE=$(printf '%s\\n' \"$SUMS\" | awk -v arch=\"$ARCH\" '$2 ~ (\"linux-\" arch \"\\\\.tar\\\\.xz$\") {print $2; exit}')\n" +
		"test -n \"$FILE\"\n" +
		"HASH=$(printf '%s\\n' \"$SUMS\" | awk -v file=\"$FILE\" '$2 == file {print $1; exit}')\n" +
		"TMP=$(mktemp --suffix=.tar.xz)\ntrap 'rm -f \"$TMP\"' EXIT\n" +
		"curl -fsSL \"$BASE/$FILE\" -o \"$TMP\"\n" +
		"printf '%s  %s\\n' \"$HASH\" \"$TMP\" | sha256sum -c -\n" +
		"DEST=/usr/local/lib/nodejs/$(basename \"$FILE\" .tar.xz)\n" +
		"rm -rf \"$DEST\"\nmkdir -p \"$DEST\"\n" +
		"tar -xJf \"$TMP\" -C \"$DEST\" --strip-components=1\n" +
		"for bin in node npm npx corepack; do test -e \"$DEST/bin/$bin\" && ln -sfn \"$DEST/bin/$bin\" \"/usr/local/bin/$bin\"; done\n"
	return runShell(ctx, script)
}

func installTailwind(ctx context.Context) error {
	script := "set -euo pipefail\n" +
		"case $(uname -m) in x86_64) ASSET=tailwindcss-linux-x64 ;; aarch64|arm64) ASSET=tailwindcss-linux-arm64 ;; *) echo unsupported architecture >&2; exit 1 ;; esac\n" +
		"TMP=$(mktemp /usr/local/bin/.tailwindcss.XXXXXX)\ntrap 'rm -f \"$TMP\"' EXIT\n" +
		"curl -fsSL \"https://github.com/tailwindlabs/tailwindcss/releases/latest/download/$ASSET\" -o \"$TMP\"\n" +
		"chmod 0755 \"$TMP\"\nmv -f \"$TMP\" /usr/local/bin/tailwindcss\n"
	return runShell(ctx, script)
}

func installGo(ctx context.Context) error {
	script := "set -euo pipefail\n" +
		"case $(uname -m) in x86_64) ARCH=amd64 ;; aarch64|arm64) ARCH=arm64 ;; *) echo unsupported architecture >&2; exit 1 ;; esac\n" +
		"VERSION=$(curl -fsSL 'https://go.dev/VERSION?m=text' | head -n1)\n" +
		"test -n \"$VERSION\"\n" +
		"TMP=$(mktemp --suffix=.tar.gz)\nNEW=/usr/local/.go-new-$$\nOLD=/usr/local/.go-old-$$\n" +
		"trap 'rm -f \"$TMP\"; rm -rf \"$NEW\"' EXIT\n" +
		"curl -fsSL \"https://go.dev/dl/$VERSION.linux-$ARCH.tar.gz\" -o \"$TMP\"\n" +
		"mkdir -p \"$NEW\"\ntar -xzf \"$TMP\" -C \"$NEW\" --strip-components=1\n" +
		"test -x \"$NEW/bin/go\"\n" +
		"if [ -d /usr/local/go ]; then mv /usr/local/go \"$OLD\"; fi\n" +
		"if mv \"$NEW\" /usr/local/go; then rm -rf \"$OLD\"; else test ! -d \"$OLD\" || mv \"$OLD\" /usr/local/go; exit 1; fi\n"
	return runShell(ctx, script)
}

func installAir(ctx context.Context) error {
	goBin := "/usr/local/go/bin/go"
	if _, err := os.Stat(goBin); err != nil {
		path, lookupErr := exec.LookPath("go")
		if lookupErr != nil {
			return errors.New("Go must be installed before Air")
		}
		goBin = path
	}
	cmd := exec.CommandContext(ctx, goBin, "install", "github.com/air-verse/air@latest")
	cmd.Env = append(os.Environ(), "GOBIN=/usr/local/bin")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("install Air: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func runShell(ctx context.Context, script string) error {
	cmd := exec.CommandContext(ctx, "bash", "-c", script)
	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("software install failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", filepath.Base(name), strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
