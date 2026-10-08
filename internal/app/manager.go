package app

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bau59/open-go-panel/internal/journal"
	"github.com/bau59/open-go-panel/internal/linuxuser"
	"github.com/bau59/open-go-panel/internal/state"
)

const (
	minPort = 8100
	maxPort = 8999
)

var (
	appNamePattern = regexp.MustCompile("^[a-z0-9][a-z0-9_-]{0,63}$")
	envNamePattern = regexp.MustCompile("^[A-Za-z_][A-Za-z0-9_]*$")
	cpuPattern     = regexp.MustCompile("^[0-9]{1,4}%$")
	memoryPattern  = regexp.MustCompile("^[0-9]+[KMGTP]$")
	branchPattern  = regexp.MustCompile("^[A-Za-z0-9._/-]{1,128}$")
)

type ServiceConfig struct {
	Mode             string `json:"mode,omitempty"`
	RunMode          string `json:"run_mode,omitempty"`
	Command          string `json:"command,omitempty"`
	WorkingDirectory string `json:"working_directory,omitempty"`
	Path             string `json:"path,omitempty"`
	Restart          string `json:"restart,omitempty"`
	RestartSec       int    `json:"restart_sec,omitempty"`
	TimeoutStopSec   int    `json:"timeout_stop_sec,omitempty"`
	CPUQuota         string `json:"cpu_quota,omitempty"`
	MemoryMax        string `json:"memory_max,omitempty"`
	LimitNOFILE      int    `json:"limit_nofile,omitempty"`
	TasksMax         int    `json:"tasks_max,omitempty"`
	LogRetentionDays int    `json:"log_retention_days,omitempty"`
	AutoStart        bool   `json:"auto_start,omitempty"`
	Environment      string `json:"environment,omitempty"`
	RawUnit          string `json:"raw_unit,omitempty"`
}

type App struct {
	ID        int64         `json:"id"`
	User      string        `json:"user"`
	Name      string        `json:"name"`
	Type      string        `json:"type"`
	Root      string        `json:"root"`
	Port      int           `json:"port,omitempty"`
	Command   string        `json:"command,omitempty"`
	Service   ServiceConfig `json:"service,omitempty"`
	CreatedAt time.Time     `json:"created_at"`
}

type DeployConfig struct {
	Repository     string
	Branch         string
	CurrentCommit  string
	PreviousCommit string
	AutoDeploy     bool
	DeployedAt     time.Time
}

type DeployKeyInfo struct {
	PublicKey  string
	KeyPath    string
	KnownHosts string
	Host       string
	Generated  bool
	HostKnown  bool
}

type RuntimeHealth struct {
	ProcessStatus string
	PortListening bool
	HTTPReachable bool
	HTTPStatus    int
	Error         string
}


type Manager struct {
	mu              sync.Mutex
	deployMu        sync.Mutex
	store           *state.Store
	legacyStateFile string
	serviceDir      string
	runnerDir       string
	envDir          string
	journalDir      string
	users           *linuxuser.Manager
}

func New(store *state.Store, legacyStateFile string, users *linuxuser.Manager) *Manager {
	return &Manager{
		store:           store,
		legacyStateFile: legacyStateFile,
		serviceDir:      "/etc/systemd/system",
		runnerDir:       "/var/lib/open-go-panel/runners",
		envDir:          "/var/lib/open-go-panel/env",
		journalDir:      "/etc/systemd",
		users:           users,
	}
}

func (m *Manager) List() ([]App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	apps, err := m.load()
	if err != nil {
		return nil, err
	}

	sort.Slice(apps, func(i, j int) bool {
		return apps[i].ID < apps[j].ID
	})

	return apps, nil
}

func (m *Manager) Get(id int64) (App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	apps, err := m.load()
	if err != nil {
		return App{}, err
	}

	for _, app := range apps {
		if app.ID == id {
			return app, nil
		}
	}

	return App{}, fmt.Errorf("app %d not found", id)
}

func (m *Manager) Create(username, name, appType string) (App, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !appNamePattern.MatchString(name) {
		return App{}, errors.New("app name must contain only lowercase letters, digits, underscore or hyphen")
	}

	switch appType {
	case "go", "node", "static", "worker":
	default:
		return App{}, errors.New("unsupported app type")
	}

	managed, err := m.userManaged(username)
	if err != nil {
		return App{}, err
	}
	if !managed {
		return App{}, fmt.Errorf("user %q is not managed by Open Go Panel", username)
	}

	account, err := user.Lookup(username)
	if err != nil {
		return App{}, fmt.Errorf("lookup user %q: %w", username, err)
	}

	apps, err := m.load()
	if err != nil {
		return App{}, err
	}

	for _, existing := range apps {
		if existing.User == username && existing.Name == name {
			return App{}, fmt.Errorf("app %q already exists for user %q", name, username)
		}
	}

	root := filepath.Join(account.HomeDir, "apps", name)
	if err := os.MkdirAll(root, 0750); err != nil {
		return App{}, fmt.Errorf("create app directory: %w", err)
	}

	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return App{}, fmt.Errorf("parse uid: %w", err)
	}
	gid, err := strconv.Atoi(account.Gid)
	if err != nil {
		return App{}, fmt.Errorf("parse gid: %w", err)
	}

	if err := os.Chown(filepath.Join(account.HomeDir, "apps"), uid, gid); err != nil {
		return App{}, fmt.Errorf("chown apps directory: %w", err)
	}
	if err := os.Chown(root, uid, gid); err != nil {
		return App{}, fmt.Errorf("chown app directory: %w", err)
	}

	app := App{
		ID:        nextID(apps),
		User:      username,
		Name:      name,
		Type:      appType,
		Root:      root,
		CreatedAt: time.Now().UTC(),
	}

	if appType == "go" || appType == "node" {
		port, err := nextPort(apps)
		if err != nil {
			return App{}, err
		}
		app.Port = port
	}

	apps = append(apps, app)
	if err := m.save(apps); err != nil {
		return App{}, err
	}

	return app, nil
}

