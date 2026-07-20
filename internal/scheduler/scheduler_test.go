package scheduler

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

type fakeAssignments struct{ items []store.TargetAssignment }

func (f fakeAssignments) ListTargetAssignments(context.Context) ([]store.TargetAssignment, error) {
	return f.items, nil
}

type capturedDispatcher struct{ tasks []protocol.Task }

func (d *capturedDispatcher) SendTask(_ context.Context, _ string, task protocol.Task) error {
	d.tasks = append(d.tasks, task)
	return nil
}

func TestDispatchHonorsTargetInterval(t *testing.T) {
	port := 443
	assignments := fakeAssignments{items: []store.TargetAssignment{{
		NodeID: "node", Target: store.Target{ID: "target", Kind: protocol.TaskKindTCPing, Host: "example.com", Port: &port, TimeoutMS: 1000, IntervalSeconds: 30, Enabled: true},
	}}}
	dispatcher := &capturedDispatcher{}
	scheduler := New(assignments, dispatcher, slog.New(slog.NewTextHandler(io.Discard, nil)))
	now := time.Now().UTC()
	scheduler.dispatch(context.Background(), now)
	scheduler.dispatch(context.Background(), now.Add(29*time.Second))
	if len(dispatcher.tasks) != 1 {
		t.Fatalf("dispatched %d tasks before interval elapsed", len(dispatcher.tasks))
	}
	scheduler.dispatch(context.Background(), now.Add(30*time.Second))
	if len(dispatcher.tasks) != 2 {
		t.Fatalf("dispatched %d tasks after interval elapsed", len(dispatcher.tasks))
	}
}
