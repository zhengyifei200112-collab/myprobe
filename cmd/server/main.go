package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
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
	recovery, err := store.ApplyPendingRestore(ctx, cfg.DatabasePath, time.Now().UTC())
	if err != nil {
		logger.Error("apply pending database restore", "error", err)
		os.Exit(1)
	}
	if recovery != "" {
		logger.Warn("pending database restore applied; previous database preserved", "recovery_path", recovery)
	}
	database, err := store.Open(ctx, cfg.DatabasePath)
	if err != nil {
		logger.Error("open database", "error", err)
		os.Exit(1)
	}
	defer database.Close()
	retentionPolicy := store.RetentionPolicy{
		Raw: cfg.Retention.Raw, OneMinute: cfg.Retention.OneMinute, FiveMinute: cfg.Retention.FiveMinute,
	}
	if err := database.ApplyRetention(ctx, time.Now().UTC(), retentionPolicy); err != nil {
		logger.Error("apply history retention", "error", err)
		os.Exit(1)
	}

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
	go runRetention(runCtx, database, retentionPolicy, cfg.Retention.Interval, logger)
	api := httpapi.New(cfg, database, authService, gateway, hub)
	server := &http.Server{
		Addr: cfg.ListenAddress, Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 90 * time.Second,
	}

	if isPublicListener(cfg.ListenAddress) && !cfg.PublicHTTPAck {
		logger.Warn("public HTTP listener has no TLS; place MyProbe behind HTTPS or explicitly acknowledge direct HTTP", "environment", "MYPROBE_PUBLIC_HTTP_ACKNOWLEDGED")
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

func runRetention(ctx context.Context, database *store.Store, policy store.RetentionPolicy, interval time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if err := database.ApplyRetention(ctx, now.UTC(), policy); err != nil && !errors.Is(err, context.Canceled) {
				logger.Error("apply history retention", "error", err)
			}
		}
	}
}

func isPublicListener(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return true
	}
	if host == "" {
		return true
	}
	ip := net.ParseIP(host)
	return ip == nil || ip.IsUnspecified()
}
