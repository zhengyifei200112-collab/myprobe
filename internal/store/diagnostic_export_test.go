package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

func TestConfigurationDiagnosticCountsVersionBoundsAndPrivacy(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	private := "synthetic-private-host"
	for i := 0; i < 35; i++ {
		node, _, err := db.CreateNode(ctx, CreateNodeParams{Name: private + fmt.Sprint(i)})
		if err != nil {
			t.Fatal(err)
		}
		hello := protocol.Hello{AgentVersion: fmt.Sprintf("1.2.%d-%s", i, private), Hostname: private, OS: "linux", Architecture: "amd64", CollectionSeconds: 5, ReportSeconds: 5}
		if err = db.SaveAgentMetadata(ctx, node.ID, hello); err != nil {
			t.Fatal(err)
		}
	}
	unknown, _, err := db.CreateNode(ctx, CreateNodeParams{Name: private})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.SaveAgentMetadata(ctx, unknown.ID, protocol.Hello{AgentVersion: "203.0.113.9", Hostname: private, OS: "linux", Architecture: "amd64", CollectionSeconds: 5, ReportSeconds: 5}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = db.CreateNode(ctx, CreateNodeParams{Name: "no hello"}); err != nil {
		t.Fatal(err)
	}
	got := db.ConfigurationDiagnostic(ctx)
	if got.Status != "available" || got.Counts == nil || got.Counts.Nodes != 37 || len(got.AgentVersions) != 32 || *got.AgentVersionOverflow != 3 || *got.AgentUnrecognized != 1 || *got.AgentNoHello != 1 {
		t.Fatalf("bounded configuration evidence: %+v", got)
	}
	encoded, _ := json.Marshal(got)
	for _, value := range []string{private, unknown.ID, "203.0.113.9", "agent_token", "hostname"} {
		if strings.Contains(string(encoded), value) {
			t.Fatalf("configuration diagnostic leaked %s", value)
		}
	}
	var unchanged string
	if err = db.db.QueryRowContext(ctx, `SELECT agent_version FROM node_agent_metadata WHERE node_id=?`, unknown.ID).Scan(&unchanged); err != nil || unchanged != "203.0.113.9" {
		t.Fatal("diagnostic changed stored metadata")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	unavailable := db.ConfigurationDiagnostic(canceled)
	if unavailable.Status != "unavailable" || unavailable.Counts != nil || unavailable.AgentNoHello != nil {
		t.Fatal("failed query supplied fake zeros")
	}
}
