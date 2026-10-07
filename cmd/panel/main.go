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

	"github.com/bau59/open-go-panel/internal/config"
	"github.com/bau59/open-go-panel/internal/server"
)

func main() {
	cfg := config.Load()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           server.New(logger),
		ReadHeaderTimeout: 5 * time.Second,
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
