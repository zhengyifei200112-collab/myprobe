package agentgateway

import (
	"context"
	"testing"
	"time"

	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestNodePublicationRetainsPublicShapeAndSkipsHiddenOrMissing(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "publication fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SaveReport(ctx, node.ID, protocol.Report{CapturedAt: time.Now().UTC(), PublicIP: "203.0.113.42"}); err != nil {
		t.Fatal(err)
	}
	hub := NewHub()
	events, unsubscribe := hub.Subscribe()
	defer unsubscribe()
	gateway := New(db, hub)
	gateway.publishNode(ctx, node.ID)
	select {
	case event := <-events:
		if event.Type != "node_metrics" || event.Node.Node.ID != node.ID || event.Node.Report == nil || event.Node.Report.PublicIP != "203.0.••.••" {
			t.Fatal("publication changed the public event")
		}
	case <-time.After(time.Second):
		t.Fatal("visible update missing")
	}
	if _, err := db.UpdateNode(ctx, node.ID, store.UpdateNodeParams{Name: node.Name, Hidden: true, LatencyMode: protocol.TaskKindPing, CollectionSeconds: 5, ReportSeconds: 5}); err != nil {
		t.Fatal(err)
	}
	gateway.publishNode(ctx, node.ID)
	gateway.publishNode(ctx, "missing-node")
	select {
	case <-events:
		t.Fatal("hidden/missing node published")
	default:
	}
}