func (m *Manager) DeployConfig(id int64) (DeployConfig, error) {
	var cfg DeployConfig
	var deployedAt string
	err := m.store.DB().QueryRow(`
		SELECT repository, branch, current_commit, previous_commit, auto_deploy, deployed_at
		FROM deployments
		WHERE app_id = ?
	`, id).Scan(&cfg.Repository, &cfg.Branch, &cfg.CurrentCommit, &cfg.PreviousCommit, &cfg.AutoDeploy, &deployedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return DeployConfig{Branch: "main"}, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read deployment config: %w", err)
	}
	if deployedAt != "" {
		cfg.DeployedAt, _ = time.Parse(time.RFC3339Nano, deployedAt)
	}
	return cfg, nil
}

func (m *Manager) SetDeployConfig(id int64, repository, branch string, autoDeploy bool) error {
	repository = strings.TrimSpace(repository)
	branch = strings.TrimSpace(branch)
	if repository == "" {
		return errors.New("Git repository is required")
	}
	if strings.ContainsAny(repository, " \t\r\n") {
		return errors.New("Git repository URL must not contain whitespace")
	}
	if !(strings.HasPrefix(repository, "https://") || strings.HasPrefix(repository, "ssh://") || strings.HasPrefix(repository, "git@")) {
		return errors.New("Git repository must use https://, ssh:// or git@")
	}
	if !branchPattern.MatchString(branch) || strings.Contains(branch, "..") || strings.HasPrefix(branch, "-") {
		return errors.New("invalid Git branch")
	}
	if _, err := m.Get(id); err != nil {
		return err
	}
	_, err := m.store.DB().Exec(`
		INSERT INTO deployments(app_id, repository, branch, auto_deploy)
		VALUES(?, ?, ?, ?)
		ON CONFLICT(app_id) DO UPDATE SET
			repository=excluded.repository,
			branch=excluded.branch,
			auto_deploy=excluded.auto_deploy
	`, id, repository, branch, autoDeploy)
	if err != nil {
		return fmt.Errorf("save deployment config: %w", err)
	}
	return nil
}

func (m *Manager) DeployKeyInfo(id int64) (DeployKeyInfo, error) {
	app, err := m.Get(id)
	if err != nil {
		return DeployKeyInfo{}, err
	}
	cfg, err := m.DeployConfig(id)
	if err != nil {
		return DeployKeyInfo{}, err
	}

	account, err := user.Lookup(app.User)
	if err != nil {
		return DeployKeyInfo{}, fmt.Errorf("lookup user %q: %w", app.User, err)
	}
	keyPath := filepath.Join(account.HomeDir, ".ssh", fmt.Sprintf("ogp-app-%d", app.ID))
	knownHosts := filepath.Join(account.HomeDir, ".ssh", "known_hosts")
	info := DeployKeyInfo{
		KeyPath:    keyPath,
		KnownHosts: knownHosts,
		Host:       repositorySSHHost(cfg.Repository),
	}

	if data, err := os.ReadFile(keyPath + ".pub"); err == nil {
		info.PublicKey = strings.TrimSpace(string(data))
		info.Generated = info.PublicKey != ""
	} else if !errors.Is(err, os.ErrNotExist) {
		return info, fmt.Errorf("read deploy public key: %w", err)
	}

	if info.Host != "" {
		cmd := exec.Command("ssh-keygen", "-F", info.Host, "-f", knownHosts)
		if err := cmd.Run(); err == nil {
			info.HostKnown = true
		}
	}
	return info, nil
}

func (m *Manager) EnsureDeployKey(ctx context.Context, id int64) (DeployKeyInfo, error) {
	app, err := m.Get(id)
	if err != nil {
		return DeployKeyInfo{}, err
	}
	account, err := user.Lookup(app.User)
	if err != nil {
		return DeployKeyInfo{}, fmt.Errorf("lookup user %q: %w", app.User, err)
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return DeployKeyInfo{}, fmt.Errorf("parse uid: %w", err)
	}
	gid, err := strconv.Atoi(account.Gid)
	if err != nil {
		return DeployKeyInfo{}, fmt.Errorf("parse gid: %w", err)
	}

	sshDir := filepath.Join(account.HomeDir, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		return DeployKeyInfo{}, fmt.Errorf("create deploy SSH directory: %w", err)
	}
	if err := os.Chown(sshDir, uid, gid); err != nil {
		return DeployKeyInfo{}, fmt.Errorf("chown deploy SSH directory: %w", err)
	}
	if err := os.Chmod(sshDir, 0700); err != nil {
		return DeployKeyInfo{}, fmt.Errorf("chmod deploy SSH directory: %w", err)
	}

	keyPath := filepath.Join(sshDir, fmt.Sprintf("ogp-app-%d", app.ID))
	if _, err := os.Stat(keyPath); errors.Is(err, os.ErrNotExist) {
		if _, err := exec.LookPath("ssh-keygen"); err != nil {
			return DeployKeyInfo{}, errors.New("ssh-keygen is not installed")
		}
		if _, err := runAsUser(ctx, app.User, account.HomeDir, "ssh-keygen",
			"-q", "-t", "ed25519", "-N", "", "-C", fmt.Sprintf("open-go-panel-app-%d", app.ID), "-f", keyPath); err != nil {
			return DeployKeyInfo{}, err
		}
	} else if err != nil {
		return DeployKeyInfo{}, fmt.Errorf("inspect deploy key: %w", err)
	}
	_ = os.Chmod(keyPath, 0600)
	_ = os.Chmod(keyPath+".pub", 0644)
	_ = os.Chown(keyPath, uid, gid)
	_ = os.Chown(keyPath+".pub", uid, gid)
	return m.DeployKeyInfo(id)
}

