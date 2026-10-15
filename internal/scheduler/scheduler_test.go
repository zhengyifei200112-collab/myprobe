package scheduler

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

type fakeAssignments struct{ items []store.NetworkAssignment }

func (f fakeAssignments) ExpireNetworkDispatchLeases(context.Context, time.Time) error { return nil }

func (f fakeAssignments) ListNetworkAssignments(context.Context, time.Time) ([]store.NetworkAssignment, error) {
	return f.items, nil
}

type blockedNetworkDispatcher struct {
	entered         chan struct{}
	release         <-chan struct{}
	active, maximum atomic.Int32
}

func (d *blockedNetworkDispatcher) DispatchNetwork(context.Context, string, time.Time) error {
	active := d.active.Add(1)
	for previous := d.maximum.Load(); active > previous; previous = d.maximum.Load() {
		if d.maximum.CompareAndSwap(previous, active) {
			break
		}
	}
	d.entered <- struct{}{}
	<-d.release
	d.active.Add(-1)
	return nil
}

func TestNetworkDispatchBoundsBlockedConnections(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	release := make(chan struct{})
	var once sync.Once
	d := &blockedNetworkDispatcher{entered: make(chan struct{}, 16), release: release}
	items := make([]store.NetworkAssignment, 16)
	for i := range items {
		items[i].Eligible = true
	}
	s := New(fakeAssignments{items: items}, d, slog.New(slog.NewTextHandler(io.Discard, nil)))
	done := make(chan struct{})
	go func() { defer close(done); s.dispatch(ctx, time.Now().UTC()) }()
	defer func() { once.Do(func() { close(release) }); <-done }()
	for range 4 {
		select {
		case <-d.entered:
		case <-ctx.Done():
			t.Fatal("dispatch workers did not start")
		}
	}
	select {
	case <-d.entered:
		t.Fatal("blocked dispatch exceeded four workers")
	case <-time.After(50 * time.Millisecond):
	}
	once.Do(func() { close(release) })
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("dispatch did not finish after unblocking")
	}
	if d.maximum.Load() != 4 || s.Diagnostics().LastCycle.Dispatched != 16 {
		t.Fatal("bounded dispatch lost work")
	}
}

type capturedDispatcher struct{ tasks []time.Time }

func (d *capturedDispatcher) DispatchNetwork(_ context.Context, _ string, slot time.Time) error {
	d.tasks = append(d.tasks, slot)
	return nil
}

func TestDispatchHonorsPersistentSlotEligibility(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	assignments := fakeAssignments{items: []store.NetworkAssignment{{EpochID: "epoch", NodeID: "node", TargetID: "target", ScheduledAt: now, Eligible: true}}}
	dispatcher := &capturedDispatcher{}
	scheduler := New(assignments, dispatcher, slog.New(slog.NewTextHandler(io.Discard, nil)))
	scheduler.dispatch(context.Background(), now)
	assignments.items[0].Eligible = false
	scheduler.dispatch(context.Background(), now.Add(29*time.Second))
	if len(dispatcher.tasks) != 1 {
		t.Fatalf("dispatched %d tasks before interval elapsed", len(dispatcher.tasks))
	}
	assignments.items[0].Eligible = true
	assignments.items[0].ScheduledAt = now.Add(30 * time.Second)
	scheduler.dispatch(context.Background(), now.Add(30*time.Second))
	if len(dispatcher.tasks) != 2 {
		t.Fatalf("dispatched %d tasks after interval elapsed", len(dispatcher.tasks))
	}
	if !dispatcher.tasks[1].Equal(now.Add(30 * time.Second)) {
		t.Fatal("scheduler replaced the persisted plan phase")
	}
}
