package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
)

func TestHTTPServiceRevisionAndAtomicAssignments(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "services.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	node, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.SaveHTTPService(ctx, HTTPService{Name: "website", Enabled: true, IntervalSeconds: 60, NodeIDs: []string{node.ID}, Spec: httpcheck.Spec{URL: "https://example.com", Method: "GET", StatusCodes: []int{200}, TimeoutMS: 5000, MaxBodyBytes: 1024}})
	if err != nil {
		t.Fatal(err)
	}
	if v.Revision != 1 {
		t.Fatal(v.Revision)
	}
	assignments, err := s.ListHTTPAssignments(ctx)
	if err != nil || len(assignments) != 1 || assignments[0].Revision != v.Revision || !assignments[0].Anchor.Equal(v.UpdatedAt) || assignments[0].NodeID != node.ID {
		t.Fatalf("schedule assignments: %+v %v", assignments, err)
	}
	stale := v
	v.Name = "renamed"
	v, err = s.SaveHTTPService(ctx, v)
	if err != nil || v.Revision != 2 {
		t.Fatalf("update: %v %v", v, err)
	}
	if _, err = s.SaveHTTPService(ctx, stale); !errors.Is(err, ErrServiceConflict) {
		t.Fatalf("stale: %v", err)
	}
	invalid := v
	invalid.Name = "must rollback"
	invalid.NodeIDs = []string{node.ID, "missing-node"}
	if _, err = s.SaveHTTPService(ctx, invalid); err == nil {
		t.Fatal("missing node accepted")
	}
	got, err := s.HTTPService(ctx, v.ID)
	if err != nil || got.Name != "renamed" || got.Revision != 2 || len(got.NodeIDs) != 1 {
		t.Fatalf("rollback: %+v %v", got, err)
	}
	invalid = v
	invalid.IntervalSeconds = 30
	invalid.Spec.TimeoutMS = 30000
	if _, err = s.SaveHTTPService(ctx, invalid); err == nil {
		t.Fatal("timeout equal to interval accepted")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err = s.HTTPService(ctx, v.ID)
	if err != nil || got.Revision != 2 || got.Spec.URL != v.Spec.URL {
		t.Fatalf("reopen: %+v %v", got, err)
	}
	if _, err = s.CreateHTTPTask(ctx, v.ID, node.ID, v.UpdatedAt, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteHTTPService(ctx, v.ID, 1); !errors.Is(err, ErrServiceConflict) {
		t.Fatalf("stale delete: %v", err)
	}
	if err = s.DeleteHTTPService(ctx, v.ID, 2); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"http_service_nodes", "http_tasks", "http_schedule_epochs"} {
		var count int
		if err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE service_id=?", v.ID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("cascade %s: %d %v", table, count, err)
		}
	}
}
