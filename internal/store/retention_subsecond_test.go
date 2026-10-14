package store

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"
)

func TestCapturedSortKeyPreservesOriginalTimestampAndPayload(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	node, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "Synthetic UTC key"})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 10, 15, 12, 0, 0, 0, time.FixedZone("synthetic", 8*3600))
	for i, ns := range []int{0, 1, 10, 100, 100000000, 110000000, 111000000, 111000001, 999999999} {
		at := base.Add(time.Duration(ns))
		report := historyCapacityReport(at, i)
		if err := s.SaveReport(ctx, node.ID, report); err != nil {
			t.Fatal(err)
		}
		var original, key, payload string
		if err := s.db.QueryRow(`SELECT captured_at,captured_sort_ns,report_json FROM metric_samples ORDER BY id DESC LIMIT 1`).Scan(&original, &key, &payload); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		if original != formatTime(at) || key != formatCapturedTime(at) || payload != string(encoded) {
			t.Fatal("UTC sort key rewrote original data or changed nanoseconds")
		}
	}
}

func TestRetentionSubsecondBoundaryAndBucket(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	node, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "Synthetic subsecond"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-DefaultRetentionPolicy().Raw)
	for i, at := range []time.Time{cutoff.Add(-time.Nanosecond), cutoff, cutoff.Add(time.Nanosecond)} {
		report := historyCapacityReport(at, i)
		report.CPU.UsagePercent = []float64{13, 23, 37}[i]
		if err := s.SaveReport(ctx, node.ID, report); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ApplyRetention(ctx, now, DefaultRetentionPolicy()); err != nil {
		t.Fatal(err)
	}
	assertCount(t, s, `SELECT COUNT(*) FROM metric_samples`, 3)
	var bucket string
	var count int
	var cpu float64
	if err := s.db.QueryRow(`SELECT bucket_at,sample_count,cpu_sum FROM metric_rollups`).Scan(&bucket, &count, &cpu); err != nil {
		t.Fatal(err)
	}
	if bucket != formatTime(cutoff.Add(-time.Minute)) || count != 1 || cpu != 13 {
		t.Fatal("subsecond sample crossed its true minute boundary")
	}
}

func TestSubsecondRawHistoryAndTrafficOrder(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	node, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "Synthetic ordering"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	at := now.Add(-10 * 24 * time.Hour)
	for i, offset := range []time.Duration{0, 100 * time.Millisecond, 110 * time.Millisecond} {
		report := historyCapacityReport(at.Add(offset), i)
		report.Networks[0].RXTotalBytes = uint64(100 + i*10)
		report.CPU.UsagePercent = []float64{13, 23, 37}[i]
		if err := s.SaveReport(ctx, node.ID, report); err != nil {
			t.Fatal(err)
		}
	}
	history, err := s.MetricHistory(ctx, node.ID, at, 1)
	if err != nil || len(history) != 1 || math.Abs(history[0].CPUPercent-73.0/3) > 1e-9 {
		t.Fatal("history omitted samples sharing its start second")
	}
	traffic, err := s.TrafficHistory(ctx, node.ID, at, at.Add(time.Second), 1)
	if err != nil || len(traffic) != 1 || traffic[0].RXBytes != 20 {
		t.Fatal("fractional timestamp order invented a counter reset")
	}
	if err := s.ApplyRetention(ctx, now, DefaultRetentionPolicy()); err != nil {
		t.Fatal(err)
	}
	var total int64
	if err := s.db.QueryRow(`SELECT SUM(rx_bytes) FROM traffic_rollups`).Scan(&total); err != nil || total != 20 {
		t.Fatal("retention changed the true raw counter delta")
	}
	var anchor string
	if err := s.db.QueryRow(`SELECT captured_at FROM metric_samples`).Scan(&anchor); err != nil || anchor != formatTime(at.Add(110*time.Millisecond)) {
		t.Fatal("retention kept the wrong fractional counter anchor")
	}
}

func TestSubsecondLatencyRangeLatestAndRetention(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	node, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "Synthetic latency"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	if _, err := s.db.Exec(`INSERT INTO targets(id,name,kind,host,created_at,updated_at) VALUES('target','Synthetic target','ping','example.test',?,?)`, formatTime(now), formatTime(now)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO node_targets(node_id,target_id) VALUES(?,'target')`, node.ID); err != nil {
		t.Fatal(err)
	}
	cutoff := now.Add(-DefaultRetentionPolicy().Raw)
	for i, at := range []time.Time{cutoff.Add(-time.Nanosecond), cutoff, cutoff.Add(100 * time.Millisecond), cutoff.Add(110 * time.Millisecond)} {
		if _, err := s.db.Exec(`INSERT INTO latency_samples(node_id,target_id,kind,captured_at,success,latency_ms) VALUES(?,'target','ping',?,1,?)`, node.ID, formatTime(at), 10+i); err != nil {
			t.Fatal(err)
		}
	}
	latest, err := s.ListLatestLatency(ctx, node.ID)
	if err != nil || len(latest) != 1 || latest[0].UpdatedAt == nil || !latest[0].UpdatedAt.Equal(cutoff.Add(110*time.Millisecond)) {
		t.Fatal("latest latency chose an earlier fraction")
	}
	history, err := s.LatencyHistory(ctx, node.ID, cutoff, 1)
	if err != nil || len(history) != 1 || history[0].LatencyMS == nil || *history[0].LatencyMS != 12 {
		t.Fatal("latency history omitted the start-second fractions")
	}
	if err := s.ApplyRetention(ctx, now, DefaultRetentionPolicy()); err != nil {
		t.Fatal(err)
	}
	assertCount(t, s, `SELECT COUNT(*) FROM latency_samples`, 3)
	var bucket string
	var samples int
	if err := s.db.QueryRow(`SELECT bucket_at,sample_count FROM latency_rollups`).Scan(&bucket, &samples); err != nil || bucket != formatTime(cutoff.Add(-time.Minute)) || samples != 1 {
		t.Fatal("latency subsecond rounding moved a sample into the wrong minute")
	}
}
