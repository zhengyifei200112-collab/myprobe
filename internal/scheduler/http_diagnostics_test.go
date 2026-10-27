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
