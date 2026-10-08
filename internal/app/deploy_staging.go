package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// stageApplication copies the current filesystem into a sibling directory,
// preserving the running tree until its replacement is fully prepared.
// Both paths must be on the same filesystem for an atomic directory exchange.
func stageApplication(ctx context.Context, app App) (string, error) {
	info, err := os.Lstat(app.Root)
	if err != nil {
		return "", fmt.Errorf("inspect active application: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("atomic deploy requires a real application directory (not a symlink)")
	}
	parent := filepath.Dir(app.Root)
	stage, err := os.MkdirTemp(parent, ".ogp-"+app.Name+"-stage-")
	if err != nil {
		return "", fmt.Errorf("create release directory: %w", err)
	}
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(stage)
		}
	}()

	cmd := exec.CommandContext(ctx, "cp", "-a", "--", app.Root+string(os.PathSeparator)+".", stage)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("stage current application: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		if err := os.Chown(stage, int(stat.Uid), int(stat.Gid)); err != nil {
			return "", fmt.Errorf("set release owner: %w", err)
		}
	}
	if err := os.Chmod(stage, info.Mode().Perm()); err != nil {
		return "", fmt.Errorf("set release permissions: %w", err)
	}
	success = true
	return stage, nil
}

// exchangeApplicationVersion swaps two complete directories atomically on
// Linux. Never fall back to two os.Rename calls: that introduces a missing-root
// window and can leave the application offline if the panel process exits.
func exchangeApplicationVersion(root, stage string) error {
	if filepath.Dir(root) != filepath.Dir(stage) {
		return errors.New("release staging directory must be a sibling of the active directory")
	}
	if err := unix.Renameat2(unix.AT_FDCWD, root, unix.AT_FDCWD, stage, unix.RENAME_EXCHANGE); err != nil {
		return fmt.Errorf("atomic directory exchange not supported or failed: %w", err)
	}
	return nil
}

// activateStagedRelease switches the prepared release, then rolls back to the
// original tree if activation or persistence fails. After the function returns,
// stage holds the original tree on success and the rejected tree on failure.
func activateStagedRelease(root, stage string, activate func() error, recoverOriginal func() error) error {
	if err := exchangeApplicationVersion(root, stage); err != nil {
		return err
	}
	if err := activate(); err != nil {
		if rollbackErr := exchangeApplicationVersion(root, stage); rollbackErr != nil {
			return errors.Join(err, fmt.Errorf("critical: failed to return original release to %s: %w", root, rollbackErr))
		}
		if recoverOriginal != nil {
			if restartErr := recoverOriginal(); restartErr != nil {
				return errors.Join(err, fmt.Errorf("original application restored on disk but failed to restart: %w", restartErr))
			}
		}
		return fmt.Errorf("release activation failed; previous files restored: %w", err)
	}
	return nil
}

func (m *Manager) prepareStagedGit(ctx context.Context, app App, stage string, cfg DeployConfig, gitEnv []string, rollback bool) (string, error) {
	if _, err := os.Stat(filepath.Join(stage, ".git")); errors.Is(err, os.ErrNotExist) {
		if rollback {
			return "", errors.New("cannot roll back an app without a Git repository")
		}
		if _, err := runAsUser(ctx, app.User, stage, "git", "init"); err != nil {
			return "", err
		}
		if _, err := runAsUserEnv(ctx, app.User, stage, gitEnv, "git", "remote", "add", "origin", cfg.Repository); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", fmt.Errorf("inspect staged Git repository: %w", err)
	} else if !rollback {
		if _, err := runAsUserEnv(ctx, app.User, stage, gitEnv, "git", "remote", "set-url", "origin", cfg.Repository); err != nil {
			return "", err
		}
	}

	var target string
	if rollback {
		target = cfg.PreviousCommit
	} else {
		if _, err := runAsUserEnv(ctx, app.User, stage, gitEnv, "git", "fetch", "--prune", "origin", cfg.Branch); err != nil {
			return "", err
		}
		commit, err := runAsUser(ctx, app.User, stage, "git", "rev-parse", "FETCH_HEAD")
		if err != nil {
			return "", err
		}
		target = strings.TrimSpace(commit)
	}
	if target == "" {
		return "", errors.New("target Git commit is empty")
	}
	if _, err := runAsUser(ctx, app.User, stage, "git", "reset", "--hard", target); err != nil {
		return "", err
	}
	stagedApp := app
	stagedApp.Root = stage
	if err := m.prepareDeployment(ctx, stagedApp); err != nil {
		return "", fmt.Errorf("prepare staged release %s: %w", target, err)
	}
	return target, nil
}

func (m *Manager) deployStaged(ctx context.Context, id int64, rollback bool) error {
	app, err := m.Get(id)
	if err != nil {
		return err
	}
	cfg, err := m.DeployConfig(id)
	if err != nil {
		return err
	}
	if cfg.Repository == "" {
		return errors.New("configure a Git repository before deploying")
	}
	if rollback && cfg.PreviousCommit == "" {
		return errors.New("no previous deployment is available")
	}
	if _, err := exec.LookPath("git"); err != nil {
		return errors.New("git is not installed")
	}

	gitEnv, err := m.gitEnvironment(id, cfg.Repository)
	if err != nil {
		return err
	}
	previous := ""
	if out, err := runAsUser(ctx, app.User, app.Root, "git", "rev-parse", "HEAD"); err == nil {
		previous = strings.TrimSpace(out)
	}
	if rollback && previous == "" {
		return errors.New("current Git revision is not available for a safe rollback")
	}

	stage, err := stageApplication(ctx, app)
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)

	target, err := m.prepareStagedGit(ctx, app, stage, cfg, gitEnv, rollback)
	if err != nil {
		return err // active tree remains untouched
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	activate := func() error {
		if app.Type != "static" {
			if err := m.Restart(ctx, id); err != nil {
				return fmt.Errorf("restart new application: %w", err)
			}
			if err := m.waitForRelease(ctx, app); err != nil {
				return err
			}
		}
		if rollback {
			return m.recordRollback(id, target, previous)
		}
		_, err := m.store.DB().Exec(`
			UPDATE deployments
			SET current_commit = ?, previous_commit = ?, deployed_at = ?
			WHERE app_id = ?
		`, target, previous, time.Now().UTC().Format(time.RFC3339Nano), id)
		return err
	}
	recoverPrevious := func() error {
		if app.Type == "static" {
			return nil
		}
		recoveryCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		return m.Restart(recoveryCtx, id)
	}
	return activateStagedRelease(app.Root, stage, activate, recoverPrevious)
}

func (m *Manager) waitForRelease(ctx context.Context, app App) error {
	// A running systemd unit alone can be a false positive for an app that
	// fails just after startup. Require that its listener is reachable too.
	for i := 0; i < 30; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if m.Status(ctx, app.ID) == "active" {
			if app.Port <= 0 {
				return nil
			}
			dialer := net.Dialer{Timeout: 400 * time.Millisecond}
			conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(app.Port)))
			if err == nil {
				_ = conn.Close()
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return errors.New("new application did not become active or start listening before the readiness deadline")
}
