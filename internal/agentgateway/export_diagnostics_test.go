package agentgateway

import (
	"testing"
	"time"
)

func TestTryDiagnosticsBusyLocksAndNoMutation(t *testing.T) {
	g := New(nil, NewHub())
	g.sessions["synthetic-node"] = &agentSession{version: 2, httpPending: map[string]time.Time{"synthetic-http": time.Now().Add(time.Minute)}}
	g.pending["synthetic-task"] = pendingTask{expires: time.Now().Add(time.Minute)}
	g.sessionsMu.Lock()
	snapshot, ok := g.TryDiagnostics()
	g.sessionsMu.Unlock()
	if ok {
		t.Fatalf("busy session map invented evidence: %+v", snapshot)
	}
	g.pendingMu.Lock()
	_, ok = g.TryDiagnostics()
	g.pendingMu.Unlock()
	if ok {
		t.Fatal("busy pending map invented evidence")
	}
	snapshot, ok = g.TryDiagnostics()
	if !ok || snapshot.AgentConnections != 1 || snapshot.V2Connections != 1 || snapshot.PendingResults != 1 || snapshot.HTTPPendingResults == nil || *snapshot.HTTPPendingResults != 1 {
		t.Fatalf("actual transport evidence: %+v %v", snapshot, ok)
	}
	if len(g.pending) != 1 || len(g.sessions["synthetic-node"].httpPending) != 1 {
		t.Fatal("diagnostic export changed pending state")
	}
	h := NewHub()
	h.mu.Lock()
	_, ok = h.TrySubscriberCount()
	h.mu.Unlock()
	if ok {
		t.Fatal("busy subscriber map invented zero")
	}
}
