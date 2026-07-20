package store

import (
	"context"
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
}
