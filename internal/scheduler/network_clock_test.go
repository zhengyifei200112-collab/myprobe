package scheduler

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

type networkClockAssignments struct{ observed time.Time }

func (*networkClockAssignments) ExpireNetworkDispatchLeases(context.Context, time.Time) error {
	return nil
}
func (f *networkClockAssignments) ListNetworkAssignments(_ context.Context, now time.Time) ([]store.NetworkAssignment, error) {
	f.observed = now
	return nil, nil
}

func TestNetworkDelayedCycleUsesCurrentTime(t *testing.T) {
	now := time.Now().UTC()
	tick := now.Add(-time.Minute)
	f := &networkClockAssignments{}
	s := New(f, &diagnosticDispatcher{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.dispatchAt(context.Background(), tick, now)
	got := s.Diagnostics().LastCycle
	if !f.observed.Equal(now) || !got.ScheduledAt.Equal(tick) || got.StartDelaySeconds != 60 {
		t.Fatalf("stale tick selected network slots: %+v at %s", got, f.observed)
	}
}

type delayedNetworkAssignments struct{ delayedHTTPAssignments }

func (*delayedNetworkAssignments) ExpireNetworkDispatchLeases(context.Context, time.Time) error {
	return nil
}
func (f *delayedNetworkAssignments) ListNetworkAssignments(ctx context.Context, _ time.Time) ([]store.NetworkAssignment, error) {
	_, err := f.ListHTTPAssignments(ctx)
	return nil, err
}

func TestNetworkRunObservesDelayedTicker(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	f := &delayedNetworkAssignments{delayedHTTPAssignments{cancel: cancel}}
	s := New(f, &diagnosticDispatcher{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.Run(ctx)
	cycle := s.Diagnostics().LastCycle
	if f.calls != 2 || cycle == nil || cycle.StartDelaySeconds < 0.1 {
		t.Fatalf("network ticker lost its scheduled time: calls=%d cycle=%+v", f.calls, cycle)
	}
}
