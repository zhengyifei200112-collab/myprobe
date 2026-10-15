package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

func TestPublicNodePointLookupPreservesProjectionAndVisibility(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UTC()
	node, _, err := db.CreateNode(ctx, CreateNodeParams{Name: "visible fixture", CollectionSeconds: 15, ReportSeconds: 15})
	if err != nil {
		t.Fatal(err)
	}
	expires := now.Add(48 * time.Hour)
	if _, err := db.UpdateNode(ctx, node.ID, UpdateNodeParams{Name: node.Name, LatencyMode: protocol.TaskKindTCPing, CollectionSeconds: 15, ReportSeconds: 15, ExpiresAt: &expires, Tags: []string{"public-tag"}}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveAgentMetadata(ctx, node.ID, protocol.Hello{AgentVersion: "private-version", Hostname: "private-host", OS: "linux", Platform: "synthetic", Capabilities: []string{"private-capability"}}); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		if err := db.SaveReport(ctx, node.ID, protocol.Report{CapturedAt: now.Add(time.Duration(i-1) * time.Minute), PublicIP: "203.0.113.42", CPU: protocol.CPUMetric{UsagePercent: 23}, Networks: []protocol.NetworkMetric{{Interface: "synthetic", RXTotalBytes: uint64(1000 + i*50), TXTotalBytes: uint64(2000 + i*75)}}}); err != nil {
			t.Fatal(err)
		}
	}
	port := 443
	target, err := db.CreateTarget(ctx, CreateTargetParams{Name: "owned alias", Kind: protocol.TaskKindTCPing, Host: "example.test", Port: &port})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AssignTarget(ctx, node.ID, target.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveLatencyResult(ctx, node.ID, protocol.TaskKindTCPing, protocol.LatencyResult{TaskID: "synthetic-task", TargetID: target.ID, Success: true, LatencyMS: 12, CompletedAt: now}); err != nil {
		t.Fatal(err)
	}
	items, err := db.ListPublicNodes(ctx, now)
	if err != nil || len(items) != 1 {
		t.Fatalf("list: %v", err)
	}
	point, err := db.PublicNode(ctx, node.ID, now)
	if err != nil || !reflect.DeepEqual(items[0], *point) {
		t.Fatalf("point changed public projection: %v", err)
	}
	if point.Report == nil || point.Report.PublicIP != "203.0.••.••" || len(point.Latency) != 1 || point.Commercial == nil || point.Node.Agent == nil {
		t.Fatal("public report/latency/commercial/metadata missing")
	}
	raw, _ := json.Marshal(point)
	for _, private := range []string{"private-host", "private-version", "private-capability", "203.0.113.42"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("point lookup exposed private metadata")
		}
	}
	if _, err := db.UpdateNode(ctx, node.ID, UpdateNodeParams{Name: node.Name, Hidden: true, LatencyMode: protocol.TaskKindTCPing, CollectionSeconds: 15, ReportSeconds: 15}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{node.ID, "missing-node", "", "' OR 1=1 --"} {
		if point, err := db.PublicNode(ctx, id, now); point != nil || !errors.Is(err, ErrNotFound) {
			t.Fatalf("invisible lookup returned data: %v", err)
		}
	}
}

func TestPublicNodePointLookupDoesNotReadUnrelatedNode(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	node, _, err := db.CreateNode(ctx, CreateNodeParams{Name: "healthy fixture"})
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := db.CreateNode(ctx, CreateNodeParams{Name: "unrelated fixture"})
	if err != nil {
		t.Fatal(err)
	}
	// Corrupt a foreign numeric value to prove publication does not scan it.
	// Normal management does not admit this value; this is a disk fault fixture.
	if _, err := db.db.ExecContext(ctx, "UPDATE nodes SET price_minor='invalid-numeric-fixture' WHERE id=?", other.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ListPublicNodes(ctx, time.Now().UTC()); err == nil {
		t.Fatal("foreign corruption fixture did not affect full scan")
	}
	if point, err := db.PublicNode(ctx, node.ID, time.Now().UTC()); err != nil || point.Node.ID != node.ID {
		t.Fatalf("foreign row blocked point publication: %v", err)
	}
}