func (m *Manager) TrustDeployHost(ctx context.Context, id int64) (DeployKeyInfo, error) {
	app, err := m.Get(id)
	if err != nil {
		return DeployKeyInfo{}, err
	}
	cfg, err := m.DeployConfig(id)
	if err != nil {
		return DeployKeyInfo{}, err
	}
	host := repositorySSHHost(cfg.Repository)
	if host == "" {
		return DeployKeyInfo{}, errors.New("repository is not an SSH Git URL")
	}
	if _, err := exec.LookPath("ssh-keyscan"); err != nil {
		return DeployKeyInfo{}, errors.New("ssh-keyscan is not installed")
	}
	account, err := user.Lookup(app.User)
	if err != nil {
		return DeployKeyInfo{}, fmt.Errorf("lookup user %q: %w", app.User, err)
	}
	uid, _ := strconv.Atoi(account.Uid)
	gid, _ := strconv.Atoi(account.Gid)
	sshDir := filepath.Join(account.HomeDir, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		return DeployKeyInfo{}, err
	}
	_ = os.Chown(sshDir, uid, gid)
	_ = os.Chmod(sshDir, 0700)

	out, err := exec.CommandContext(ctx, "ssh-keyscan", "-H", "-T", "5", host).CombinedOutput()
	if err != nil {
		return DeployKeyInfo{}, fmt.Errorf("scan SSH host %s: %w: %s", host, err, strings.TrimSpace(string(out)))
	}
	if strings.TrimSpace(string(out)) == "" {
		return DeployKeyInfo{}, fmt.Errorf("ssh-keyscan returned no keys for %s", host)
	}

	knownHosts := filepath.Join(sshDir, "known_hosts")
	existing, _ := os.ReadFile(knownHosts)
	content := strings.TrimRight(string(existing), "\n")
	if content != "" {
		content += "\n"
	}
	content += strings.TrimSpace(string(out)) + "\n"
	if err := os.WriteFile(knownHosts, []byte(content), 0600); err != nil {
		return DeployKeyInfo{}, fmt.Errorf("write known_hosts: %w", err)
	}
	_ = os.Chown(knownHosts, uid, gid)
	return m.DeployKeyInfo(id)
}

func repositorySSHHost(repository string) string {
	repository = strings.TrimSpace(repository)
	if strings.HasPrefix(repository, "ssh://") {
		u, err := url.Parse(repository)
		if err == nil {
			return u.Hostname()
		}
		return ""
	}
	if at := strings.Index(repository, "@"); at >= 0 {
		rest := repository[at+1:]
		if colon := strings.Index(rest, ":"); colon > 0 {
			return rest[:colon]
		}
	}
	return ""
}

func (m *Manager) gitEnvironment(id int64, repository string) ([]string, error) {
	if repositorySSHHost(repository) == "" {
		return nil, nil
	}
	info, err := m.DeployKeyInfo(id)
	if err != nil {
		return nil, err
	}
	if !info.Generated {
		return nil, errors.New("private SSH repository requires a deploy key; generate one first")
	}
	if !info.HostKnown {
		return nil, fmt.Errorf("SSH host %q is not trusted yet", info.Host)
	}
	command := "ssh -i " + info.KeyPath + " -o IdentitiesOnly=yes -o UserKnownHostsFile=" + info.KnownHosts
	return []string{"GIT_SSH_COMMAND=" + command}, nil
}

func runAsUserEnv(ctx context.Context, username, dir string, env []string, name string, args ...string) (string, error) {
	runArgs := []string{"-u", username, "--"}
	if len(env) > 0 {
		runArgs = append(runArgs, "env")
		runArgs = append(runArgs, env...)
	}
	runArgs = append(runArgs, name)
	runArgs = append(runArgs, args...)
	cmd := exec.CommandContext(ctx, "runuser", runArgs...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (m *Manager) Deploy(ctx context.Context, id int64) error {
	m.deployMu.Lock()
	defer m.deployMu.Unlock()

	return m.deployUnlocked(ctx, id)
}

func (m *Manager) deployUnlocked(ctx context.Context, id int64) error {
	return m.deployStaged(ctx, id, false)
}

func (m *Manager) RemoteCommit(ctx context.Context, id int64) (string, error) {
	app, err := m.Get(id)
	if err != nil {
		return "", err
	}
	cfg, err := m.DeployConfig(id)
	if err != nil {
		return "", err
	}
	if cfg.Repository == "" {
		return "", errors.New("Git repository is not configured")
	}
	gitEnv, err := m.gitEnvironment(id, cfg.Repository)
	if err != nil {
		return "", err
	}
	out, err := runAsUserEnv(ctx, app.User, app.Root, gitEnv, "git", "ls-remote", cfg.Repository, "refs/heads/"+cfg.Branch)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(out)
	if len(fields) < 1 || len(fields[0]) != 40 {
		return "", fmt.Errorf("branch %q was not found in remote repository", cfg.Branch)
	}
	return fields[0], nil
}

func (m *Manager) RunAutoDeploys(ctx context.Context) (int, error) {
	apps, err := m.List()
	if err != nil {
		return 0, err
	}
	deployed := 0
	var failures []string
	for _, app := range apps {
		cfg, err := m.DeployConfig(app.ID)
		if err != nil {
			failures = append(failures, fmt.Sprintf("app %d: %v", app.ID, err))
			continue
		}
		if !cfg.AutoDeploy || strings.TrimSpace(cfg.Repository) == "" {
			continue
		}
		remote, err := m.RemoteCommit(ctx, app.ID)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", app.Name, err))
			continue
		}
		if remote == cfg.CurrentCommit {
			continue
		}
		// A manual rollback may have disabled auto deploy while the
		// remote query was running. Re-check the setting under deployMu.
		m.deployMu.Lock()
		latest, checkErr := m.DeployConfig(app.ID)
		skip := checkErr == nil && !shouldContinueAutoDeploy(cfg, latest, remote)
		if checkErr != nil {
			err = checkErr
		} else if !skip {
			err = m.deployUnlocked(ctx, app.ID)
		}
		m.deployMu.Unlock()
		if skip {
			continue
		}
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", app.Name, err))
			continue
		}
		deployed++
	}
	if len(failures) > 0 {
		return deployed, errors.New(strings.Join(failures, "; "))
	}
	return deployed, nil
}

func shouldContinueAutoDeploy(before, after DeployConfig, remote string) bool {
	return after.AutoDeploy &&
		after.Repository == before.Repository &&
		after.Branch == before.Branch &&
		after.CurrentCommit != remote
}
// Rollback and Deploy must never mutate the same working tree concurrently.
func (m *Manager) Rollback(ctx context.Context, id int64) error {
	m.deployMu.Lock()
	defer m.deployMu.Unlock()

	return m.deployStaged(ctx, id, true)
}

