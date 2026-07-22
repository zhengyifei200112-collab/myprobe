package store

import (
	"context"
	"testing"
	"time"

	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

func TestRetentionBuildsBothRollupsAndKeepsHistoryQueryable(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	node, _, err := database.CreateNode(ctx, CreateNodeParams{Name: "retention"})
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	for index, age := range []time.Duration{40 * 24 * time.Hour, 10 * 24 * time.Hour, 24 * time.Hour} {
		for offset := 0; offset < 2; offset++ {
			report := protocol.Report{
				CapturedAt: now.Add(-age).Add(time.Duration(offset) * 10 * time.Second),
				CPU:        protocol.CPUMetric{UsagePercent: float64(10 + index*20)},
				Memory:     protocol.MemoryMetric{TotalBytes: 100, UsedBytes: uint64(20 + index*10)},
				Networks:   []protocol.NetworkMetric{{Interface: "eth0", RXTotalBytes: uint64(index*100 + offset*10), TXTotalBytes: uint64(index*200 + offset*20)}},
			}
			if err := database.SaveReport(ctx, node.ID, report); err != nil {
				t.Fatal(err)
			}
		}
	}

	if err := database.ApplyRetention(ctx, now, DefaultRetentionPolicy()); err != nil {
		t.Fatal(err)
	}
	assertCount(t, database, "SELECT COUNT(*) FROM metric_rollups WHERE bucket_seconds=300", 1)
	assertCount(t, database, "SELECT COUNT(*) FROM metric_rollups WHERE bucket_seconds=60", 1)
	assertCount(t, database, "SELECT COUNT(*) FROM metric_samples", 3)

	points, err := database.MetricHistory(ctx, node.ID, now.Add(-60*24*time.Hour), 24*3600)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) != 3 {
		t.Fatalf("history point count = %d, want 3", len(points))
	}
	traffic, err := database.TrafficHistory(ctx, node.ID, now.Add(-60*24*time.Hour), now, 24*3600)
	if err != nil {
		t.Fatal(err)
	}
	if len(traffic) == 0 {
		t.Fatal("traffic history was lost during retention")
	}

	if err := database.ApplyRetention(ctx, now, DefaultRetentionPolicy()); err != nil {
		t.Fatal(err)
	}
	assertCount(t, database, "SELECT COUNT(*) FROM metric_rollups WHERE bucket_seconds=300", 1)
	assertCount(t, database, "SELECT COUNT(*) FROM metric_rollups WHERE bucket_seconds=60", 1)
	assertCount(t, database, "SELECT sample_count FROM metric_rollups WHERE bucket_seconds=300", 2)
	assertCount(t, database, "SELECT sample_count FROM metric_rollups WHERE bucket_seconds=60", 2)
}

func TestRetentionRejectsUnorderedDurationsWithoutMutation(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.ApplyRetention(ctx, time.Now().UTC(), RetentionPolicy{Raw: 2 * time.Hour, OneMinute: time.Hour, FiveMinute: 3 * time.Hour}); err == nil {
		t.Fatal("unordered retention policy was accepted")
	}
}

func TestIncrementalTrafficRollupUsesRetainedCounterAnchor(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	node, _, err := database.CreateNode(ctx, CreateNodeParams{Name: "traffic-anchor"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	for offset, total := range []uint64{100, 110} {
		report := protocol.Report{CapturedAt: now.Add(-10 * 24 * time.Hour).Add(time.Duration(offset) * 10 * time.Second), Networks: []protocol.NetworkMetric{{Interface: "eth0", RXTotalBytes: total}}}
		if err := database.SaveReport(ctx, node.ID, report); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.ApplyRetention(ctx, now, DefaultRetentionPolicy()); err != nil {
		t.Fatal(err)
	}
	for offset, total := range []uint64{300, 320} {
		report := protocol.Report{CapturedAt: now.Add(-6 * 24 * time.Hour).Add(time.Duration(offset) * 10 * time.Second), Networks: []protocol.NetworkMetric{{Interface: "eth0", RXTotalBytes: total}}}
		if err := database.SaveReport(ctx, node.ID, report); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.ApplyRetention(ctx, now.Add(2*24*time.Hour), DefaultRetentionPolicy()); err != nil {
		t.Fatal(err)
	}
	var delta int64
	if err := database.db.QueryRow(`SELECT rx_bytes FROM traffic_rollups WHERE node_id=? AND bucket_seconds=60 AND bucket_at=?`, node.ID, formatTime(now.Add(-6*24*time.Hour))).Scan(&delta); err != nil {
		t.Fatal(err)
	}
	if delta != 210 {
		t.Fatalf("incremental traffic delta = %d, want 210", delta)
	}
}

func assertCount(t *testing.T, database *Store, query string, want int) {
	t.Helper()
	var got int
	if err := database.db.QueryRow(query).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("%s = %d, want %d", query, got, want)
	}
}
