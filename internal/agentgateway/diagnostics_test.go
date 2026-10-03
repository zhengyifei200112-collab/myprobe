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
