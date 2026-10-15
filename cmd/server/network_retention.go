package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/diagnostics"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

type networkRetentionDatabase interface {
	RetainNetworkQuality(context.Context, time.Time, store.RetentionPolicy) (store.NetworkQualityRetentionBatch, error)
}

// Separate from hourly host rollups: network backlog must yield the writer after
// each batch, retry failures, and never wait an hour to continue partial work.
func runNetworkRetention(ctx context.Context, db networkRetentionDatabase, policy store.RetentionPolicy, interval time.Duration, logger *slog.Logger, observer *diagnostics.Loop) {
	stop := observer.Begin()
	defer stop()
	delay := 100 * time.Millisecond
	timer := time.NewTimer(delay)
	defer timer.Stop()
	observer.Schedule(delay)
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			batch, err := db.RetainNetworkQuality(ctx, time.Now().UTC(), policy)
			if ctx.Err() != nil {
				return
			}
			switch {
			case err != nil:
				logger.Error("retain network quality history", "error", err)
				delay = 5 * time.Second
			case batch.More:
				delay = 100 * time.Millisecond
			default:
				delay = interval
			}
			observer.Schedule(delay)
			timer.Reset(delay)
		}
	}
}