// A manual rollback must not be immediately undone by the periodic auto deploy.
func (m *Manager) recordRollback(id int64, target, original string) error {
	_, err := m.store.DB().Exec(`
		UPDATE deployments
		SET current_commit = ?, previous_commit = ?, deployed_at = ?, auto_deploy = 0
		WHERE app_id = ?
	`, target, original, time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

// Recover both Git files and runtime dependencies after a failed deployment.
// A Git reset alone cannot repair node_modules modified by a failed npm ci.
// The recovery runs independently of the canceled HTTP request, with a limit.
func (m *Manager) recoverDeployment(app App, id int64, previous string, cause error) error {
	if previous == "" {
		return fmt.Errorf("%w; no prior Git revision is available for recovery", cause)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if _, err := runAsUser(ctx, app.User, app.Root, "git", "reset", "--hard", previous); err != nil {
		return errors.Join(cause, fmt.Errorf("reset previous commit %s failed: %w", previous, err))
	}
	if err := m.prepareDeployment(ctx, app); err != nil {
		return errors.Join(cause, fmt.Errorf("restore previous dependencies failed: %w", err))
	}
	if app.Type != "static" {
		if err := m.Restart(ctx, id); err != nil {
			return errors.Join(cause, fmt.Errorf("restart previous deployment failed: %w", err))
		}
	}
	return fmt.Errorf("%w; previous commit %s restored", cause, previous)
}

func (m *Manager) prepareDeployment(ctx context.Context, app App) error {
	switch app.Type {
	case "node":
		if _, err := os.Stat(filepath.Join(app.Root, "package-lock.json")); err == nil {
			_, err = runAsUser(ctx, app.User, app.Root, "npm", "ci")
			return err
		}
		if _, err := os.Stat(filepath.Join(app.Root, "package.json")); err == nil {
			_, err = runAsUser(ctx, app.User, app.Root, "npm", "install")
			return err
		}
	case "go":
		if _, err := os.Stat(filepath.Join(app.Root, "go.mod")); err == nil {
			// runuser inherits the panel service's PATH, which may not
			// contain /usr/local/go/bin even when Go is installed and
			// available to the application's own systemd service.
			goBinary, err := resolveDeployGo()
			if err != nil {
				return err
			}
			_, err = runAsUser(ctx, app.User, app.Root, goBinary, "mod", "download")
			return err
		}
	}
	return nil
}

// resolveDeployGo returns an absolute executable path so runuser does not
// depend on the panel daemon's PATH when preparing staged Go releases.
func resolveDeployGo() (string, error) {
	candidates := []string{"/usr/local/go/bin/go", "/usr/local/bin/go", "/usr/bin/go", "/bin/go"}
	if path, err := exec.LookPath("go"); err == nil {
		candidates = append(candidates, path)
	}
	if path := firstExecutableGo(candidates); path != "" {
		return path, nil
	}
	return "", errors.New("Go toolchain is not installed or executable; install Go from Software before deploying this application")
}

func firstExecutableGo(candidates []string) string {
	for _, candidate := range candidates {
		absolute, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		info, err := os.Stat(absolute)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
			return absolute
		}
	}
	return ""
}

func runAsUser(ctx context.Context, username, dir, name string, args ...string) (string, error) {
	runArgs := []string{"-u", username, "--", name}
	runArgs = append(runArgs, args...)
	cmd := exec.CommandContext(ctx, "runuser", runArgs...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (m *Manager) RuntimeHealth(ctx context.Context, id int64) RuntimeHealth {
	app, err := m.Get(id)
	if err != nil {
		return RuntimeHealth{Error: err.Error()}
	}
	health := RuntimeHealth{ProcessStatus: m.Status(ctx, id)}
	if app.Port <= 0 {
		return health
	}

	dialer := net.Dialer{Timeout: time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(app.Port)))
	if err == nil {
		health.PortListening = true
		_ = conn.Close()
	}

	client := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(app.Port)+"/", nil)
	if err != nil {
		health.Error = err.Error()
		return health
	}
	resp, err := client.Do(req)
	if err != nil {
		if health.Error == "" {
			health.Error = err.Error()
		}
		return health
	}
	health.HTTPReachable = true
	health.HTTPStatus = resp.StatusCode
	_ = resp.Body.Close()
	return health
}

func (m *Manager) Delete(ctx context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	apps, err := m.load()
	if err != nil {
		return err
	}
	index := -1
	var app App
	for i := range apps {
		if apps[i].ID == id {
			index = i
			app = apps[i]
			break
		}
	}
	if index < 0 {
		return fmt.Errorf("app %d not found", id)
	}

	if app.Type != "static" {
		_ = exec.CommandContext(ctx, "systemctl", "disable", "--now", serviceName(id)).Run()
	}
	_ = os.Remove(filepath.Join(m.serviceDir, serviceName(id)))
	_ = os.Remove(runnerPath(m.runnerDir, id))
	_ = os.Remove(envPath(m.envDir, id))
	_ = os.RemoveAll(filepath.Join(m.journalDir, "journald@"+journalNamespace(id)+".conf.d"))
	_ = exec.CommandContext(ctx, "systemctl", "daemon-reload").Run()

	apps = append(apps[:index], apps[index+1:]...)
	if err := m.save(apps); err != nil {
		return err
	}
	return nil
}

func (m *Manager) SetPort(ctx context.Context, id int64, port int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if port < 1024 || port > 65535 {
		return errors.New("application port must be between 1024 and 65535")
	}

	apps, err := m.load()
	if err != nil {
		return err
	}

	index := -1
	for i := range apps {
		if apps[i].ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		return fmt.Errorf("app %d not found", id)
	}
	app := apps[index]
	if app.Type != "go" && app.Type != "node" {
		return errors.New("only Go and Node applications use a managed HTTP port")
	}
	if app.Service.Mode == "raw" {
		return errors.New("managed port changes are unavailable in raw systemd mode")
	}
	if app.Port == port {
		return nil
	}

	for _, other := range apps {
		if other.ID != id && other.Port == port {
			return fmt.Errorf("port %d is already assigned to app %q", port, other.Name)
		}
	}
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("port %d is already in use: %w", port, err)
	}
	_ = listener.Close()

	wasActive := false
	if out, err := exec.CommandContext(ctx, "systemctl", "is-active", serviceName(id)).Output(); err == nil && strings.TrimSpace(string(out)) == "active" {
		wasActive = true
	}

	previous := app
	app.Port = port
	apps[index] = app

	if err := m.ensureStorage(); err != nil {
		return err
	}
	if err := m.writeUnit(app); err != nil {
		return err
	}
	if err := systemctl(ctx, "daemon-reload"); err != nil {
		_ = m.writeUnit(previous)
		_ = systemctl(context.Background(), "daemon-reload")
		return err
	}
	if wasActive {
		if err := systemctl(ctx, "restart", serviceName(id)); err != nil {
			_ = m.writeUnit(previous)
			_ = systemctl(context.Background(), "daemon-reload")
			_ = systemctl(context.Background(), "restart", serviceName(id))
			return err
		}
	}

	if err := m.save(apps); err != nil {
		_ = m.writeUnit(previous)
		_ = systemctl(context.Background(), "daemon-reload")
		if wasActive {
			_ = systemctl(context.Background(), "restart", serviceName(id))
		}
		return err
	}
	return nil
}

func (m *Manager) SetCommand(ctx context.Context, id int64, command string) error {
	return m.SetServiceConfig(ctx, id, ServiceConfig{
		Mode:       "form",
		RunMode:    "custom",
		Command:    command,
		Restart:    "on-failure",
		RestartSec:       3,
		TimeoutStopSec:   15,
		LimitNOFILE:      65535,
		TasksMax:         256,
		LogRetentionDays: 7,
		AutoStart:        true,
	})
}

func (m *Manager) SetEnvironmentVariable(ctx context.Context, id int64, key, value string) error {
	if !envNamePattern.MatchString(key) {
		return errors.New("invalid environment variable name")
	}
	app, err := m.Get(id)
	if err != nil {
		return err
	}
	if app.Type == "static" {
		return errors.New("static apps do not use service environment variables")
	}
	if app.Service.Mode == "raw" {
		return errors.New("database attachment is unavailable in raw systemd mode")
	}

	cfg := app.Service
	if cfg.Mode == "" {
		cfg.Mode = "form"
	}
	if cfg.RunMode == "" {
		switch app.Type {
		case "go":
			cfg.RunMode = "go-build"
		case "node":
			cfg.RunMode = "node-npm"
		default:
			cfg.RunMode = "custom"
		}
	}
	if cfg.Restart == "" {
		cfg.Restart = "on-failure"
	}
	if cfg.RestartSec == 0 {
		cfg.RestartSec = 3
	}
	if cfg.TimeoutStopSec == 0 {
		cfg.TimeoutStopSec = 15
	}
	if cfg.LimitNOFILE == 0 {
		cfg.LimitNOFILE = 65535
	}
	if cfg.TasksMax == 0 {
		cfg.TasksMax = 256
	}
	if cfg.LogRetentionDays == 0 {
		cfg.LogRetentionDays = 7
	}
	if cfg.Path == "" {
		cfg.Path = defaultPath(app)
	}
	if cfg.WorkingDirectory == "" {
		cfg.WorkingDirectory = app.Root
	}

	var lines []string
	replaced := false
	for _, line := range strings.Split(cfg.Environment, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, key+"=") {
			lines = append(lines, key+"="+value)
			replaced = true
			continue
		}
		lines = append(lines, line)
	}
	if !replaced {
		lines = append(lines, key+"="+value)
	}
	cfg.Environment = strings.Join(lines, "\n")
	return m.SetServiceConfig(ctx, id, cfg)
}

