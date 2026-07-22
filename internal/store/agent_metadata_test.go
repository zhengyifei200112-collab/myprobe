package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

func TestAgentMetadataPersistsWithoutMachineID(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "metadata.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	node, _, err := database.CreateNode(ctx, CreateNodeParams{Name: "metadata"})
	if err != nil {
		t.Fatal(err)
	}
	hello := protocol.Hello{AgentVersion: "1.2.3", Hostname: "vps-01", MachineID: "private-machine-id", OS: "linux", Platform: "debian", PlatformVersion: "13", KernelVersion: "6.12", Architecture: "amd64", Capabilities: []string{"metrics.v1"}, CollectionSeconds: 5, ReportSeconds: 5}
	if err := database.SaveAgentMetadata(ctx, node.ID, hello); err != nil {
		t.Fatal(err)
	}
	nodes, err := database.ListNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].Agent == nil || nodes[0].Agent.Platform != "debian" || nodes[0].Agent.AgentVersion != "1.2.3" {
		t.Fatalf("nodes = %#v", nodes)
	}
	public, err := database.ListPublicNodes(ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(public) != 1 || public[0].Node.Agent == nil || public[0].Node.Agent.Hostname != "" || public[0].Node.Agent.AgentVersion != "" || len(public[0].Node.Agent.Capabilities) != 0 || public[0].Node.Agent.Platform != "debian" {
		t.Fatalf("public metadata = %#v", public)
	}
	var machineIDCount int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM node_agent_metadata WHERE hostname='private-machine-id' OR platform='private-machine-id'`).Scan(&machineIDCount); err != nil || machineIDCount != 0 {
		t.Fatalf("machine ID persisted: count=%d err=%v", machineIDCount, err)
	}
}

func TestCommercialStatusExactBoundaries(t *testing.T) {
	now := time.Date(2028, 2, 28, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		expiry  time.Time
		expired bool
		days    int
	}{
		{"exact", now, false, 0},
		{"one-second-future", now.Add(time.Second), false, 1},
		{"one-day-future", now.Add(24 * time.Hour), false, 1},
		{"leap-boundary", time.Date(2028, 2, 29, 12, 0, 1, 0, time.UTC), false, 2},
		{"one-second-past", now.Add(-time.Second), true, 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := ComputeCommercialStatus(&test.expiry, now)
			if value.Expired != test.expired || value.Days != test.days {
				t.Fatalf("status = %#v", value)
			}
		})
	}
	if value := ComputeCommercialStatus(nil, now); value != nil {
		t.Fatalf("nil expiry status = %#v", value)
	}
}
