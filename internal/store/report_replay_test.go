package store

import (
	"context"
	"encoding/json"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"testing"
	"time"
)

func TestCachedAndOlderReportsDoNotDuplicateHistory(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	node, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "cached"})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	for _, offset := range []time.Duration{0, 0, -time.Second, time.Second, time.Second} {
		report := protocol.Report{CapturedAt: at.Add(offset), Networks: []protocol.NetworkMetric{{Interface: "eth0", RXTotalBytes: 100}}}
		if err := s.SaveReport(ctx, node.ID, report); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM metric_samples WHERE node_id=?", node.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("stored %d samples, want 2", count)
	}
	var raw string
	if err := s.db.QueryRowContext(ctx, "SELECT report_json FROM metric_latest WHERE node_id=?", node.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var latest protocol.Report
	if err := json.Unmarshal([]byte(raw), &latest); err != nil {
		t.Fatal(err)
	}
	if !latest.CapturedAt.Equal(at.Add(time.Second)) {
		t.Fatal("latest sample moved backward")
	}
	old := at.Add(-time.Hour).Format(time.RFC3339Nano)
	if _, err := s.db.ExecContext(ctx, "UPDATE nodes SET last_seen_at=? WHERE id=?", old, node.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveReport(ctx, node.ID, latest); err != nil {
		t.Fatal(err)
	}
	var seen string
	if err := s.db.QueryRowContext(ctx, "SELECT last_seen_at FROM nodes WHERE id=?", node.ID).Scan(&seen); err != nil {
		t.Fatal(err)
	}
	if seen == old {
		t.Fatal("cached report did not refresh liveness")
	}
}
