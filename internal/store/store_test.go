package store

import (
	"context"
	"errors"
	"testing"
	"time"

	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

func TestNodeTokenAndReportRoundTrip(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	node, token, err := store.CreateNode(ctx, CreateNodeParams{Name: "Tokyo", Tags: []string{"JP", "Production"}})
	if err != nil {
		t.Fatal(err)
	}
	if token == "" {
		t.Fatal("CreateNode returned an empty token")
	}
	authenticated, err := store.AuthenticateAgent(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if authenticated.ID != node.ID {
		t.Fatalf("authenticated node = %q, want %q", authenticated.ID, node.ID)
	}

	report := protocol.Report{
		CapturedAt: time.Now().UTC(),
		CPU:        protocol.CPUMetric{Model: "Test CPU", LogicalCores: 2, UsagePercent: 12.5},
		Memory:     protocol.MemoryMetric{TotalBytes: 1024, UsedBytes: 512, UsagePercent: 50},
		Disks:      []protocol.DiskMetric{{Mount: "/", TotalBytes: 2048, UsedBytes: 1024, UsagePercent: 50}},
		Networks:   []protocol.NetworkMetric{{Interface: "eth0", RXTotalBytes: 20, TXTotalBytes: 10, RXBytesPerS: 2, TXBytesPerS: 1}},
	}
	if err := store.SaveReport(ctx, node.ID, report); err != nil {
		t.Fatal(err)
	}
	nodes, err := store.ListPublicNodes(ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].Report == nil {
		t.Fatalf("ListPublicNodes() = %#v", nodes)
	}
	if nodes[0].Report.CPU.Model != "Test CPU" {
		t.Fatalf("CPU model = %q", nodes[0].Report.CPU.Model)
	}
	history, err := store.MetricHistory(ctx, node.ID, time.Now().UTC().Add(-time.Hour), 60)
	if err != nil || len(history) != 1 || history[0].CPUPercent != report.CPU.UsagePercent {
		t.Fatalf("metric history = %#v, error = %v", history, err)
	}
}

func TestLatencyAssignmentAndResultRoundTrip(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	node, _, err := database.CreateNode(ctx, CreateNodeParams{Name: "probe-node"})
	if err != nil {
		t.Fatal(err)
	}
	port := 443
	target, err := database.CreateTarget(ctx, CreateTargetParams{Name: "example HTTPS", Kind: protocol.TaskKindTCPing, Host: "example.com", Port: &port, IntervalSeconds: 30, TimeoutMS: 1000})
	if err != nil {
		t.Fatal(err)
	}
	group, err := database.CreateTargetGroup(ctx, "public TCP", protocol.TaskKindTCPing)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AddTargetToGroup(ctx, group.ID, target.ID); err != nil {
		t.Fatal(err)
	}
	if err := database.AssignTargetGroup(ctx, node.ID, group.ID); err != nil {
		t.Fatal(err)
	}
	assignments, err := database.ListTargetAssignments(ctx)
	if err != nil || len(assignments) != 1 || assignments[0].Target.ID != target.ID {
		t.Fatalf("assignments = %#v, error = %v", assignments, err)
	}
	result := protocol.LatencyResult{TaskID: "task", TargetID: target.ID, Success: true, LatencyMS: 18.25, CompletedAt: time.Now().UTC()}
	if err := database.SaveLatencyResult(ctx, node.ID, protocol.TaskKindTCPing, result); err != nil {
		t.Fatal(err)
	}
	latest, err := database.ListLatestLatency(ctx, node.ID)
	if err != nil || len(latest) != 1 || latest[0].LatencyMS == nil || *latest[0].LatencyMS != result.LatencyMS {
		t.Fatalf("latest = %#v, error = %v", latest, err)
	}
	history, err := database.LatencyHistory(ctx, node.ID, time.Now().UTC().Add(-time.Hour), 60)
	if err != nil || len(history) != 1 || history[0].LatencyMS == nil || *history[0].LatencyMS != result.LatencyMS || history[0].SuccessRate != 100 {
		t.Fatalf("latency history = %#v, error = %v", history, err)
	}
}

func TestAdministrativeCRUDTokenRotationAndAudit(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	node, oldToken, err := database.CreateNode(ctx, CreateNodeParams{Name: "old"})
	if err != nil {
		t.Fatal(err)
	}
	reset := 15
	price := int64(499)
	expires := time.Now().UTC().Add(30 * 24 * time.Hour)
	updated, err := database.UpdateNode(ctx, node.ID, UpdateNodeParams{Name: "updated", SortOrder: 2, Tags: []string{"US"}, CountryCode: "us", Currency: "usd", PriceMinor: &price, BillingCycle: "year", ExpiresAt: &expires, TrafficResetDay: &reset, LatencyMode: protocol.TaskKindTCPing, CollectionSeconds: 10, ReportSeconds: 10})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "updated" || updated.Currency != "USD" || updated.TrafficResetDay == nil || *updated.TrafficResetDay != 15 {
		t.Fatalf("updated node = %#v", updated)
	}
	newToken, err := database.RotateAgentToken(ctx, node.ID)
	if err != nil || newToken == "" {
		t.Fatal(err)
	}
	if _, err := database.AuthenticateAgent(ctx, oldToken); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old token error = %v", err)
	}
	if _, err := database.AuthenticateAgent(ctx, newToken); err != nil {
		t.Fatal(err)
	}
	port := 443
	target, err := database.CreateTarget(ctx, CreateTargetParams{Name: "old", Kind: protocol.TaskKindTCPing, Host: "example.com", Port: &port, IntervalSeconds: 30, TimeoutMS: 1000})
	if err != nil {
		t.Fatal(err)
	}
	target, err = database.UpdateTarget(ctx, target.ID, UpdateTargetParams{Name: "new", Kind: protocol.TaskKindTCPing, Host: "example.org", Port: &port, IntervalSeconds: 60, TimeoutMS: 2000, Enabled: true, SortOrder: 3})
	if err != nil || target.Name != "new" {
		t.Fatalf("target=%#v err=%v", target, err)
	}
	group, err := database.CreateTargetGroup(ctx, "old", protocol.TaskKindTCPing)
	if err != nil {
		t.Fatal(err)
	}
	group, err = database.UpdateTargetGroup(ctx, group.ID, "new", protocol.TaskKindTCPing)
	if err != nil || group.Name != "new" {
		t.Fatal(err)
	}
	if err := database.AddTargetToGroup(ctx, group.ID, target.ID); err != nil {
		t.Fatal(err)
	}
	if err := database.AssignTargetGroup(ctx, node.ID, group.ID); err != nil {
		t.Fatal(err)
	}
	if err := database.RemoveTargetFromGroup(ctx, group.ID, target.ID); err != nil {
		t.Fatal(err)
	}
	if err := database.UnassignTargetGroup(ctx, node.ID, group.ID); err != nil {
		t.Fatal(err)
	}
	user, err := database.CreateUser(ctx, "auditor", "test-hash")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.LogAudit(ctx, user.ID, "update", "node", node.ID, "127.0.0.1", map[string]any{"name": "updated"}); err != nil {
		t.Fatal(err)
	}
	var audits int
	if err := database.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_log").Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("audit count=%d err=%v", audits, err)
	}
	if err := database.DeleteTargetGroup(ctx, group.ID); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteTarget(ctx, target.ID); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteNode(ctx, node.ID); err != nil {
		t.Fatal(err)
	}
}
