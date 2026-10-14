package scheduler

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

type failingAssignments struct{ err error }

func (f failingAssignments) ListTargetAssignments(context.Context) ([]store.TargetAssignment, error) {
	return nil, f.err
}

type diagnosticDispatcher struct{ err error }

func (d *diagnosticDispatcher) SendTask(context.Context, string, protocol.Task) error { return d.err }

func TestSchedulerDiagnosticsOutcomes(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	assignments := fakeAssignments{items: []store.TargetAssignment{{NodeID: "node", Target: store.Target{ID: "target", IntervalSeconds: 30}}}}
	d := &diagnosticDispatcher{}
	s := New(assignments, d, logger)
	if got := s.Diagnostics(); got.Job.State != "never_run" || got.LastCycle != nil {
		t.Fatalf("invented cycle: %+v", got)
	}
	now := time.Now().UTC()
	s.dispatch(context.Background(), now)
	got := s.Diagnostics()
	if got.Job.State != "success" || got.LastCycle.Dispatched != 1 || got.LastCycle.Due != 1 {
		t.Fatalf("success: %+v", got)
	}
	got.LastCycle.Dispatched = 99
	if s.Diagnostics().LastCycle.Dispatched != 1 {
		t.Fatal("snapshot aliases scheduler")
	}
	s.dispatch(context.Background(), now.Add(time.Second))
	if s.Diagnostics().LastCycle.Due != 0 {
		t.Fatal("interval ignored")
	}
	d.err = agentgateway.ErrAgentOffline
	s.dispatch(context.Background(), now.Add(30*time.Second))
	got = s.Diagnostics()
	if got.Job.State != "success" || got.LastCycle.Offline != 1 || got.LastCycle.Failed != 0 {
		t.Fatalf("offline: %+v", got)
	}
	d.err = errors.New("private transport error")
	s.dispatch(context.Background(), now.Add(31*time.Second))
	got = s.Diagnostics()
	if got.Job.State != "failed" || got.LastCycle.Failed != 1 || got.Job.LastErrorCode != "operation_failed" {
		t.Fatalf("failure: %+v", got)
	}
	for _, err := range []error{errors.New("private database error"), context.Canceled} {
		s.store = failingAssignments{err: err}
		s.dispatch(context.Background(), now)
		got = s.Diagnostics()
		want := "failed"
		if errors.Is(err, context.Canceled) {
			want = "cancelled"
		}
		if got.Job.State != want || got.LastCycle.Dispatched != 0 || got.LastCycle.AssignmentsLoaded {
			t.Fatalf("read failure: %+v", got)
		}
	}
}
