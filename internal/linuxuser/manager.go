package linuxuser

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const managedGroup = "ogp-users"

var usernamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

type User struct {
	Username string
	Home     string
	Shell    string
	Locked   bool
	SSHKeys  int
}

type Manager struct {
	logger *slog.Logger
}

func New(logger *slog.Logger) *Manager {
	return &Manager{logger: logger}
}

func (m *Manager) EnsureGroup(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "groupadd", "--force", managedGroup)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ensure group %s: %w: %s", managedGroup, err, strings.TrimSpace(string(out)))
	}

	return nil
}

func (m *Manager) List(ctx context.Context) ([]User, error) {
	members, err := managedGroupMembers()
	if err != nil {
		return nil, err
	}

	users := make([]User, 0, len(members))
	for _, username := range members {
		account, err := user.Lookup(username)
		if err != nil {
			continue
		}

		locked, err := accountLocked(ctx, username)
		if err != nil {
			return nil, err
		}

		keys, err := countSSHKeys(account.HomeDir)
		if err != nil {
			return nil, err
		}

		users = append(users, User{
			Username: username,
			Home:     account.HomeDir,
			Shell:    accountShell(username),
			Locked:   locked,
			SSHKeys:  keys,
		})
	}

	sort.Slice(users, func(i, j int) bool {
		return users[i].Username < users[j].Username
	})

	return users, nil
}

func (m *Manager) Create(ctx context.Context, username, password string) error {
	if err := validateUsername(username); err != nil {
		return err
	}

	if len(password) < 8 {
		return errors.New("password must contain at least 8 characters")
	}

	if err := m.EnsureGroup(ctx); err != nil {
		return err
	}

	if _, err := user.Lookup(username); err == nil {
		return fmt.Errorf("user %q already exists", username)
	} else if !errors.Is(err, user.UnknownUserError(username)) {
		var unknown user.UnknownUserError
		if !errors.As(err, &unknown) {
			return fmt.Errorf("lookup user %q: %w", username, err)
		}
	}

	cmd := exec.CommandContext(
		ctx,
		"useradd",
		"--create-home",
		"--shell", "/bin/bash",
		"--groups", managedGroup,
		username,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("create user %q: %w: %s", username, err, strings.TrimSpace(string(out)))
	}

	if err := setPassword(ctx, username, password); err != nil {
		_ = exec.CommandContext(context.Background(), "userdel", "--remove", username).Run()
		return err
	}

	m.logger.Info("linux user created", "username", username)
	return nil
}

func (m *Manager) Delete(ctx context.Context, username string) error {
	if err := validateManagedUsername(username); err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, "userdel", "--remove", username)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("delete user %q: %w: %s", username, err, strings.TrimSpace(string(out)))
	}

	m.logger.Info("linux user deleted", "username", username)
	return nil
}

func (m *Manager) Lock(ctx context.Context, username string) error {
	if err := validateManagedUsername(username); err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, "usermod", "--lock", username)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("lock user %q: %w: %s", username, err, strings.TrimSpace(string(out)))
	}

	m.logger.Info("linux user locked", "username", username)
	return nil
}

func (m *Manager) Unlock(ctx context.Context, username string) error {
	if err := validateManagedUsername(username); err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, "usermod", "--unlock", username)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("unlock user %q: %w: %s", username, err, strings.TrimSpace(string(out)))
	}

	m.logger.Info("linux user unlocked", "username", username)
	return nil
}

func (m *Manager) SetPassword(ctx context.Context, username, password string) error {
	if err := validateManagedUsername(username); err != nil {
		return err
	}

	if len(password) < 8 {
		return errors.New("password must contain at least 8 characters")
	}

	if err := setPassword(ctx, username, password); err != nil {
		return err
	}

	m.logger.Info("linux user password changed", "username", username)
	return nil
}