func (m *Manager) RemoveEnvironmentVariable(ctx context.Context, id int64, key string) error {
	if !envNamePattern.MatchString(key) {
		return errors.New("invalid environment variable name")
	}
	app, err := m.Get(id)
	if err != nil {
		return err
	}
	if app.Type == "static" {
		return errors.New("static apps do not use service environment variables")
	}
	if app.Service.Mode == "raw" {
		return errors.New("database attachment is unavailable in raw systemd mode")
	}

	cfg := app.Service
	var lines []string
	for _, line := range strings.Split(cfg.Environment, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, key+"=") {
			continue
		}
		lines = append(lines, line)
	}
	cfg.Environment = strings.Join(lines, "\n")
	return m.SetServiceConfig(ctx, id, cfg)
}

func (m *Manager) SetServiceConfig(ctx context.Context, id int64, cfg ServiceConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	apps, err := m.load()
	if err != nil {
		return err
	}

	index := -1
	for i := range apps {
		if apps[i].ID == id {
			index = i
			break
		}
	}
	if index == -1 {
		return fmt.Errorf("app %d not found", id)
	}
	if apps[index].Type == "static" {
		return errors.New("static apps do not use systemd services")
	}

	cfg.Mode = strings.TrimSpace(cfg.Mode)
	if cfg.Mode == "" {
		cfg.Mode = "form"
	}

	if cfg.Mode == "raw" {
		cfg.RawUnit = strings.TrimSpace(cfg.RawUnit)
		if cfg.RawUnit == "" {
			return errors.New("raw unit is required")
		}
		apps[index].Service = cfg

		if err := m.writeRawUnit(ctx, apps[index]); err != nil {
			return err
		}
	} else {
		if err := validateServiceConfig(apps[index].Type, &cfg); err != nil {
			return err
		}

		if cfg.WorkingDirectory == "" {
			cfg.WorkingDirectory = apps[index].Root
		}
		if cfg.Path == "" {
			cfg.Path = defaultPath(apps[index])
		}

		apps[index].Service = cfg
		apps[index].Command = cfg.Command

		if err := m.ensureStorage(); err != nil {
			return err
		}
		if err := m.writeEnvironment(apps[index]); err != nil {
			return err
		}
		if err := m.writeRunner(apps[index]); err != nil {
			return err
		}
		if err := m.writeUnit(apps[index]); err != nil {
			return err
		}
		if err := m.writeJournalConfig(apps[index]); err != nil {
			return err
		}
	}

	if err := systemctl(ctx, "daemon-reload"); err != nil {
		return err
	}
	if cfg.Mode == "raw" || cfg.AutoStart {
		if err := systemctl(ctx, "enable", serviceName(id)); err != nil {
			return err
		}
	} else {
		if err := systemctl(ctx, "disable", serviceName(id)); err != nil {
			return err
		}
	}
	if cfg.Mode != "raw" && cfg.LogRetentionDays > 0 {
		_ = exec.CommandContext(ctx, "systemctl", "try-restart", journalServiceName(id)).Run()
	}

	return m.save(apps)
}

func (m *Manager) Unit(id int64) (string, error) {
	if _, err := m.Get(id); err != nil {
		return "", err
	}

	data, err := os.ReadFile(filepath.Join(m.serviceDir, serviceName(id)))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read systemd unit: %w", err)
	}

	return string(data), nil
}

