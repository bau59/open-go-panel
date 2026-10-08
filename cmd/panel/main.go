package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	"strings"

	"github.com/bau59/open-go-panel/internal/adminer"
	"github.com/bau59/open-go-panel/internal/app"
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

	// Metrics ingestion uses a bounded journal batch and its own deadline. A
	// collector failure never prevents the panel or managed apps from serving.
	metricsCtx, stopMetrics := context.WithCancel(context.Background())
	defer stopMetrics()
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		pruneAt := time.Time{}
		observeAt := time.Time{}
		for {
			select {
			case <-metricsCtx.Done():
				return
			default:
			}
			pollCtx, cancel := context.WithTimeout(metricsCtx, 12*time.Second)
			if err := caddyManager.CollectConfiguredPerformance(pollCtx); err != nil && metricsCtx.Err() == nil {
				logger.Warn("HTTP metrics collection failed", "error", err)
			}
			cancel()
			if time.Since(observeAt)>=15*time.Second {
				targets,err:=apps.List()
				if err==nil {
					observed:=make([]panelcaddy.ServiceTarget,0,len(targets))
					for _,app:=range targets {
						observed=append(observed,panelcaddy.ServiceTarget{AppID:app.ID,Name:app.Name})
					}
					observeCtx,done:=context.WithTimeout(metricsCtx,5*time.Second)
					if err:=caddyManager.ObserveProcessStarts(observeCtx,observed);err!=nil && metricsCtx.Err()==nil {
						logger.Warn("observe systemd app starts failed","err",err)
					}
					done()
				}
				observeAt=time.Now()
			}
			if time.Since(pruneAt) >= time.Hour {
				pruneCtx, pruneCancel := context.WithTimeout(metricsCtx, 15*time.Second)
				if err := caddyManager.PrunePerformance(pruneCtx); err != nil && metricsCtx.Err() == nil {
					logger.Warn("HTTP metrics retention failed", "error", err)
				}
				pruneCancel()
				pruneAt = time.Now()
			}
			select {
			case <-metricsCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()

	// Backups and Git deployment checks run independently. A long backup
	// must not delay the 30-second interval configured for an application.
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			backupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
			ran, err := databaseManager.RunScheduledBackupIfDue(backupCtx, time.Now())
			cancel()
			if err != nil {
				logger.Error("scheduled database backup failed", "err", err)
			} else if ran {
				logger.Info("scheduled database backup completed")
			}
			<-ticker.C
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
