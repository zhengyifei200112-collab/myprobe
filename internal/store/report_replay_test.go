package store

import (
	"context"
	"encoding/json"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"testing"
	"time"
)

func TestCachedReplayDeduplicatesAndDistinctLateReportRemains(t *testing.T) {
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
	for _, offset := range []time.Duration{0, 0, -time.Second, time.Second, time.Second, 0, -time.Second} {
		report := protocol.Report{CapturedAt: at.Add(offset), Networks: []protocol.NetworkMetric{{Interface: "eth0", RXTotalBytes: uint64(100 + offset/time.Second*10)}}}
		if err := s.SaveReport(ctx, node.ID, report); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM metric_samples WHERE node_id=?", node.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("stored %d samples, want three distinct captures including the late report", count)
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
	conflicting := latest
	conflicting.CPU.UsagePercent = 99
	if err := s.SaveReport(ctx, node.ID, conflicting); err == nil {
		t.Fatal("different payload at an existing capture time was acknowledged")
	}
	var lastRX, cycleRX int64
	var lastCaptured string
	if err := s.db.QueryRowContext(ctx, "SELECT last_captured_at,last_rx_total,cycle_rx_bytes FROM traffic_state WHERE node_id=?", node.ID).Scan(&lastCaptured, &lastRX, &cycleRX); err != nil {
		t.Fatal(err)
	}
	if lastCaptured != formatTime(latest.CapturedAt) || lastRX != 110 || cycleRX != 10 {
		t.Fatalf("late/replayed report changed traffic: capture=%s last=%d cycle=%d", lastCaptured, lastRX, cycleRX)
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