func (m *Manager) QueryLogs(ctx context.Context, id int64, query journal.Query) (journal.Result, error) {
	app, err := m.Get(id)
	if err != nil {
		return journal.Result{}, err
	}
	query.Service = serviceName(id)
	if app.Service.Mode != "raw" && app.Service.LogRetentionDays > 0 {
		query.Namespace = journalNamespace(id)
	}
	return journal.Read(ctx, query)
}

func (m *Manager) Logs(ctx context.Context, id int64, lines int) (string, error) {
	if lines <= 0 || lines > 1000 {
		lines = 200
	}
	app, err := m.Get(id)
	if err != nil {
		return "", err
	}

	args := []string{"-u", serviceName(id), "-n", strconv.Itoa(lines), "--no-pager", "-o", "short-iso"}
	if app.Service.Mode != "raw" && app.Service.LogRetentionDays > 0 {
		args = append([]string{"--namespace=" + journalNamespace(id)}, args...)
	}
	cmd := exec.CommandContext(ctx, "journalctl", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("read app logs: %w: %s", err, strings.TrimSpace(string(out)))
	}

	return string(out), nil
}

func (m *Manager) Start(ctx context.Context, id int64) error {
	app, err := m.Get(id)
	if err != nil {
		return err
	}
	if err := validateRunnable(app); err != nil {
		return err
	}
	return systemctl(ctx, "start", serviceName(id))
}

func (m *Manager) Stop(ctx context.Context, id int64) error {
	app, err := m.Get(id)
	if err != nil {
		return err
	}
	if err := validateRunnable(app); err != nil {
		return err
	}
	return systemctl(ctx, "stop", serviceName(id))
}

func (m *Manager) Restart(ctx context.Context, id int64) error {
	app, err := m.Get(id)
	if err != nil {
		return err
	}
	if err := validateRunnable(app); err != nil {
		return err
	}
	return systemctl(ctx, "restart", serviceName(id))
}

func (m *Manager) Status(ctx context.Context, id int64) string {
	app, err := m.Get(id)
	if err != nil || app.Type == "static" {
		return "not configured"
	}

	if app.Service.Mode == "raw" {
		if strings.TrimSpace(app.Service.RawUnit) == "" {
			return "not configured"
		}
	} else if strings.TrimSpace(runnerCommand(app)) == "" {
		return "not configured"
	}

	cmd := exec.CommandContext(ctx, "systemctl", "is-active", serviceName(id))
	out, err := cmd.Output()
	status := strings.TrimSpace(string(out))
	if err != nil {
		if status != "" {
			return status
		}
		return "inactive"
	}

	return status
}

func (m *Manager) writeRunner(app App) error {
	if err := m.ensureStorage(); err != nil {
		return err
	}
	if err := os.MkdirAll(m.runnerDir, 0755); err != nil {
		return fmt.Errorf("create runner directory: %w", err)
	}

	path := runnerPath(m.runnerDir, app.ID)
	content := "#!/usr/bin/env bash\nset -e\n" + runnerCommand(app) + "\n"

	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		return fmt.Errorf("write app runner: %w", err)
	}
	if err := os.Chmod(path, 0755); err != nil {
		return fmt.Errorf("chmod app runner: %w", err)
	}

	return nil
}

func (m *Manager) writeEnvironment(app App) error {
	if err := os.MkdirAll(m.envDir, 0700); err != nil {
		return fmt.Errorf("create environment directory: %w", err)
	}

	content := strings.TrimSpace(app.Service.Environment)
	if content != "" {
		content += "\n"
	}

	if err := os.WriteFile(envPath(m.envDir, app.ID), []byte(content), 0600); err != nil {
		return fmt.Errorf("write environment file: %w", err)
	}

	return nil
}

func (m *Manager) writeUnit(app App) error {
	var b strings.Builder

	fmt.Fprintf(&b, "[Unit]\nDescription=Open Go Panel app %d (%s/%s)\nAfter=network-online.target\nWants=network-online.target\n\n", app.ID, app.User, app.Name)
	workingDir := app.Service.WorkingDirectory
	if workingDir == "" {
		workingDir = app.Root
	}
	fmt.Fprintf(&b, "[Service]\nType=simple\nUser=%s\nWorkingDirectory=%s\n", app.User, workingDir)

	pathValue := app.Service.Path
	if pathValue == "" {
		pathValue = defaultPath(app)
	}
	fmt.Fprintf(&b, "Environment=PATH=%s\n", pathValue)

	if app.Port > 0 {
		fmt.Fprintf(&b, "Environment=PORT=%d\n", app.Port)
	}

	fmt.Fprintf(&b, "EnvironmentFile=-%s\n", envPath(m.envDir, app.ID))
	fmt.Fprintf(&b, "ExecStart=%s\n", runnerPath(m.runnerDir, app.ID))

	restart := app.Service.Restart
	if restart == "" {
		restart = "on-failure"
	}
	fmt.Fprintf(&b, "Restart=%s\nRestartSec=%d\n", restart, app.Service.RestartSec)
	if app.Service.TimeoutStopSec > 0 {
		fmt.Fprintf(&b, "TimeoutStopSec=%d\n", app.Service.TimeoutStopSec)
	}
	if app.Service.LimitNOFILE > 0 {
		fmt.Fprintf(&b, "LimitNOFILE=%d\n", app.Service.LimitNOFILE)
	}
	if app.Service.TasksMax > 0 {
		fmt.Fprintf(&b, "TasksMax=%d\n", app.Service.TasksMax)
	}
	if app.Service.LogRetentionDays > 0 {
		fmt.Fprintf(&b, "LogNamespace=%s\n", journalNamespace(app.ID))
	}

	if app.Service.CPUQuota != "" {
		fmt.Fprintf(&b, "CPUQuota=%s\n", app.Service.CPUQuota)
	}
	if app.Service.MemoryMax != "" {
		fmt.Fprintf(&b, "MemoryMax=%s\n", app.Service.MemoryMax)
	}

	b.WriteString("\n[Install]\nWantedBy=multi-user.target\n")

	path := filepath.Join(m.serviceDir, serviceName(app.ID))
	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		return fmt.Errorf("write systemd unit: %w", err)
	}

	return nil
}

