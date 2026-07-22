package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

func TestConfigurationExportImportDryRunAndRoundTrip(t *testing.T) {
	ctx := context.Background()
	source, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	node, _, err := source.CreateNode(ctx, CreateNodeParams{ID: "node-portable", Name: "Portable", Tags: []string{"prod"}, CountryCode: "CN"})
	if err != nil {
		t.Fatal(err)
	}
	port := 443
	target, err := source.CreateTarget(ctx, CreateTargetParams{Name: "HTTPS", Kind: protocol.TaskKindTCPing, Host: "example.com", Port: &port, IntervalSeconds: 30, TimeoutMS: 1000})
	if err != nil {
		t.Fatal(err)
	}
	group, err := source.CreateTargetGroup(ctx, "TCP", protocol.TaskKindTCPing)
	if err != nil {
		t.Fatal(err)
	}
	if err := source.AddTargetToGroup(ctx, group.ID, target.ID); err != nil {
		t.Fatal(err)
	}
	if err := source.AssignTargetGroup(ctx, node.ID, group.ID); err != nil {
		t.Fatal(err)
	}
	snapshot, err := source.ExportConfig(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Version != ConfigSnapshotVersion || len(snapshot.Nodes) != 1 || len(snapshot.GroupMembers) != 1 || len(snapshot.NodeGroups) != 1 {
		t.Fatalf("snapshot = %#v", snapshot)
	}

	destination, err := Open(ctx, filepath.Join(t.TempDir(), "destination.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()
	preview, err := destination.ImportConfig(ctx, snapshot, true)
	if err != nil {
		t.Fatal(err)
	}
	if preview.NodesCreated != 1 || len(preview.AgentTokens) != 0 {
		t.Fatalf("preview = %#v", preview)
	}
	if nodes, _ := destination.ListNodes(ctx); len(nodes) != 0 {
		t.Fatalf("dry run created nodes: %#v", nodes)
	}
	result, err := destination.ImportConfig(ctx, snapshot, false)
	if err != nil {
		t.Fatal(err)
	}
	token := result.AgentTokens[node.ID]
	if token == "" {
		t.Fatal("new node agent token was not returned")
	}
	if authenticated, err := destination.AuthenticateAgent(ctx, token); err != nil || authenticated.ID != node.ID {
		t.Fatalf("authenticate imported node = %#v, %v", authenticated, err)
	}
	second, err := destination.ImportConfig(ctx, snapshot, false)
	if err != nil {
		t.Fatal(err)
	}
	if second.NodesCreated != 0 || second.NodesUpdated != 1 || len(second.AgentTokens) != 0 {
		t.Fatalf("second import = %#v", second)
	}
	exportedAgain, err := destination.ExportConfig(ctx, snapshot.ExportedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(exportedAgain.Targets) != 1 || len(exportedAgain.TargetGroups) != 1 || len(exportedAgain.GroupMembers) != 1 || len(exportedAgain.NodeGroups) != 1 {
		t.Fatalf("round trip = %#v", exportedAgain)
	}
}
