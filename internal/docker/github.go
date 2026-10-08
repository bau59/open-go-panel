package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const dockerGitKeysDir = "/etc/open-go-panel/docker-git"

var (
	githubOwnerRE = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`)
	githubRepoRE  = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)
	gitBranchRE   = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_./-]{0,127}$`)
)

type GitHubKeyInfo struct {
	Repository string
	PublicKey  string
	Generated  bool
}

type BuildTask struct {
	Running    bool
	Repository string
	Container  string
	Step       string
	Image      string
	Error      string
	StartedAt  time.Time
	FinishedAt time.Time
}

type githubRepository struct {
	Owner string
	Name  string
}

func parseGitHubRepository(input string) (githubRepository, error) {
	value := strings.TrimSpace(input)
	switch {
	case strings.HasPrefix(value, "https://github.com/"):
		value = strings.TrimPrefix(value, "https://github.com/")
	case strings.HasPrefix(value, "git@github.com:"):
		value = strings.TrimPrefix(value, "git@github.com:")
	case strings.Contains(value, "://") || strings.Contains(value, "@"):
		return githubRepository{}, errors.New("use a GitHub repository URL, e.g. https://github.com/owner/repo")
	}
	value = strings.TrimSuffix(strings.TrimSuffix(value, "/"), ".git")
	parts := strings.Split(value, "/")
	if len(parts) != 2 || !githubOwnerRE.MatchString(parts[0]) || !githubRepoRE.MatchString(parts[1]) ||
		parts[1] == "." || parts[1] == ".." || strings.Contains(parts[1], "..") {
		return githubRepository{}, errors.New("invalid GitHub repository; enter owner/repo")
	}
	return githubRepository{Owner: parts[0], Name: parts[1]}, nil
}

func (r githubRepository) ID() string { return r.Owner + "/" + r.Name }
func (r githubRepository) SSH() string { return "git@github.com:" + r.ID() + ".git" }
func (r githubRepository) HTTPS() string { return "https://github.com/" + r.ID() + ".git" }

func (r githubRepository) keyDir() string {
	return filepath.Join(dockerGitKeysDir, strings.ToLower(r.Owner), strings.ToLower(r.Name))
}

func validateBranch(branch string) error {
	if branch == "" {
		return nil // Use the repository's default branch.
	}
	if !gitBranchRE.MatchString(branch) || strings.Contains(branch, "..") ||
		strings.Contains(branch, "//") || strings.HasSuffix(branch, "/") ||
		strings.HasSuffix(branch, ".") || strings.HasSuffix(branch, ".lock") {
		return errors.New("invalid Git branch")
	}
	for _, part := range strings.Split(branch, "/") {
		if part == "." || part == ".." || strings.HasPrefix(part, ".") {
			return errors.New("invalid Git branch")
		}
	}
	return nil
}

func validateDockerfile(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "Dockerfile"
	}
	clean := filepath.Clean(value)
	if filepath.IsAbs(clean) || clean == "." || clean == ".." ||
		strings.HasPrefix(clean, ".."+string(os.PathSeparator)) ||
		strings.ContainsAny(value, "\r\n") {
		return "", errors.New("Dockerfile must be a path inside the repository")
	}
	return clean, nil
}

func (m *Manager) GitHubKey(repository string) (GitHubKeyInfo, error) {
	repo, err := parseGitHubRepository(repository)
	if err != nil {
		return GitHubKeyInfo{}, err
	}
	info := GitHubKeyInfo{Repository: repo.ID()}
	keyPath := filepath.Join(repo.keyDir(), "id_ed25519")
	if _, err := os.Stat(keyPath); errors.Is(err, os.ErrNotExist) {
		return info, nil
	} else if err != nil {
		return info, err
	}
	public, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		return info, fmt.Errorf("read public deploy key: %w", err)
	}
	info.PublicKey = strings.TrimSpace(string(public))
	info.Generated = info.PublicKey != ""
	return info, nil
}