func (m *Manager) writeRawUnit(ctx context.Context, app App) error {
	tmp, err := os.CreateTemp(m.serviceDir, ".open-go-panel-*.service")
	if err != nil {
		return fmt.Errorf("create temporary systemd unit: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.WriteString(app.Service.RawUnit + "\n"); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temporary systemd unit: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary systemd unit: %w", err)
	}

	verify := exec.CommandContext(ctx, "systemd-analyze", "verify", tmpName)
	if out, err := verify.CombinedOutput(); err != nil {
		return fmt.Errorf("invalid systemd unit: %w: %s", err, strings.TrimSpace(string(out)))
	}

	path := filepath.Join(m.serviceDir, serviceName(app.ID))
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("install systemd unit: %w", err)
	}

	return nil
}

func validateServiceConfig(appType string, cfg *ServiceConfig) error {
	if cfg.Mode != "form" {
		return errors.New("unsupported service mode")
	}

	switch cfg.Restart {
	case "", "no", "on-failure", "always":
	default:
		return errors.New("unsupported restart policy")
	}
	if cfg.Restart == "" {
		cfg.Restart = "on-failure"
	}

	if cfg.RestartSec < 0 || cfg.RestartSec > 300 {
		return errors.New("restart delay must be between 0 and 300 seconds")
	}
	if cfg.TimeoutStopSec < 0 || cfg.TimeoutStopSec > 3600 {
		return errors.New("stop timeout must be between 0 and 3600 seconds")
	}
	if cfg.LimitNOFILE < 0 || cfg.LimitNOFILE > 1048576 {
		return errors.New("LimitNOFILE is out of range")
	}
	if cfg.TasksMax < 0 || cfg.TasksMax > 1048576 {
		return errors.New("TasksMax is out of range")
	}
	if cfg.LogRetentionDays < 0 || cfg.LogRetentionDays > 3650 {
		return errors.New("log retention must be between 0 and 3650 days")
	}
	if cfg.WorkingDirectory != "" {
		if !filepath.IsAbs(cfg.WorkingDirectory) || strings.ContainsAny(cfg.WorkingDirectory, "\r\n") {
			return errors.New("working directory must be an absolute path")
		}
	}
	if strings.ContainsAny(cfg.Path, "\r\n") {
		return errors.New("PATH must be a single line")
	}

	switch cfg.RunMode {
	case "":
		cfg.RunMode = "custom"
	case "custom", "go-air", "go-build", "go-run", "node-npm":
	default:
		return errors.New("unsupported run mode")
	}

	if (cfg.RunMode == "go-air" || cfg.RunMode == "go-build" || cfg.RunMode == "go-run") && appType != "go" {
		return errors.New("selected run mode is only available for Go apps")
	}
	if cfg.RunMode == "node-npm" && appType != "node" {
		return errors.New("node npm mode is only available for Node.js apps")
	}
	if cfg.RunMode == "custom" && strings.TrimSpace(cfg.Command) == "" {
		return errors.New("custom command is required")
	}

	if cfg.CPUQuota != "" && !cpuPattern.MatchString(cfg.CPUQuota) {
		return errors.New("CPU quota must look like 100% or 200%")
	}
	if cfg.MemoryMax != "" && !memoryPattern.MatchString(cfg.MemoryMax) {
		return errors.New("memory limit must look like 512M or 2G")
	}

	for _, line := range strings.Split(cfg.Environment, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid environment line %q", line)
		}
		if !envNamePattern.MatchString(parts[0]) {
			return fmt.Errorf("invalid environment variable name %q", parts[0])
		}
	}

	return nil
}

func runnerCommand(app App) string {
	switch app.Service.RunMode {
	case "go-air":
		return "exec air"
	case "go-build":
		return "go build -o .ogp-app . && exec ./.ogp-app"
	case "go-run":
		return "exec go run ."
	case "node-npm":
		return "exec npm start"
	default:
		if app.Service.Command != "" {
			return app.Service.Command
		}
		return app.Command
	}
}


func (m *Manager) ensureStorage() error {
	root := filepath.Dir(m.legacyStateFile)
	if err := os.MkdirAll(root, 0755); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	if err := os.Chmod(root, 0755); err != nil {
		return fmt.Errorf("chmod state directory: %w", err)
	}
	if err := os.MkdirAll(m.runnerDir, 0755); err != nil {
		return fmt.Errorf("create runner directory: %w", err)
	}
	if err := os.Chmod(m.runnerDir, 0755); err != nil {
		return fmt.Errorf("chmod runner directory: %w", err)
	}
	if err := os.MkdirAll(m.envDir, 0700); err != nil {
		return fmt.Errorf("create environment directory: %w", err)
	}
	if err := os.Chmod(m.envDir, 0700); err != nil {
		return fmt.Errorf("chmod environment directory: %w", err)
	}
	return nil
}

func (m *Manager) EnsureStorage() error {
	return m.ensureStorage()
}

func (m *Manager) writeJournalConfig(app App) error {
	if app.Service.LogRetentionDays <= 0 {
		return nil
	}
	dir := filepath.Join(m.journalDir, "journald@"+journalNamespace(app.ID)+".conf.d")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create journald config directory: %w", err)
	}
	content := fmt.Sprintf("[Journal]\nMaxRetentionSec=%dday\n", app.Service.LogRetentionDays)
	if err := os.WriteFile(filepath.Join(dir, "open-go-panel.conf"), []byte(content), 0644); err != nil {
		return fmt.Errorf("write journald config: %w", err)
	}
	return nil
}

func defaultPath(app App) string {
	if app.Type == "go" {
		return filepath.Join("/home", app.User, "go", "bin") + ":/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin"
	}
	return "/usr/local/bin:/usr/bin:/bin"
}

func journalNamespace(id int64) string {
	return fmt.Sprintf("ogp-app-%d", id)
}

func journalServiceName(id int64) string {
	return fmt.Sprintf("systemd-journald@%s.service", journalNamespace(id))
}

