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
	"github.com/zhengyifei200112-collab/myprobe/internal/backup"
	"github.com/zhengyifei200112-collab/myprobe/internal/config"
	"github.com/zhengyifei200112-collab/myprobe/internal/diagnostics"
	"github.com/zhengyifei200112-collab/myprobe/internal/httpapi"
	"github.com/zhengyifei200112-collab/myprobe/internal/scheduler"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

var version = "dev"

func main() {
	startedAt := time.Now()
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
	var httpScheduler *scheduler.HTTPScheduler
	if cfg.HTTPProbesEnabled {
		gateway.EnableHTTPProbes(true)
		httpScheduler = scheduler.NewHTTP(database, gateway, logger)
		go httpScheduler.Run(runCtx)
		logger.Info("HTTP service probing enabled", "maintenance_exclusion_available", true)
	}
	alertService := alerts.New(database, cfg.EncryptionKey, nil, logger, cfg.NotificationOrigin)

	alertService.EnableHTTPServiceAlerts(cfg.HTTPProbesEnabled)
	alertService.SetHTTPAgentAvailability(gateway.SupportsHTTP)

	go alertService.Run(runCtx)
	backupWorker, backupErr := backup.NewScheduler(runCtx, database, cfg.BackupPassphraseFile, alertService.VerifyRecoveryChannels, alertService.ValidateBackupFailureChannels)
	if backupErr != nil {
		logger.Warn("scheduled backup worker unavailable", "error_code", backupErr.Error())
	} else {
		defer backupWorker.Close()
		go backupWorker.Run(runCtx)
	}
	retentionLoop := &diagnostics.Loop{}
	go runRetention(runCtx, database, retentionPolicy, cfg.Retention.Interval, logger, retentionLoop)
	networkRetentionLoop := &diagnostics.Loop{}
	go runNetworkRetention(runCtx, database, retentionPolicy, cfg.Retention.Interval, logger, networkRetentionLoop)
	api := httpapi.NewWithAlertService(cfg, database, authService, gateway, hub, alertService,
		httpapi.WithRuntimeIdentity(version, startedAt),
		httpapi.WithBackupScheduler(backupWorker),
		httpapi.WithRetentionWorkerHealth(retentionLoop.Snapshot),
		httpapi.WithNetworkRetentionWorkerHealth(networkRetentionLoop.Snapshot),
		httpapi.WithAlertHealth(alertService.Diagnostics),
		httpapi.WithHTTPSchedulerHealth(func() any {
			if httpScheduler == nil {
				return map[string]string{"status": "not_attached"}
			}
			return httpScheduler.Diagnostics()
		}),
		httpapi.WithSchedulerHealth(func() any { return latencyScheduler.Diagnostics() }))
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

type retentionDatabase interface {
	ApplyRetention(context.Context, time.Time, store.RetentionPolicy) error
	PruneHTTPHistory(context.Context, time.Time) (store.HTTPRetentionBatch, error)
}

func runRetention(ctx context.Context, database retentionDatabase, policy store.RetentionPolicy, interval time.Duration, logger *slog.Logger, observers ...*diagnostics.Loop) {
	var observer *diagnostics.Loop
	if len(observers) > 0 {
		observer = observers[0]
	}
	if observer != nil {
		stop := observer.Begin()
		defer stop()
	}
	schedule := func(delay time.Duration) {
		if observer != nil {
			observer.Schedule(delay)
		}
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	// Startup retention removes only one HTTP batch. Catch up after startup,
	// releasing the SQLite writer between every bounded pass.
	cleanup := time.NewTimer(100 * time.Millisecond)
	schedule(100 * time.Millisecond)
	defer cleanup.Stop()
	catchingUp := true
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := database.ApplyRetention(ctx, time.Now().UTC(), policy); err != nil && !errors.Is(err, context.Canceled) {
				logger.Error("apply history retention", "error", err)
			}
			if !catchingUp {
				cleanup.Reset(100 * time.Millisecond)
				schedule(100 * time.Millisecond)
			}
		case <-cleanup.C:
			batch, err := database.PruneHTTPHistory(ctx, time.Now().UTC())
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				logger.Error("prune HTTP history", "error", err)
				catchingUp = true
				cleanup.Reset(5 * time.Second)
				schedule(5 * time.Second)
				continue
			}
			if batch.TasksDeleted > 0 || batch.EpochsDeleted > 0 {
				logger.Info("pruned HTTP history batch", "tasks", batch.TasksDeleted, "epochs", batch.EpochsDeleted, "retained_from", batch.RetainedFrom, "more", batch.More)
			}
			catchingUp = batch.More
			if batch.More {
				cleanup.Reset(100 * time.Millisecond)
				schedule(100 * time.Millisecond)
			} else {
				cleanup.Reset(interval)
				schedule(interval)
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