// EnsureGitHubKey creates a unique read-only GitHub deploy-key identity for
// this one repository. The private key never enters the image build context.
func (m *Manager) EnsureGitHubKey(ctx context.Context, repository string) (GitHubKeyInfo, error) {
	repo, err := parseGitHubRepository(repository)
	if err != nil {
		return GitHubKeyInfo{}, err
	}
	m.keyMu.Lock()
	defer m.keyMu.Unlock()

	dir := repo.keyDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return GitHubKeyInfo{}, fmt.Errorf("create key directory: %w", err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return GitHubKeyInfo{}, err
	}
	keyPath := filepath.Join(dir, "id_ed25519")
	if _, err := os.Stat(keyPath); errors.Is(err, os.ErrNotExist) {
		output, err := exec.CommandContext(ctx, "ssh-keygen",
			"-q", "-t", "ed25519", "-N", "", "-f", keyPath,
			"-C", "open-go-panel-docker-"+repo.ID()).CombinedOutput()
		if err != nil {
			return GitHubKeyInfo{}, fmt.Errorf("generate deploy key: %w: %s", err, strings.TrimSpace(string(output)))
		}
	} else if err != nil {
		return GitHubKeyInfo{}, err
	}
	if err := os.Chmod(keyPath, 0600); err != nil {
		return GitHubKeyInfo{}, err
	}
	if err := ensureGitHubKnownHosts(ctx, filepath.Join(dir, "known_hosts")); err != nil {
		return GitHubKeyInfo{}, err
	}
	return m.GitHubKey(repo.ID())
}