func (m *Manager) StreamLogs(ctx context.Context, id int64, lines int, fn func(string) error) error {
	if lines < 0 || lines > 1000 {
		lines = 100
	}
	app, err := m.Get(id)
	if err != nil {
		return err
	}

	args := []string{"-u", serviceName(id), "-n", strconv.Itoa(lines), "-f", "--no-pager", "-o", "short-iso"}
	if app.Service.Mode != "raw" && app.Service.LogRetentionDays > 0 {
		args = append([]string{"--namespace=" + journalNamespace(id)}, args...)
	}

	cmd := exec.CommandContext(ctx, "journalctl", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open journal stream: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("open journal error stream: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start journal stream: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		if err := fn(scanner.Text()); err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("read journal stream: %w", err)
	}

	if err := cmd.Wait(); err != nil && ctx.Err() == nil {
		errText, _ := bufio.NewReader(stderr).ReadString('\n')
		return fmt.Errorf("journal stream stopped: %w: %s", err, strings.TrimSpace(errText))
	}
	return ctx.Err()
}

func validateRunnable(app App) error {
	if app.Type == "static" {
		return errors.New("static apps do not use systemd services")
	}

	if app.Service.Mode == "raw" {
		if strings.TrimSpace(app.Service.RawUnit) == "" {
			return errors.New("configure the systemd unit first")
		}
		return nil
	}

	if strings.TrimSpace(runnerCommand(app)) == "" {
		return errors.New("configure a start command first")
	}

	return nil
}

func systemctl(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "systemctl", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func serviceName(id int64) string {
	return fmt.Sprintf("open-go-panel-app-%d.service", id)
}

func runnerPath(dir string, id int64) string {
	return filepath.Join(dir, fmt.Sprintf("app-%d.sh", id))
}

func envPath(dir string, id int64) string {
	return filepath.Join(dir, fmt.Sprintf("app-%d.env", id))
}

func (m *Manager) userManaged(username string) (bool, error) {
	users, err := m.users.List(context.Background())
	if err != nil {
		return false, err
	}

	for _, u := range users {
		if u.Username == username {
			return true, nil
		}
	}

	return false, nil
}

func (m *Manager) load() ([]App, error) {
	rows, err := m.store.DB().Query(`
		SELECT id, owner, name, type, root, port, command, service_json, created_at
		FROM apps
		ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("query apps state: %w", err)
	}
	defer rows.Close()

	var apps []App
	for rows.Next() {
		var app App
		var serviceJSON string
		var createdAt string
		if err := rows.Scan(
			&app.ID,
			&app.User,
			&app.Name,
			&app.Type,
			&app.Root,
			&app.Port,
			&app.Command,
			&serviceJSON,
			&createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan app state: %w", err)
		}
		if strings.TrimSpace(serviceJSON) != "" {
			if err := json.Unmarshal([]byte(serviceJSON), &app.Service); err != nil {
				return nil, fmt.Errorf("decode app %d service state: %w", app.ID, err)
			}
		}
		if createdAt != "" {
			if t, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
				app.CreatedAt = t
			}
		}
		apps = append(apps, app)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate apps state: %w", err)
	}

	if len(apps) > 0 {
		if _, done, err := m.store.Setting("migration.apps_json_done"); err != nil {
			return nil, err
		} else if !done {
			if err := m.store.SetSetting("migration.apps_json_done", "1"); err != nil {
				return nil, fmt.Errorf("mark apps migration complete: %w", err)
			}
		}
	}
	if len(apps) == 0 {
		if _, done, err := m.store.Setting("migration.apps_json_done"); err != nil {
			return nil, err
		} else if !done {
			legacy, err := m.loadLegacy()
			if err != nil {
				return nil, err
			}
			if len(legacy) > 0 {
				if err := m.save(legacy); err != nil {
					return nil, fmt.Errorf("migrate legacy apps state: %w", err)
				}
				apps = legacy
			}
			if err := m.store.SetSetting("migration.apps_json_done", "1"); err != nil {
				return nil, fmt.Errorf("mark apps migration complete: %w", err)
			}
		}
	}

	return apps, nil
}

func (m *Manager) loadLegacy() ([]App, error) {
	data, err := os.ReadFile(m.legacyStateFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read legacy apps state: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil
	}
	var apps []App
	if err := json.Unmarshal(data, &apps); err != nil {
		return nil, fmt.Errorf("decode legacy apps state: %w", err)
	}
	return apps, nil
}

func (m *Manager) save(apps []App) error {
	tx, err := m.store.DB().Begin()
	if err != nil {
		return fmt.Errorf("begin apps state transaction: %w", err)
	}
	defer tx.Rollback()

	keep := make(map[int64]struct{}, len(apps))
	for _, app := range apps {
		serviceJSON, err := json.Marshal(app.Service)
		if err != nil {
			return fmt.Errorf("encode app %d service state: %w", app.ID, err)
		}
		createdAt := app.CreatedAt.UTC().Format(time.RFC3339Nano)
		if app.CreatedAt.IsZero() {
			createdAt = time.Now().UTC().Format(time.RFC3339Nano)
		}
		if _, err := tx.Exec(`
			INSERT INTO apps(id, owner, name, type, root, port, command, service_json, created_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				owner=excluded.owner,
				name=excluded.name,
				type=excluded.type,
				root=excluded.root,
				port=excluded.port,
				command=excluded.command,
				service_json=excluded.service_json,
				created_at=excluded.created_at
		`, app.ID, app.User, app.Name, app.Type, app.Root, app.Port, app.Command, string(serviceJSON), createdAt); err != nil {
			return fmt.Errorf("save app %d state: %w", app.ID, err)
		}
		keep[app.ID] = struct{}{}
	}

	rows, err := tx.Query(`SELECT id FROM apps`)
	if err != nil {
		return fmt.Errorf("query existing app ids: %w", err)
	}
	var stale []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		if _, ok := keep[id]; !ok {
			stale = append(stale, id)
		}
	}
	rows.Close()
	for _, id := range stale {
		if _, err := tx.Exec(`DELETE FROM apps WHERE id = ?`, id); err != nil {
			return fmt.Errorf("delete stale app %d state: %w", id, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit apps state: %w", err)
	}
	return nil
}

func nextID(apps []App) int64 {
	var maxID int64
	for _, app := range apps {
		if app.ID > maxID {
			maxID = app.ID
		}
	}
	return maxID + 1
}

func nextPort(apps []App) (int, error) {
	used := make(map[int]struct{}, len(apps))
	for _, app := range apps {
		if app.Port > 0 {
			used[app.Port] = struct{}{}
		}
	}

	for port := minPort; port <= maxPort; port++ {
		if _, exists := used[port]; exists {
			continue
		}
		listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			continue
		}
		_ = listener.Close()
		return port, nil
	}

	return 0, errors.New("no free application ports available")
}
