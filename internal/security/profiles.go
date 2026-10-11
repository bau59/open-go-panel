package security

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const maxProfilesBytes = 64 * 1024

// ProfilesFile is the existing CrowdSec YAML document; no parallel panel copy.
type ProfilesFile struct {
	Kind    string
	Path    string
	Content string
	Version string
	Exists  bool
}

func (m *Manager) profilesPath(kind string) (string, error) {
	dir := m.profilesDir
	if dir == "" {
		dir = "/etc/crowdsec"
	}
	switch kind {
	case "main":
		return filepath.Join(dir, "profiles.yaml"), nil
	case "local":
		return filepath.Join(dir, "profiles.yaml.local"), nil
	default:
		return "", errors.New("invalid CrowdSec profiles file")
	}
}

func (m *Manager) ReadProfilesFile(kind string) (ProfilesFile, error) {
	path, err := m.profilesPath(kind)
	if err != nil {
		return ProfilesFile{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return readProfilesFile(kind, path)
}

func readProfilesFile(kind, path string) (ProfilesFile, error) {
	out := ProfilesFile{Kind: kind, Path: path, Version: "missing"}
	stat, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) && kind == "local" {
		return out, nil
	}
	if err != nil {
		return ProfilesFile{}, err
	}
	if !stat.Mode().IsRegular() {
		return ProfilesFile{}, errors.New("CrowdSec profiles must be a regular file, not a symlink")
	}
	if stat.Size() > maxProfilesBytes {
		return ProfilesFile{}, fmt.Errorf("CrowdSec profiles exceed %d bytes", maxProfilesBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ProfilesFile{}, err
	}
	sum := sha256.Sum256(data)
	out.Exists = true
	out.Content = string(data)
	out.Version = hex.EncodeToString(sum[:])
	return out, nil
}

// SaveProfilesFile writes the native CrowdSec YAML, validates the complete
// configuration and restarts the engine. Any failure restores the original
// bytes (or removes a newly created .local document). A content hash prevents
// overwriting edits made in another browser or outside the panel.
func (m *Manager) SaveProfilesFile(ctx context.Context, kind, expectedVersion, content string) error {
	path, err := m.profilesPath(kind)
	if err != nil {
		return err
	}
	if strings.TrimSpace(content) == "" {
		return errors.New("profile YAML cannot be empty")
	}
	if len(content) > maxProfilesBytes {
		return fmt.Errorf("profile YAML must not exceed %d bytes", maxProfilesBytes)
	}
	if _, err := exec.LookPath("crowdsec"); err != nil {
		return errors.New("CrowdSec is not installed")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	previous, err := readProfilesFile(kind, path)
	if err != nil {
		return err
	}
	if previous.Version != expectedVersion {
		return errors.New("profiles changed since you opened the editor; reload before saving")
	}
	if previous.Content == content && previous.Exists {
		return nil
	}
	if !serviceActive(ctx, "crowdsec.service") {
		return errors.New("CrowdSec engine must be running to apply profile changes")
	}

	mode := os.FileMode(0644)
	if previous.Exists {
		stat, err := os.Stat(path)
		if err != nil {
			return err
		}
		mode = stat.Mode().Perm()
	}
	if err := replaceProfilesFile(path, []byte(content), mode); err != nil {
		return fmt.Errorf("write profiles: %w", err)
	}

	restore := func() error {
		if !previous.Exists {
			err := os.Remove(path)
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		return replaceProfilesFile(path, []byte(previous.Content), mode)
	}
	if output, err := exec.CommandContext(ctx, "crowdsec", "-t").CombinedOutput(); err != nil {
		undoErr := restore()
		return fmt.Errorf("CrowdSec rejected profiles (%s); rollback: %v", strings.TrimSpace(string(output)), undoErr)
	}
	if output, err := exec.CommandContext(ctx, "systemctl", "restart", "crowdsec.service").CombinedOutput(); err != nil {
		undoErr := restore()
		// Restore the running service even if the HTTP request is cancelled.
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		restartOutput, restartErr := exec.CommandContext(rollbackCtx, "systemctl", "restart", "crowdsec.service").CombinedOutput()
		return fmt.Errorf("CrowdSec restart failed (%s); file restore: %v; service restore: %v %s",
			strings.TrimSpace(string(output)), undoErr, restartErr, strings.TrimSpace(string(restartOutput)))
	}
	return nil
}

func replaceProfilesFile(path string, contents []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".open-go-panel-profiles-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(contents); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
