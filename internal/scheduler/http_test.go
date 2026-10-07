package scheduler

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

type httpFixture struct {
	items []store.HTTPAssignment
	mu    sync.Mutex
	slots []time.Time
}

func (f *httpFixture) ListHTTPAssignments(context.Context) ([]store.HTTPAssignment, error) {
	return f.items, nil
}
func (f *httpFixture) DispatchHTTP(_ context.Context, _, _ string, slot time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.slots = append(f.slots, slot)
	return agentgateway.ErrAgentOffline
}

func TestHTTPSlotsDoNotRetryOfflineOrReplayMissed(t *testing.T) {
	now := time.Now().UTC()
	f := &httpFixture{items: []store.HTTPAssignment{{ServiceID: "service", NodeID: "node", Revision: 1, IntervalSeconds: 30, Anchor: now}}}
	s := NewHTTP(f, f, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.dispatch(context.Background(), now)
	s.dispatch(context.Background(), now.Add(time.Second))
	if len(f.slots) != 1 {
		t.Fatalf("retried offline: %v", f.slots)
	}
	s.dispatch(context.Background(), now.Add(20*time.Second))
	if len(f.slots) != 1 {
		t.Fatal("replayed missed slot")
	}
	s.dispatch(context.Background(), now.Add(30*time.Second))
	if len(f.slots) != 2 || !f.slots[1].Equal(now.Add(30*time.Second)) {
		t.Fatalf("next slot: %v", f.slots)
	}
	// A new revision uses its own anchor and removes the obsolete schedule key.
	f.items[0].Revision = 2
	f.items[0].Anchor = now.Add(31 * time.Second)
	s.dispatch(context.Background(), now.Add(31*time.Second))
	if len(f.slots) != 3 || len(s.last) != 1 {
		t.Fatalf("revision: %v %v", f.slots, s.last)
	}
	f.items = nil
	s.dispatch(context.Background(), now.Add(32*time.Second))
	if len(s.last) != 0 {
		t.Fatal("deleted assignment retained")
	}
}
