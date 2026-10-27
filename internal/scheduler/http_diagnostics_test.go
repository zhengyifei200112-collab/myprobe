package scheduler

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

type httpDiagnosticFixture struct {
	items   []store.HTTPAssignment
	readErr error
}

func (f *httpDiagnosticFixture) ListHTTPAssignments(context.Context) ([]store.HTTPAssignment, error) {
	return f.items, f.readErr
}
func (f *httpDiagnosticFixture) DispatchHTTP(_ context.Context, node, _ string, _ time.Time) error {
	switch node {
	case "ok":
		return nil
	case "offline":
		return agentgateway.ErrAgentOffline
	case "unsupported":
		return agentgateway.ErrHTTPUnavailable
	case "busy":
		return agentgateway.ErrHTTPBusy
	case "changed":
		return store.ErrHTTPTaskRejected
	default:
		return errors.New("private target marker")
	}
}

func TestHTTPDiagnosticsObserveCategoriesAndReadFailure(t *testing.T) {
	now := time.Now().UTC()
	f := &httpDiagnosticFixture{}
	for _, node := range []string{"ok", "offline", "unsupported", "busy", "changed", "error"} {
		f.items = append(f.items, store.HTTPAssignment{ServiceID: "private-service", NodeID: node, Revision: 1, IntervalSeconds: 30, Anchor: now})
	}
	s := NewHTTP(f, f, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.dispatch(context.Background(), now)
	snapshot := s.Diagnostics()
	c := snapshot.LastCycle
	if snapshot.Running || snapshot.Job.State != "failed" || !c.AssignmentsLoaded || c.Due != 6 || c.Dispatched != 1 || c.Offline != 1 || c.Unavailable != 1 || c.Busy != 1 || c.Rejected != 1 || c.Failed != 1 {
		t.Fatalf("cycle: %+v %+v", snapshot.Job, c)
	}
	f.readErr = errors.New("private read marker")
	s.dispatch(context.Background(), now)
	if s.Diagnostics().LastCycle.AssignmentsLoaded || s.Diagnostics().Job.State != "failed" {
		t.Fatal("read failure claimed no assignments")
	}
}

type delayedHTTPAssignments struct {
	calls  int
	cancel context.CancelFunc
}

func (f *delayedHTTPAssignments) ListHTTPAssignments(context.Context) ([]store.HTTPAssignment, error) {
	f.calls++
	if f.calls == 1 {
		// Leave the first ticker event waiting while the initial cycle works.
		time.Sleep(1200 * time.Millisecond)
	} else {
		f.cancel()
	}
	return nil, nil
}

func TestHTTPRunObservesDelayedTicker(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	f := &delayedHTTPAssignments{cancel: cancel}
	s := NewHTTP(f, &httpFixture{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.Run(ctx)
	cycle := s.Diagnostics().LastCycle
	if f.calls != 2 || cycle == nil || cycle.StartDelaySeconds < 0.1 {
		t.Fatalf("delayed ticker lost its scheduled time: calls=%d cycle=%+v", f.calls, cycle)
	}
}

func TestHTTPDelayedCycleUsesActualTimeForSlots(t *testing.T) {
	now := time.Now().UTC()
	tick := now.Add(-time.Minute)
	f := &httpFixture{items: []store.HTTPAssignment{{ServiceID: "service", NodeID: "node", Revision: 1, IntervalSeconds: 30, Anchor: now}}}
	s := NewHTTP(f, f, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.dispatchAt(context.Background(), tick, now)
	cycle := s.Diagnostics().LastCycle
	if !cycle.ScheduledAt.Equal(tick) || cycle.StartDelaySeconds != 60 || cycle.Due != 1 || len(f.slots) != 1 || !f.slots[0].Equal(now) {
		t.Fatalf("scheduled tick changed live slot selection: cycle=%+v slots=%v", cycle, f.slots)
	}
}
