package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/diagnostics"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

type networkRetentionFixture func(context.Context, time.Time, store.RetentionPolicy) (store.NetworkQualityRetentionBatch, error)

func (f networkRetentionFixture) RetainNetworkQuality(ctx context.Context, now time.Time, p store.RetentionPolicy) (store.NetworkQualityRetentionBatch, error) {
	return f(ctx, now, p)
}

func TestNetworkRetentionStartupYieldRetryAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	called := make(chan time.Time, 4)
	done := make(chan struct{})
	observer := &diagnostics.Loop{}
	var count atomic.Int64
	fixture := networkRetentionFixture(func(_ context.Context, now time.Time, p store.RetentionPolicy) (store.NetworkQualityRetentionBatch, error) {
		if p != store.DefaultRetentionPolicy() {
			t.Error("worker lost configured policy")
		}
		called <- now
		switch count.Add(1) {
		case 1:
			return store.NetworkQualityRetentionBatch{}, errors.New("synthetic transient failure")
		case 2:
			return store.NetworkQualityRetentionBatch{More: true}, nil
		default:
			return store.NetworkQualityRetentionBatch{}, nil
		}
	})
	go func() {
		defer close(done)
		runNetworkRetention(ctx, fixture, store.DefaultRetentionPolicy(), time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)), observer)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("network retention loop did not cancel")
		}
	})
	var previous time.Time
	for i := range 3 {
		select {
		case at := <-called:
			if i == 1 && at.Sub(previous) < 4*time.Second {
				t.Fatal("failure retry has no backoff")
			}
			if i == 2 && at.Sub(previous) < 80*time.Millisecond {
				t.Fatal("batch did not yield the writer")
			}
			previous = at
		case <-time.After(7 * time.Second):
			t.Fatal("network backlog waited for the hourly timer")
		}
	}
	if observer.Snapshot().Status != "running" {
		t.Fatal("actual worker entry not observed")
	}
	select {
	case <-called:
		t.Fatal("empty backlog was busy-polled")
	case <-time.After(200 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
	if observer.Snapshot().Status != "not_running" {
		t.Fatal("stopped worker still reported running")
	}
}
