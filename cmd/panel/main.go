package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	"strings"

	"github.com/bau59/open-go-panel/internal/adminer"
	"github.com/bau59/open-go-panel/internal/app"
	"github.com/bau59/open-go-panel/internal/filemanager"
	panelcaddy "github.com/bau59/open-go-panel/internal/caddy"
	"github.com/bau59/open-go-panel/internal/config"
	"github.com/bau59/open-go-panel/internal/linuxuser"
	"github.com/bau59/open-go-panel/internal/dbmanager"
	paneldocker "github.com/bau59/open-go-panel/internal/docker"
	"github.com/bau59/open-go-panel/internal/security"
	"github.com/bau59/open-go-panel/internal/software"
	"github.com/bau59/open-go-panel/internal/server"
	"github.com/bau59/open-go-panel/internal/state"
)

func main() {
	if len(os.Args)==3 && os.Args[1]=="--restore-server-snapshot" {
		logger:=slog.New(slog.NewTextHandler(os.Stdout,nil))
		if err:=server.RunServerRestore(context.Background(),os.Args[2],logger);err!=nil{logger.Error("restore failed","err",err);os.Exit(1)}
		return
	}
    // In helper mode the process runs as the app owner, before any privileged
    // panel config, SQLite state or background services are initialized.
    if len(os.Args) > 1 && os.Args[1] == "--app-file-helper" {
        if err := filemanager.RunHelper(os.Args[2:]); err != nil {
            fmt.Fprintln(os.Stderr, err)
            os.Exit(1)
        }
        return
    }
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("load config failed", "err", err)
		os.Exit(1)
	}

	stateStore, err := state.Open("/var/lib/open-go-panel/panel.db")
	if err != nil {
		logger.Error("open panel state failed", "err", err)
		os.Exit(1)
	}
	defer stateStore.Close()

	users := linuxuser.New(logger)
	if err := users.EnsureGroup(context.Background()); err != nil {
		logger.Error("ensure managed users group failed", "err", err)
		os.Exit(1)
	}

	apps := app.New(stateStore, "/var/lib/open-go-panel/apps.json", users)
	caddyManager := panelcaddy.New(stateStore, "/var/lib/open-go-panel/caddy-sites.json")
	securityManager := security.New()
	databaseManager := dbmanager.New(stateStore, "/var/lib/open-go-panel/databases.json")
	adminerManager := adminer.New()
	softwareManager := software.New()
	dockerManager := paneldocker.New()
	if err := apps.EnsureStorage(); err != nil {
		logger.Error("ensure app storage failed", "err", err)
		os.Exit(1)
	}

	if _, err := apps.List(); err != nil {
		logger.Error("migrate apps state failed", "err", err)
		os.Exit(1)
	}
	if _, err := databaseManager.List(); err != nil {
		logger.Error("migrate database state failed", "err", err)
		os.Exit(1)
	}
	if _, err := caddyManager.Sites(); err != nil {
		logger.Error("migrate Caddy state failed", "err", err)
		os.Exit(1)
	}

	appItems, _ := apps.List()
	dbItems, _ := databaseManager.List()
	for _, appItem := range appItems {
		for _, line := range strings.Split(appItem.Service.Environment, "\n") {
			parts := strings.SplitN(strings.TrimSpace(line), "=", 2)
			if len(parts) != 2 || parts[0] == "" {
				continue
			}
			for _, dbItem := range dbItems {
				if parts[1] == dbItem.DSN() {
					_ = databaseManager.Attach(appItem.ID, dbItem.ID, parts[0])
					break
				}
			}
		}
	}

	// HTTP diagnostics are computed on demand from Caddy logs. No continuous
	// metrics importer, SQLite duplication, or systemd/Air polling is started.

	// Backups and Git deployment checks run independently. A long backup
	// must not delay the 30-second interval configured for an application.
	backupWorkCtx, stopBackupWork := context.WithCancel(context.Background())
	defer stopBackupWork()
	databaseManager.SetBackupWorkerContext(backupWorkCtx)
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			if backupWorkCtx.Err() != nil { return }
			// Local retention and off-site transfer have independent schedules.
			// Neither a 30-second HTTP deadline nor a two-hour global cap can
			// cancel a multi-gigabyte dump or a slow upload.
			ran, err := databaseManager.RunScheduledBackupIfDue(backupWorkCtx, time.Now())
			if err != nil {
				logger.Error("scheduled local database backup failed", "err", err)
			} else if ran {
				logger.Info("scheduled local database backup completed")
			}
			if backupWorkCtx.Err() != nil { return }
			sent, err := databaseManager.RunScheduledOffsiteIfDue(backupWorkCtx, time.Now())
			if err != nil {
				logger.Error("scheduled off-site database upload failed", "err", err)
			} else if sent {
				logger.Info("scheduled off-site database upload completed")
			}
			select {
			case <-backupWorkCtx.Done(): return
			case <-ticker.C:
			}
		}
	}()

	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			deployCtx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
			deployed, err := apps.RunAutoDeploysDue(deployCtx, time.Now())
			cancel()
			if err != nil {
				logger.Error("automatic app deploy check failed", "err", err)
			} else if deployed > 0 {
				logger.Info("automatic app deploy completed", "count", deployed)
			}
			// Checks are serialized; while a deployment is running, subsequent
			// checks may be delayed but will not overlap with it.
			<-ticker.C
		}
	}()

	srv := &http.Server{
		Addr: cfg.ListenAddr,
		Handler: server.New(server.Config{
			Logger:        logger,
			AdminUser:     cfg.AdminUser,
			AdminPassword: cfg.AdminPassword,
			Users:         users,
			Apps:          apps,
			Caddy:         caddyManager,
			Security:      securityManager,
			Databases:     databaseManager,
			Adminer:       adminerManager,
			Software:      softwareManager,
			Docker:        dockerManager,
			State:         stateStore,
			ClosePanel:    schedulePanelStop,
			BackupContext: backupWorkCtx,
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		logger.Info("open go panel started", "addr", cfg.ListenAddr)

		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server failed", "err", err)
			os.Exit(1)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("http server shutdown failed", "err", err)
		os.Exit(1)
	}
}
