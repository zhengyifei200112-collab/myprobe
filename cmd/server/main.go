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

	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/alerts"
	"github.com/zhengyifei200112-collab/myprobe/internal/auth"
	"github.com/zhengyifei200112-collab/myprobe/internal/config"
	"github.com/zhengyifei200112-collab/myprobe/internal/httpapi"
	"github.com/zhengyifei200112-collab/myprobe/internal/scheduler"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	ctx := context.Background()
	database, err := store.Open(ctx, cfg.DatabasePath)
	if err != nil {
		logger.Error("open database", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	authService := auth.New(database, cfg.SessionTTL)
	generatedPassword, err := authService.Bootstrap(ctx, cfg.AdminUsername, cfg.AdminPassword)
	if err != nil {
		logger.Error("bootstrap administrator", "error", err)
		os.Exit(1)
	}
	if generatedPassword != "" {
		logger.Warn("generated initial administrator password; change it after login", "username", cfg.AdminUsername, "password", generatedPassword)
	}

	hub := agentgateway.NewHub()
	gateway := agentgateway.New(database, hub)
	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	latencyScheduler := scheduler.New(database, gateway, logger)
	go latencyScheduler.Run(runCtx)
	alertService := alerts.New(database, cfg.EncryptionKey, nil, logger)
	go alertService.Run(runCtx)
	api := httpapi.New(cfg, database, authService, gateway, hub)
	server := &http.Server{
		Addr: cfg.ListenAddress, Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 90 * time.Second,
	}

	go func() {
		logger.Info("MyProbe server started", "address", cfg.ListenAddress)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server stopped", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	cancelRun()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}
}
