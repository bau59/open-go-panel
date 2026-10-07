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

	"github.com/bau59/open-go-panel/internal/app"
	"github.com/bau59/open-go-panel/internal/config"
	"github.com/bau59/open-go-panel/internal/linuxuser"
	"github.com/bau59/open-go-panel/internal/server"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("load config failed", "err", err)
		os.Exit(1)
	}

	users := linuxuser.New(logger)
	if err := users.EnsureGroup(context.Background()); err != nil {
		logger.Error("ensure managed users group failed", "err", err)
		os.Exit(1)
	}

	apps := app.New("/var/lib/open-go-panel/apps.json", users)

	srv := &http.Server{
		Addr: cfg.ListenAddr,
		Handler: server.New(server.Config{
			Logger:        logger,
			AdminUser:     cfg.AdminUser,
			AdminPassword: cfg.AdminPassword,
			Users:         users,
			Apps:          apps,
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
