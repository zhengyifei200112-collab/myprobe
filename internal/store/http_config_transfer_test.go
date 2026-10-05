package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
)

func TestHTTPConfigurationTransfer(t *testing.T) {
	ctx := context.Background()
	source, err := Open(ctx, filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target, err := Open(ctx, filepath.Join(t.TempDir(), "target.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	node, _, err := source.CreateNode(ctx, CreateNodeParams{Name: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	v, err := source.SaveHTTPService(ctx, HTTPService{Name: "fixture", Enabled: true, IntervalSeconds: 30, NodeIDs: []string{node.ID}, Spec: httpcheck.Spec{URL: "http://example.com/private-target", Method: "GET", StatusCodes: []int{200}, TimeoutMS: 5000, MaxBodyBytes: 1024}})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := source.ExportConfig(ctx, time.Now())
	if err != nil || snapshot.Version != 2 || len(snapshot.HTTPServices) != 1 {
		t.Fatalf("export: %+v %v", snapshot, err)
	}
	preview, err := target.ImportConfig(ctx, snapshot, true)
	if err != nil || preview.HTTPServicesCreated != 1 || preview.AgentTokens != nil {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	if _, err = target.HTTPService(ctx, v.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("preview persisted: %v", err)
	}
	result, err := target.ImportConfig(ctx, snapshot, false)
	if err != nil || result.HTTPServicesCreated != 1 {
		t.Fatalf("import: %+v %v", result, err)
	}
	got, err := target.HTTPService(ctx, v.ID)
	if err != nil || got.Spec.URL != v.Spec.URL || got.Revision != 1 || len(got.NodeIDs) != 1 || got.NodeIDs[0] != node.ID {
		t.Fatalf("roundtrip: %+v %v", got, err)
	}
	result, err = target.ImportConfig(ctx, snapshot, false)
	if err != nil || result.HTTPServicesUpdated != 1 {
		t.Fatalf("merge: %+v %v", result, err)
	}
	got, err = target.HTTPService(ctx, v.ID)
	if err != nil || got.Revision != 2 {
		t.Fatalf("revision: %+v %v", got, err)
	}
	var epochs int
	if err = target.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM http_schedule_epochs WHERE service_id=?`, v.ID).Scan(&epochs); err != nil || epochs != 2 {
		t.Fatalf("epochs: %d %v", epochs, err)
	}
	// A missing node must roll back node edits as well as HTTP changes.
	snapshot.Nodes[0].Name = "must rollback"
	snapshot.HTTPServices[0].NodeIDs = []string{"missing-node"}
	if _, err = target.ImportConfig(ctx, snapshot, false); err == nil {
		t.Fatal("bad assignment accepted")
	}
	after, err := target.ExportConfig(ctx, time.Now())
	if err != nil || after.Nodes[0].Name != node.Name {
		t.Fatalf("partial import: %+v %v", after, err)
	}
	// Legacy snapshots remain accepted but cannot smuggle v2-only fields.
	snapshot.Version = 1
	if _, err = target.ImportConfig(ctx, snapshot, true); err == nil {
		t.Fatal("v1 accepted HTTP fields")
	}
	snapshot.HTTPServices = nil
	if _, err = target.ImportConfig(ctx, snapshot, true); err != nil {
		t.Fatalf("legacy: %v", err)
	}
	// Deleting the last assigned node leaves an unassigned service which must
	// remain portable without silently deleting its configuration.
	if err = source.DeleteNode(ctx, node.ID); err != nil {
		t.Fatal(err)
	}
	orphan, err := source.ExportConfig(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = target.ImportConfig(ctx, orphan, false); err != nil {
		t.Fatalf("orphan import: %v", err)
	}
	got, err = target.HTTPService(ctx, v.ID)
	if err != nil || len(got.NodeIDs) != 0 {
		t.Fatalf("orphan: %+v %v", got, err)
	}
}
