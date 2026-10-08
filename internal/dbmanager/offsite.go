package dbmanager

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path"
	"regexp"
	"strings"
)

const backupRemoteSetting = "db.backup_remote"

// Remote targets use the same syntax as rclone ("remote:bucket/prefix").
// Require a remote name and a nonempty subpath so a typo cannot copy into
// a provider root. Backend credentials live in rclone.conf, not SQLite.
var backupRemotePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*:[A-Za-z0-9][A-Za-z0-9_./-]*$`)

func validateBackupRemote(remote string) error {
	if remote == "" {
		return nil
	}
	if len(remote) > 512 || !backupRemotePattern.MatchString(remote) {
		return errors.New("remote destination must look like s3:bucket/open-go-panel (rclone remote:path)")
	}
	_, rest, _ := strings.Cut(remote, ":")
	for _, part := range strings.Split(rest, "/") {
		if part == ".." || part == "." || part == "" {
			return errors.New("remote backup destination must not contain empty or relative path segments")
		}
	}
	return nil
}

func (m *Manager) BackupRemote() string {
	remote, _, err := m.store.Setting(backupRemoteSetting)
	if err != nil {
		return ""
	}
	return remote
}

func (m *Manager) SetBackupRemote(remote string) error {
	remote = strings.TrimSpace(remote)
	if err := validateBackupRemote(remote); err != nil {
		return err
	}
	if remote != "" {
		if _, err := exec.LookPath("rclone"); err != nil {
			return errors.New("rclone is not installed; install it and configure an off-server storage remote first")
		}
	}
	return m.store.SetSetting(backupRemoteSetting, remote)
}

func backupRemotePath(remote string, backup Backup) string {
	return remote + "/" + path.Join(backup.Engine, backup.Database, path.Base(backup.Path))
}

// UploadBackup copies a completed local snapshot to off-host object storage.
// Failed transfers are returned as errors and leave the local snapshot intact.
func (m *Manager) UploadBackup(ctx context.Context, backup Backup) error {
	remote := m.BackupRemote()
	if remote == "" {
		return nil
	}
	if err := validateBackupRemote(remote); err != nil {
		return err
	}
	dst := backupRemotePath(remote, backup)
	cmd := exec.CommandContext(ctx, "rclone", "copyto", "--", backup.Path, dst)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("off-site backup upload failed (local copy preserved): %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
