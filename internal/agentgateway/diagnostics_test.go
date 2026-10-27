package agentgateway

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDiagnosticsExpiryAndPrivacy(t *testing.T) {
	now := time.Now().UTC()
	g := New(nil, NewHub())
	g.sessions["private-node"] = &agentSession{}
	g.pending["private-task"] = pendingTask{nodeID: "private-node", targetID: "private-target", expires: now.Add(time.Second)}
	g.pending["boundary"] = pendingTask{expires: now}
	g.pending["expired"] = pendingTask{expires: now.Add(-time.Second)}
	snapshot := g.diagnosticsAt(now)
	if snapshot.AgentConnections != 1 || snapshot.PendingResults != 1 || snapshot.ExpiredResults != 2 {
		t.Fatalf("unexpected counts: %+v", snapshot)
	}
	if len(g.pending) != 3 {
		t.Fatal("diagnostics must not remove pending tasks")
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private-") {
		t.Fatal("identifiers leaked")
	}
	delete(g.sessions, "private-node")
	if g.Diagnostics().AgentConnections != 0 {
		t.Fatal("disconnected session retained")
	}
}

func TestDiagnosticSubscriberLifecycle(t *testing.T) {
	hub := NewHub()
	if hub.SubscriberCount() != 0 {
		t.Fatal("unexpected initial subscribers")
	}
	_, first := hub.Subscribe()
	_, second := hub.Subscribe()
	if hub.SubscriberCount() != 2 {
		t.Fatal("subscriptions not counted")
	}
	first()
	first()
	if hub.SubscriberCount() != 1 {
		t.Fatal("duplicate unsubscribe changed count")
	}
	second()
	if hub.SubscriberCount() != 0 {
		t.Fatal("subscription leaked")
	}
}

func TestDiagnosticsHTTPGraceAndBusyObservation(t *testing.T) {
	now := time.Now().UTC()
	g := New(nil, NewHub())
	session := &agentSession{version: 2, httpPending: map[string]time.Time{"private-http-task": now.Add(time.Second), "expired-http-task": now}}
	g.sessions["private-node"] = session
	got := g.diagnosticsAt(now)
	if got.HTTPResultsStatus != "available" || got.HTTPPendingResults == nil || *got.HTTPPendingResults != 1 || *got.HTTPExpiredResults != 1 || got.V2Connections != 1 || got.V1Connections != 0 {
		t.Fatalf("HTTP: %+v", got)
	}
	if len(session.httpPending) != 2 {
		t.Fatal("polling removed HTTP entries")
	}
	session.httpMu.Lock()
	defer session.httpMu.Unlock()
	busy := g.diagnosticsAt(now)
	if busy.HTTPResultsStatus != "unavailable" || busy.HTTPPendingResults != nil || busy.HTTPExpiredResults != nil {
		t.Fatal("busy observation reported false zero")
	}
}