// Retrieve verified GitHub SSH host keys over TLS, rather than blindly
// trusting unverified ssh-keyscan output from the network.
func ensureGitHubKnownHosts(ctx context.Context, path string) error {
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		return nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/meta", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("obtain GitHub SSH host keys: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub SSH key metadata returned HTTP %d", resp.StatusCode)
	}
	var metadata struct {
		SSHKeys []string `json:"ssh_keys"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 128<<10)).Decode(&metadata); err != nil {
		return fmt.Errorf("read GitHub SSH key metadata: %w", err)
	}
	var lines []string
	for _, key := range metadata.SSHKeys {
		parts := strings.Fields(key)
		if len(parts) < 2 || !strings.HasPrefix(parts[0], "ssh-") && !strings.HasPrefix(parts[0], "ecdsa-") {
			continue
		}
		if strings.ContainsAny(parts[1], "\r\n") {
			return errors.New("invalid GitHub SSH key metadata")
		}
		lines = append(lines, "github.com "+parts[0]+" "+parts[1])
	}
	if len(lines) == 0 {
		return errors.New("GitHub SSH host key metadata contained no usable keys")
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0600)
}

func (m *Manager) BuildStatus() BuildTask {
	m.buildMu.Lock()
	defer m.buildMu.Unlock()
	return m.buildTask
}

type buildRequest struct {
	Name       string
	Repository githubRepository
	Branch     string
	Dockerfile string
	Ports      string
	Autostart  bool
	Public     bool
}

func (m *Manager) StartGitHubBuild(name, repository, branch, dockerfile, ports string, autostart, public bool) error {
	repo, err := parseGitHubRepository(repository)
	if err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if !containerNamePattern.MatchString(name) {
		return errors.New("invalid Docker container name")
	}
	branch = strings.TrimSpace(branch)
	if err := validateBranch(branch); err != nil {
		return err
	}
	dockerfile, err = validateDockerfile(dockerfile)
	if err != nil {
		return err
	}
	if _, err := publishedPortArgs(ports, public); err != nil {
		return err
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return errors.New("Docker is not installed")
	}
	if _, err := exec.LookPath("git"); err != nil {
		return errors.New("Git is not installed")
	}
	req := buildRequest{Name: name, Repository: repo, Branch: branch,
		Dockerfile: dockerfile, Ports: ports, Autostart: autostart, Public: public}
	m.buildMu.Lock()
	if m.buildTask.Running {
		m.buildMu.Unlock()
		return errors.New("a Docker GitHub build is already running")
	}
	m.buildTask = BuildTask{Running: true, Repository: repo.ID(), Container: name,
		Step: "Preparing source checkout", StartedAt: time.Now()}
	m.buildMu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
		defer cancel()
		image, err := m.buildGitHubImage(ctx, req)
		m.buildMu.Lock()
		m.buildTask.Running = false
		m.buildTask.FinishedAt = time.Now()
		m.buildTask.Image = image
		if err != nil {
			m.buildTask.Error = err.Error()
			m.buildTask.Step = "Failed"
		} else {
			m.buildTask.Step = "Container created"
		}
		m.buildMu.Unlock()
	}()
	return nil
}

func (m *Manager) buildStep(step string) {
	m.buildMu.Lock()
	m.buildTask.Step = step
	m.buildMu.Unlock()
}

func (m *Manager) buildGitHubImage(ctx context.Context, req buildRequest) (string, error) {
	dir, err := os.MkdirTemp("", "ogp-docker-github-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	source := filepath.Join(dir, "src")
	keyInfo, err := m.GitHubKey(req.Repository.ID())
	if err != nil {
		return "", err
	}
	remote := req.Repository.HTTPS()
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if keyInfo.Generated {
		keyDir := req.Repository.keyDir()
		if _, err := os.Stat(filepath.Join(keyDir, "known_hosts")); err != nil {
			return "", errors.New("GitHub host keys are missing; generate the deploy key again")
		}
		remote = req.Repository.SSH()
		sshCommand := "ssh -F /dev/null -i " + strconv.Quote(filepath.Join(keyDir, "id_ed25519")) +
			" -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile=" +
			strconv.Quote(filepath.Join(keyDir, "known_hosts"))
		env = append(env, "GIT_SSH_COMMAND="+sshCommand)
	}
	args := []string{"clone", "--depth=1"}
	if req.Branch != "" {
		args = append(args, "--branch", req.Branch)
	}
	args = append(args, "--", remote, source)
	m.buildStep("Cloning GitHub repository")
	if err := runBuildCommand(ctx, dir, env, "git", args...); err != nil {
		return "", fmt.Errorf("clone %s: %w", req.Repository.ID(), err)
	}
	file := filepath.Join(source, req.Dockerfile)
	resolved, err := filepath.EvalSymlinks(file)
	if err != nil {
		return "", fmt.Errorf("Dockerfile %q not found: %w", req.Dockerfile, err)
	}
	rel, err := filepath.Rel(source, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", errors.New("Dockerfile resolves outside the cloned repository")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("Dockerfile must be a regular file")
	}
	image := fmt.Sprintf("ogp/%s:build-%d", strings.ToLower(req.Name), time.Now().UnixNano())
	m.buildStep("Building Docker image")
	if err := runBuildCommand(ctx, source, os.Environ(), "docker",
		"build", "-f", req.Dockerfile, "-t", image, "."); err != nil {
		return "", fmt.Errorf("Docker build failed: %w", err)
	}
	m.buildStep("Creating container")
	if err := m.runImage(ctx, req.Name, image, req.Ports, req.Autostart, req.Public, false); err != nil {
		return image, fmt.Errorf("image %s built but container launch failed: %w", image, err)
	}
	return image, nil
}

type cappedBuildOutput struct {
	buf []byte
}

func (o *cappedBuildOutput) Write(p []byte) (int, error) {
	const limit = 8192
	n := len(p)
	o.buf = append(o.buf, p...)
	if len(o.buf) > limit {
		o.buf = append([]byte(nil), o.buf[len(o.buf)-limit:]...)
	}
	return n, nil
}

func runBuildCommand(ctx context.Context, dir string, env []string, program string, args ...string) error {
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Dir = dir
	cmd.Env = env
	var output cappedBuildOutput
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w: %s", program, err, strings.TrimSpace(string(output.buf)))
	}
	return nil
}
