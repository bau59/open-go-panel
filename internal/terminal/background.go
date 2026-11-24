package terminal

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/bau59/open-go-panel/internal/state"
)

const (
	backgroundUnitPrefix = "ogp-bg-"
	maxBackgroundCommand = 8192
	maxBackgroundLogs = 128 * 1024
)

var backgroundIDRE = regexp.MustCompile("^[a-f0-9]{24}$")

var ErrBackgroundNotFound = errors.New("background command not found")

// BackgroundJob describes a detached systemd service, not a child of the panel.
// Transient systemd units survive browser disconnects and panel service restarts;
// they deliberately do not restart after a machine reboot.
type BackgroundJob struct {
	ID        string
	Username  string
	AppID     int64
	WorkDir   string
	Command   string
	CreatedAt string
	StoppedAt string
	Status    string
}

type BackgroundManager struct {
	store *state.Store
}

func NewBackgroundManager(store *state.Store) *BackgroundManager {
	return &BackgroundManager{store: store}
}

func backgroundUnitName(id string) (string, error) {
	if !backgroundIDRE.MatchString(id) {
		return "", errors.New("invalid background command ID")
	}
	return backgroundUnitPrefix + id + ".service", nil
}

func backgroundRunArgs(job BackgroundJob) ([]string, error) {
	unit, err := backgroundUnitName(job.ID)
	if err != nil {
		return nil, err
	}
	if job.Username != "root" && !usernameRE.MatchString(job.Username) {
		return nil, errors.New("invalid Linux user")
	}
	if !filepath.IsAbs(job.WorkDir) || strings.ContainsAny(job.WorkDir, "\r\n\x00") {
		return nil, errors.New("invalid working directory")
	}
	if strings.TrimSpace(job.Command) == "" || len(job.Command) > maxBackgroundCommand || strings.ContainsRune(job.Command, 0) {
		return nil, errors.New("command must be between 1 and 8192 bytes")
	}
	return []string{
		"--unit=" + strings.TrimSuffix(unit, ".service"),
		"--description=Open Go Panel background command",
		"--collect",
		"--property=Type=exec",
		"--property=User=" + job.Username,
		"--property=WorkingDirectory=" + job.WorkDir,
		"--property=Restart=no",
		"--",
		"/bin/bash", "-lc", job.Command,
	}, nil
}

