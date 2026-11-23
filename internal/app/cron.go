package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bau59/open-go-panel/internal/phpfpm"
)

// CronJob belongs to one managed application. The systemd timer, not the panel
// process, executes the schedule and survives panel restarts and host reboots.
type CronJob struct {
	ID        int64
	AppID     int64
	Name      string
	Schedule  string
	Command   string
	Enabled   bool
	CreatedAt string
}

func cronServiceName(id int64) string {
	return fmt.Sprintf("open-go-panel-cron-%d.service", id)
}

func cronTimerName(id int64) string {
	return fmt.Sprintf("open-go-panel-cron-%d.timer", id)
}

func cronRunnerPath(dir string, id int64) string {
	return filepath.Join(dir, fmt.Sprintf("cron-%d.sh", id))
}

func (m *Manager) CronJobs(ctx context.Context, appID int64) ([]CronJob, error) {
	if _, err := m.Get(appID); err != nil {
		return nil, err
	}
	return m.cronJobsForApp(ctx, appID)
}

func (m *Manager) cronJobsForApp(ctx context.Context, appID int64) ([]CronJob, error) {
	rows, err := m.store.DB().QueryContext(ctx, "SELECT id, app_id, name, schedule, command, enabled, created_at FROM app_cron_jobs WHERE app_id=? ORDER BY id", appID)
	if err != nil {
		return nil, fmt.Errorf("list cron jobs: %w", err)
	}
	defer rows.Close()
	var jobs []CronJob
	for rows.Next() {
		var job CronJob
		if err := rows.Scan(&job.ID, &job.AppID, &job.Name, &job.Schedule, &job.Command, &job.Enabled, &job.CreatedAt); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (m *Manager) cronJob(ctx context.Context, appID, jobID int64) (CronJob, error) {
	var job CronJob
	err := m.store.DB().QueryRowContext(ctx, "SELECT id, app_id, name, schedule, command, enabled, created_at FROM app_cron_jobs WHERE id=? AND app_id=?", jobID, appID).
		Scan(&job.ID, &job.AppID, &job.Name, &job.Schedule, &job.Command, &job.Enabled, &job.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return CronJob{}, errors.New("scheduled task not found in this application")
	}
	if err != nil {
		return CronJob{}, err
	}
	return job, nil
}

func validateCronJob(job CronJob) ([]string, error) {
	job.Name = strings.TrimSpace(job.Name)
	if job.Name == "" || len(job.Name) > 100 || strings.ContainsAny(job.Name, "\r\n\x00") {
		return nil, errors.New("task name must be 1-100 characters without newlines")
	}
	if strings.TrimSpace(job.Command) == "" || len(job.Command) > 4096 || strings.ContainsRune(job.Command, '\x00') {
		return nil, errors.New("task command must be 1-4096 characters")
	}
	if len(job.Schedule) > 150 || strings.ContainsAny(job.Schedule, "\r\n\x00") {
		return nil, errors.New("invalid schedule")
	}
	return cronCalendar(job.Schedule)
}

func (m *Manager) SaveCronJob(ctx context.Context, appID, jobID int64, name, schedule, command string, enabled bool) (CronJob, error) {
	app, err := m.Get(appID)
	if err != nil {
		return CronJob{}, err
	}
	job := CronJob{ID: jobID, AppID: appID, Name: strings.TrimSpace(name), Schedule: strings.TrimSpace(schedule), Command: strings.TrimSpace(command), Enabled: enabled}
	calendars, err := validateCronJob(job)
	if err != nil {
		return CronJob{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var old CronJob
	if jobID > 0 {
		old, err = m.cronJob(ctx, appID, jobID)
		if err != nil {
			return CronJob{}, err
		}
		job.CreatedAt = old.CreatedAt
	} else if jobID < 0 {
		return CronJob{}, errors.New("invalid scheduled task ID")
	} else {
		job.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if jobID == 0 {
		result, err := m.store.DB().ExecContext(ctx,
			"INSERT INTO app_cron_jobs(app_id,name,schedule,command,enabled,created_at) VALUES(?,?,?,?,?,?)",
			appID, job.Name, job.Schedule, job.Command, job.Enabled, job.CreatedAt)
		if err != nil {
			return CronJob{}, fmt.Errorf("save scheduled task: %w", err)
		}
		job.ID, err = result.LastInsertId()
		if err != nil {
			return CronJob{}, err
		}
	} else if err := m.updateCronRow(ctx, job); err != nil {
		return CronJob{}, err
	}
	if err := m.applyCronJob(ctx, app, job, calendars); err != nil {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if jobID == 0 {
			_, dbErr := m.store.DB().ExecContext(rollbackCtx, "DELETE FROM app_cron_jobs WHERE id=? AND app_id=?", job.ID, appID)
			cleanupErr := m.removeCronFiles(rollbackCtx, job.ID)
			return CronJob{}, errors.Join(err, dbErr, cleanupErr)
		}
		dbErr := m.updateCronRow(rollbackCtx, old)
		oldCalendars, _ := cronCalendar(old.Schedule)
		restoreErr := m.applyCronJob(rollbackCtx, app, old, oldCalendars)
		return CronJob{}, errors.Join(err, dbErr, restoreErr)
	}
	return job, nil
}

func (m *Manager) updateCronRow(ctx context.Context, job CronJob) error {
	result, err := m.store.DB().ExecContext(ctx,
		"UPDATE app_cron_jobs SET name=?,schedule=?,command=?,enabled=? WHERE id=? AND app_id=?",
		job.Name, job.Schedule, job.Command, job.Enabled, job.ID, job.AppID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("scheduled task is missing")
	}
	return nil
}

// shellQuote is used only to generate a root-owned runner file. The command
// itself is administrator-provided and intentionally interpreted by a shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func cronRunner(app App, job CronJob) string {
	prefix := "#!/bin/sh\nset -eu\ncd " + shellQuote(app.Root) + "\n"
	if app.Type == "php" && app.Service.PHPVersion == phpfpm.LegacyVersion {
		container := phpfpm.LegacyContainerName(app.ID)
		format := "{{index .Config.Labels \"org.open-go-panel.managed-runtime\"}}:{{index .Config.Labels \"org.open-go-panel.php-app-id\"}}"
		return prefix +
			"identity=$(docker inspect --format " + shellQuote(format) + " " + shellQuote(container) + ")\n" +
			"[ \"$identity\" = " + shellQuote("php74:"+strconv.FormatInt(app.ID, 10)) + " ] || { echo 'Unmanaged PHP 7.4 container' >&2; exit 1; }\n" +
			"exec docker exec --workdir " + shellQuote(app.Root) + " " + shellQuote(container) +
			" /bin/sh -lc " + shellQuote(job.Command) + "\n"
	}
	// runuser preserves the current directory and inherited environment, while
	// executing the app's command with the app's unprivileged Linux identity.
	return prefix + "exec /usr/sbin/runuser -u " + shellQuote(app.User) + " -- /bin/bash -c " + shellQuote(job.Command) + "\n"
}

func atomicCronFile(path, content string, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".open-go-panel-cron-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (m *Manager) applyCronJob(ctx context.Context, app App, job CronJob, calendars []string) error {
	if err := os.MkdirAll(m.runnerDir, 0755); err != nil {
		return err
	}
	for _, calendar := range calendars {
		cmd := exec.CommandContext(ctx, "systemd-analyze", "calendar", calendar)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("invalid systemd schedule: %w: %s", err, strings.TrimSpace(string(output)))
		}
	}
	if err := atomicCronFile(cronRunnerPath(m.runnerDir, job.ID), cronRunner(app, job), 0700); err != nil {
		return fmt.Errorf("write task runner: %w", err)
	}
	service := fmt.Sprintf("[Unit]\nDescription=Open Go Panel scheduled task %d (%s/%s)\nAfter=network-online.target\n\n[Service]\nType=oneshot\nWorkingDirectory=%s\nEnvironment=PATH=%s\nEnvironmentFile=-%s\nExecStart=/bin/sh %s\nTimeoutStartSec=15min\nStandardOutput=journal\nStandardError=journal\n",
		job.ID, app.User, app.Name, app.Root, defaultPath(app), envPath(m.envDir, app.ID), cronRunnerPath(m.runnerDir, job.ID))
	if err := atomicCronFile(filepath.Join(m.serviceDir, cronServiceName(job.ID)), service, 0644); err != nil {
		return fmt.Errorf("write task service: %w", err)
	}
	var onCalendar strings.Builder
	for _, calendar := range calendars {
		onCalendar.WriteString("OnCalendar=" + calendar + "\n")
	}
	timer := fmt.Sprintf("[Unit]\nDescription=Open Go Panel task timer %d\n\n[Timer]\nUnit=%s\n%sPersistent=false\nAccuracySec=1s\n\n[Install]\nWantedBy=timers.target\n",
		job.ID, cronServiceName(job.ID), onCalendar.String())
	if err := atomicCronFile(filepath.Join(m.serviceDir, cronTimerName(job.ID)), timer, 0644); err != nil {
		return fmt.Errorf("write task timer: %w", err)
	}
	if err := systemctl(ctx, "daemon-reload"); err != nil {
		return err
	}
	if job.Enabled {
		if err := systemctl(ctx, "enable", cronTimerName(job.ID)); err != nil {
			return err
		}
		return systemctl(ctx, "restart", cronTimerName(job.ID))
	}
	return systemctl(ctx, "disable", "--now", cronTimerName(job.ID))
}

func (m *Manager) removeCronFiles(ctx context.Context, jobID int64) error {
	timerPath := filepath.Join(m.serviceDir, cronTimerName(jobID))
	if _, err := os.Stat(timerPath); err == nil {
		if err := systemctl(ctx, "disable", "--now", cronTimerName(jobID)); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, path := range []string{
		timerPath,
		filepath.Join(m.serviceDir, cronServiceName(jobID)),
		cronRunnerPath(m.runnerDir, jobID),
	} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return systemctl(ctx, "daemon-reload")
}

func (m *Manager) DeleteCronJob(ctx context.Context, appID, jobID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.cronJob(ctx, appID, jobID); err != nil {
		return err
	}
	if err := m.removeCronFiles(ctx, jobID); err != nil {
		return err
	}
	_, err := m.store.DB().ExecContext(ctx, "DELETE FROM app_cron_jobs WHERE id=? AND app_id=?", jobID, appID)
	return err
}

// deleteCronForApp is called with m.mu held, before deleting the app row.
func (m *Manager) deleteCronForApp(ctx context.Context, appID int64) error {
	jobs, err := m.cronJobsForApp(ctx, appID)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if err := m.removeCronFiles(ctx, job.ID); err != nil {
			return fmt.Errorf("stop scheduled task %d: %w", job.ID, err)
		}
	}
	return nil
}

func (m *Manager) RunCronJob(ctx context.Context, appID, jobID int64) error {
	if _, err := m.cronJob(ctx, appID, jobID); err != nil {
		return err
	}
	// --no-block avoids holding the HTTP request open for a long-running task.
	return systemctl(ctx, "start", "--no-block", cronServiceName(jobID))
}

func (m *Manager) CronLogs(ctx context.Context, appID, jobID int64) (string, error) {
	if _, err := m.cronJob(ctx, appID, jobID); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "journalctl", "-u", cronServiceName(jobID), "-n", "150", "--no-pager", "-o", "short-iso")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("read task journal: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}
