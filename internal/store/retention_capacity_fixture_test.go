package store

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/disk"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

const historyRawPerNode = 7*24*60*4 + 60*4 + 1
const historyMinutePerNode = 23 * 24 * 60
const historyFiveMinutePerNode = 335 * 24 * 12

var historyRetentionIndexNames = []string{
	"idx_metric_rollups_epoch_lookup", "idx_latency_rollups_epoch_lookup", "idx_traffic_rollups_epoch_lookup",
	"idx_metric_samples_retention_time", "idx_latency_samples_retention_time", "idx_metric_rollups_retention_time", "idx_latency_rollups_retention_time", "idx_traffic_rollups_retention_time",
	"idx_metric_samples_node_capture_ns", "idx_latency_samples_node_target_capture_ns",
}

// Real table density for a one-hour catch-up after a normal previous run:
// seven days at 15 seconds, 23 days at one minute, 335 days at five minutes,
// and three per-node targets at 60 seconds. SQL generation is not a live soak.
func historyCapacityFixture(t testing.TB, count int) (*Store, time.Time, []string) {
	t.Helper()
	if count != 5 && count != 50 && count != 100 {
		t.Fatal("unsupported fixture size")
	}
	root := t.TempDir()
	usage, err := disk.Usage(root)
	if err != nil || usage.Free < uint64(count)*(500<<20)+(8<<30) {
		t.Fatal("capacity fixture requires 500 MiB per node plus 8 GiB free on its disposable volume")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	s, err := Open(ctx, filepath.Join(root, "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	// Comparisons start with the original indexes and identical data layout.
	// This deliberate test-only removal is followed by the exact 041 definitions
	// in optimized variants; no production Store drops its indexes.
	for _, name := range historyRetentionIndexNames {
		if _, err := s.db.ExecContext(ctx, "DROP INDEX IF EXISTS "+name); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC().Truncate(time.Hour)
	previous := now.Add(-time.Hour)
	rawStart := previous.Add(-7 * 24 * time.Hour)
	minuteStart := previous.Add(-30 * 24 * time.Hour)
	fiveStart := previous.Add(-365 * 24 * time.Hour)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	ids := make([]string, count)
	for i := range ids {
		ids[i] = fmt.Sprintf("synthetic-history-node-%09d", i)
		if _, err := tx.ExecContext(ctx, `INSERT INTO nodes(id,name,collection_seconds,report_seconds,created_at,updated_at) VALUES(?,?,15,15,?,?)`, ids[i], "Synthetic history", formatTime(now), formatTime(now)); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		if _, err := tx.ExecContext(ctx, `INSERT INTO targets(id,name,kind,host,interval_seconds,created_at,updated_at) VALUES(?,?,'ping','example.test',60,?,?)`, fmt.Sprintf("synthetic-history-target-%07d", i), "Synthetic target", formatTime(now), formatTime(now)); err != nil {
			t.Fatal(err)
		}
	}
	report := historyCapacityReport(rawStart, 0)
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	// Every row has a complete synthetic report whose time and counters agree
	// with the scalar metric columns. The statement stays bounded in memory.
	if _, err := tx.ExecContext(ctx, `WITH RECURSIVE seq(i) AS (VALUES(0) UNION ALL SELECT i+1 FROM seq WHERE i+1<?)
        INSERT INTO metric_samples(node_id,captured_at,cpu_usage,memory_used,memory_total,disk_used,disk_total,net_rx_rate,net_tx_rate,net_rx_total,net_tx_total,report_json)
        SELECT n.id,strftime('%Y-%m-%dT%H:%M:%SZ',?+(i-1)*15,'unixepoch'),10,512,1024,1024,2048,40,20,1000+i*600,2000+i*300,
          json_set(?,'$.captured_at',strftime('%Y-%m-%dT%H:%M:%SZ',?+(i-1)*15,'unixepoch'),'$.networks[0].rx_total_bytes',1000+i*600,'$.networks[0].tx_total_bytes',2000+i*300)
        FROM nodes n CROSS JOIN seq`, historyRawPerNode, rawStart.Unix(), string(encoded), rawStart.Unix()); err != nil {
		t.Fatal(err)
	}
	t.Logf("seeded %d raw metric rows", count*historyRawPerNode)
	if _, err := tx.ExecContext(ctx, `WITH RECURSIVE seq(i) AS (VALUES(0) UNION ALL SELECT i+1 FROM seq WHERE i+1<?)
        INSERT INTO latency_samples(node_id,target_id,kind,captured_at,success,latency_ms)
        SELECT n.id,t.id,'ping',strftime('%Y-%m-%dT%H:%M:%SZ',?+i*60,'unixepoch'),1,10 FROM nodes n CROSS JOIN targets t CROSS JOIN seq`, 7*24*60+60, rawStart.Unix()); err != nil {
		t.Fatal(err)
	}
	for _, layer := range []struct {
		seconds, samples, rows int
		start                  time.Time
	}{
		{60, 4, historyMinutePerNode, minuteStart}, {300, 20, historyFiveMinutePerNode, fiveStart},
	} {
		sequence := `WITH RECURSIVE seq(i) AS (VALUES(0) UNION ALL SELECT i+1 FROM seq WHERE i+1<?) `
		if _, err := tx.ExecContext(ctx, sequence+`INSERT INTO metric_rollups(node_id,bucket_seconds,bucket_at,sample_count,cpu_sum,memory_percent_sum,disk_percent_sum,net_rx_rate_sum,net_tx_rate_sum)
            SELECT n.id,?,strftime('%Y-%m-%dT%H:%M:%SZ',?+i*?,'unixepoch'),?,?,?, ?,?,? FROM nodes n CROSS JOIN seq`, layer.rows, layer.seconds, layer.start.Unix(), layer.seconds, layer.samples, layer.samples*10, layer.samples*50, layer.samples*50, layer.samples*40, layer.samples*20); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, sequence+`INSERT INTO traffic_rollups(node_id,bucket_seconds,bucket_at,rx_bytes,tx_bytes)
            SELECT n.id,?,strftime('%Y-%m-%dT%H:%M:%SZ',?+i*?,'unixepoch'),?,? FROM nodes n CROSS JOIN seq`, layer.rows, layer.seconds, layer.start.Unix(), layer.seconds, layer.samples*600, layer.samples*300); err != nil {
			t.Fatal(err)
		}
		latencySamples := layer.seconds / 60
		if _, err := tx.ExecContext(ctx, sequence+`INSERT INTO latency_rollups(node_id,target_id,kind,bucket_seconds,bucket_at,sample_count,success_count,latency_count,latency_sum)
            SELECT n.id,t.id,'ping',?,strftime('%Y-%m-%dT%H:%M:%SZ',?+i*?,'unixepoch'),?,?,?,? FROM nodes n CROSS JOIN targets t CROSS JOIN seq`, layer.rows, layer.seconds, layer.start.Unix(), layer.seconds, latencySamples, latencySamples, latencySamples, latencySamples*10); err != nil {
			t.Fatal(err)
		}
		t.Logf("seeded %d-second layer: %d metric/traffic rows and %d latency rows", layer.seconds, count*layer.rows, count*layer.rows*3)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// Warm the real latest/traffic/report-ingress path; first-ever backfill is
	// outside the measured maintenance interval. No Agent tokens are necessary.
	for _, id := range ids {
		if err := s.SaveReport(ctx, id, historyCapacityReport(now, historyRawPerNode)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	return s, now, ids
}

func historyCapacityReport(at time.Time, sample int) protocol.Report {
	return protocol.Report{CapturedAt: at, CPU: protocol.CPUMetric{UsagePercent: 10}, Memory: protocol.MemoryMetric{TotalBytes: 1024, UsedBytes: 512}, Disks: []protocol.DiskMetric{{Mount: "synthetic", TotalBytes: 2048, UsedBytes: 1024}}, Networks: []protocol.NetworkMetric{{Interface: "synthetic", RXTotalBytes: uint64(1000 + sample*600), TXTotalBytes: uint64(2000 + sample*300), RXBytesPerS: 40, TXBytesPerS: 20}}}
}