func (m *BackgroundManager) Start(ctx context.Context, username, workdir string, appID int64, command string) (BackgroundJob, error) {
	if m == nil || m.store == nil {
		return BackgroundJob{}, errors.New("background command storage unavailable")
	}
	var idBytes [12]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return BackgroundJob{}, fmt.Errorf("generate command ID: %w", err)
	}
	job := BackgroundJob{
		ID: hex.EncodeToString(idBytes[:]),
		Username: username,
		WorkDir: filepath.Clean(workdir),
		AppID: appID,
		Command: strings.TrimSpace(command),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Status: "running",
	}
	args, err := backgroundRunArgs(job)
	if err != nil {
		return BackgroundJob{}, err
	}
	if info, err := os.Stat(job.WorkDir); err != nil || !info.IsDir() {
		return BackgroundJob{}, errors.New("working directory does not exist")
	}
	if _, err := exec.LookPath("systemd-run"); err != nil {
		return BackgroundJob{}, errors.New("systemd-run is not installed")
	}
	if _, err := m.store.DB().ExecContext(ctx,
		"INSERT INTO background_commands(id,username,app_id,workdir,command,created_at) VALUES(?,?,?,?,?,?)",
		job.ID, job.Username, job.AppID, job.WorkDir, job.Command, job.CreatedAt); err != nil {
		return BackgroundJob{}, fmt.Errorf("save background command: %w", err)
	}

	startCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(startCtx, "systemd-run", args...).CombinedOutput()
	if err != nil {
		// A canceled client can race with systemd accepting the start. Stop that
		// unit before removing its metadata rather than orphaning a process.
		if unit, unitErr := backgroundUnitName(job.ID); unitErr == nil {
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = exec.CommandContext(stopCtx, "systemctl", "stop", unit).Run()
			stopCancel()
		}
		_, _ = m.store.DB().Exec("DELETE FROM background_commands WHERE id = ?", job.ID)
		return BackgroundJob{}, fmt.Errorf("start background command: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return job, nil
}

func (m *BackgroundManager) Get(ctx context.Context, id string) (BackgroundJob, error) {
	if _, err := backgroundUnitName(id); err != nil {
		return BackgroundJob{}, err
	}
	var job BackgroundJob
	err := m.store.DB().QueryRowContext(ctx,
		"SELECT id,username,app_id,workdir,command,created_at,stopped_at FROM background_commands WHERE id = ?", id).
		Scan(&job.ID, &job.Username, &job.AppID, &job.WorkDir, &job.Command, &job.CreatedAt, &job.StoppedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return BackgroundJob{}, ErrBackgroundNotFound
	}
	if err != nil {
		return BackgroundJob{}, fmt.Errorf("read background command: %w", err)
	}
	return job, nil
}

// List reads active systemd units once, rather than running systemctl per job.
func (m *BackgroundManager) List(ctx context.Context) ([]BackgroundJob, error) {
	rows, err := m.store.DB().QueryContext(ctx,
		"SELECT id,username,app_id,workdir,command,created_at,stopped_at FROM background_commands ORDER BY created_at DESC, id DESC LIMIT 200")
	if err != nil {
		return nil, fmt.Errorf("list background commands: %w", err)
	}
	var jobs []BackgroundJob
	for rows.Next() {
		var job BackgroundJob
		if err := rows.Scan(&job.ID, &job.Username, &job.AppID, &job.WorkDir, &job.Command, &job.CreatedAt, &job.StoppedAt); err != nil {
			rows.Close()
			return nil, err
		}
		job.Status = "unknown"
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	unitCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	out, err := exec.CommandContext(unitCtx, "systemctl", "list-units", "--all", "--no-legend", "--no-pager", "--plain", backgroundUnitPrefix+"*.service").Output()
	if err != nil {
		return jobs, fmt.Errorf("read systemd background status: %w", err)
	}
	units := parseBackgroundUnits(string(out))
	for i := range jobs {
		unit, _ := backgroundUnitName(jobs[i].ID)
		status := units[unit]
		if status == "" {
			status = "finished" // systemd garbage-collects inactive transient units
		}
		if jobs[i].StoppedAt != "" && status != "running" && status != "starting" {
			status = "stopped"
		}
		jobs[i].Status = status
	}
	return jobs, nil
}

func parseBackgroundUnits(output string) map[string]string {
	units := make(map[string]string)
	for _, line := range strings.Split(output, "\n") {
		parts := strings.Fields(strings.TrimSpace(strings.TrimPrefix(line, "●")))
		if len(parts) < 4 || !strings.HasPrefix(parts[0], backgroundUnitPrefix) || !strings.HasSuffix(parts[0], ".service") {
			continue
		}
		switch parts[2] {
		case "active":
			units[parts[0]] = "running"
		case "activating":
			units[parts[0]] = "starting"
		case "failed":
			units[parts[0]] = "failed"
		default:
			units[parts[0]] = "finished"
		}
	}
	return units
}

func (m *BackgroundManager) Stop(ctx context.Context, id string) error {
	job, err := m.Get(ctx, id)
	if err != nil {
		return err
	}
	unit, _ := backgroundUnitName(job.ID)
	stopCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(stopCtx, "systemctl", "stop", unit).CombinedOutput()
	if err != nil {
		return fmt.Errorf("stop background command: %w: %s", err, strings.TrimSpace(string(out)))
	}
	_, err = m.store.DB().ExecContext(ctx, "UPDATE background_commands SET stopped_at = ? WHERE id = ?",
		time.Now().UTC().Format(time.RFC3339), id)
	return err
}

type limitedOutput struct {
	data []byte
	cap  int
	cut  bool
}

func (w *limitedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if free := w.cap - len(w.data); free > 0 {
		take := n
		if take > free {
			take = free
		}
		w.data = append(w.data, p[:take]...)
	}
	if len(w.data) >= w.cap {
		w.cut = true
	}
	return n, nil
}

func (m *BackgroundManager) Logs(ctx context.Context, id string) (BackgroundJob, string, error) {
	job, err := m.Get(ctx, id)
	if err != nil {
		return BackgroundJob{}, "", err
	}
	unit, _ := backgroundUnitName(id)
	logCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(logCtx, "journalctl", "--no-pager", "--output=cat", "--lines=120", "--unit="+unit)
	w := &limitedOutput{cap: maxBackgroundLogs}
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Run(); err != nil {
		return job, "", fmt.Errorf("read background command log: %w: %s", err, string(w.data))
	}
	value := string(w.data)
	if w.cut {
		value += "\n[output truncated at 128 KiB]"
	}
	return job, value, nil
}

func (m *BackgroundManager) Forget(ctx context.Context, id string) error {
	if _, err := m.Get(ctx, id); err != nil {
		return err
	}
	unit, _ := backgroundUnitName(id)
	checkCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(checkCtx, "systemctl", "is-active", unit)
	out, err := cmd.Output()
	if err == nil {
		return errors.New("stop this command before removing its history")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return fmt.Errorf("check background status: %w", err)
	}
	switch strings.TrimSpace(string(out)) {
	case "inactive", "failed", "unknown":
		// inactive transient units may already have been garbage-collected
	default:
		if !strings.Contains(string(exitErr.Stderr), "could not be found") {
			return errors.New("cannot confirm that this command has stopped")
		}
	}
	_, err = m.store.DB().ExecContext(ctx, "DELETE FROM background_commands WHERE id = ?", id)
	return err
}

// Drain one journalctl invocation without storing unbounded command output.
var _ io.Writer = (*limitedOutput)(nil)