func (m *Manager) AddSSHKey(ctx context.Context, username, publicKey string) error {
	if err := validateManagedUsername(username); err != nil {
		return err
	}

	publicKey = strings.TrimSpace(publicKey)
	if err := validatePublicKey(publicKey); err != nil {
		return err
	}

	account, err := user.Lookup(username)
	if err != nil {
		return fmt.Errorf("lookup user %q: %w", username, err)
	}

	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return fmt.Errorf("parse uid for %q: %w", username, err)
	}

	gid, err := strconv.Atoi(account.Gid)
	if err != nil {
		return fmt.Errorf("parse gid for %q: %w", username, err)
	}

	sshDir := filepath.Join(account.HomeDir, ".ssh")
	if err := os.MkdirAll(sshDir, 0700); err != nil {
		return fmt.Errorf("create .ssh directory: %w", err)
	}

	if err := os.Chmod(sshDir, 0700); err != nil {
		return fmt.Errorf("chmod .ssh directory: %w", err)
	}

	if err := os.Chown(sshDir, uid, gid); err != nil {
		return fmt.Errorf("chown .ssh directory: %w", err)
	}

	authorizedKeys := filepath.Join(sshDir, "authorized_keys")

	existing, err := os.ReadFile(authorizedKeys)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read authorized_keys: %w", err)
	}

	for _, line := range strings.Split(string(existing), "\n") {
		if strings.TrimSpace(line) == publicKey {
			return errors.New("SSH key already exists")
		}
	}

	file, err := os.OpenFile(authorizedKeys, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("open authorized_keys: %w", err)
	}
	defer file.Close()

	if _, err := fmt.Fprintln(file, publicKey); err != nil {
		return fmt.Errorf("write authorized_keys: %w", err)
	}

	if err := file.Chmod(0600); err != nil {
		return fmt.Errorf("chmod authorized_keys: %w", err)
	}

	if err := file.Chown(uid, gid); err != nil {
		return fmt.Errorf("chown authorized_keys: %w", err)
	}

	m.logger.Info("ssh key added", "username", username)
	return nil
}

func validateUsername(username string) error {
	if !usernamePattern.MatchString(username) {
		return errors.New("username must start with a lowercase letter or underscore and contain only lowercase letters, digits, underscore or hyphen")
	}

	if username == "root" {
		return errors.New("root user cannot be managed")
	}

	return nil
}

func validateManagedUsername(username string) error {
	if err := validateUsername(username); err != nil {
		return err
	}

	ok, err := isManagedUser(username)
	if err != nil {
		return err
	}

	if !ok {
		return fmt.Errorf("user %q is not managed by Open Go Panel", username)
	}

	return nil
}

func managedGroupMembers() ([]string, error) {
	file, err := os.Open("/etc/group")
	if err != nil {
		return nil, fmt.Errorf("open /etc/group: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, ":", 4)
		if len(parts) != 4 || parts[0] != managedGroup {
			continue
		}

		if parts[3] == "" {
			return nil, nil
		}

		return strings.Split(parts[3], ","), nil
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read /etc/group: %w", err)
	}

	return nil, nil
}

func isManagedUser(username string) (bool, error) {
	members, err := managedGroupMembers()
	if err != nil {
		return false, err
	}

	for _, member := range members {
		if member == username {
			return true, nil
		}
	}

	return false, nil
}

func setPassword(ctx context.Context, username, password string) error {
	cmd := exec.CommandContext(ctx, "chpasswd")
	cmd.Stdin = strings.NewReader(username + ":" + password + "\n")

	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("set password for %q: %w: %s", username, err, strings.TrimSpace(string(out)))
	}

	return nil
}

func accountLocked(ctx context.Context, username string) (bool, error) {
	cmd := exec.CommandContext(ctx, "passwd", "--status", username)
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("read password status for %q: %w", username, err)
	}

	fields := strings.Fields(string(out))
	if len(fields) < 2 {
		return false, fmt.Errorf("unexpected password status for %q", username)
	}

	return fields[1] == "L", nil
}

func accountShell(username string) string {
	file, err := os.Open("/etc/passwd")
	if err != nil {
		return ""
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	prefix := username + ":"
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, prefix) {
			continue
		}

		parts := strings.Split(line, ":")
		if len(parts) >= 7 {
			return parts[6]
		}
	}

	return ""
}

func countSSHKeys(home string) (int, error) {
	data, err := os.ReadFile(filepath.Join(home, ".ssh", "authorized_keys"))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read authorized_keys: %w", err)
	}

	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			count++
		}
	}

	return count, nil
}

func validatePublicKey(publicKey string) error {
	if strings.ContainsAny(publicKey, "\r\n") {
		return errors.New("SSH key must be a single line")
	}

	fields := strings.Fields(publicKey)
	if len(fields) < 2 {
		return errors.New("invalid SSH public key")
	}

	switch fields[0] {
	case "ssh-ed25519", "ssh-rsa", "ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521", "sk-ssh-ed25519@openssh.com", "sk-ecdsa-sha2-nistp256@openssh.com":
	default:
		return errors.New("unsupported SSH public key type")
	}

	if _, err := base64.StdEncoding.DecodeString(fields[1]); err != nil {
		return errors.New("invalid SSH public key data")
	}

	return nil
}
