package agentclient

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/disk"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

type jointHistorySeed struct {
	BacklogHours                                                                    int
	LastLatencyID                                                                   int64
	Mode                                                                            string
	Anchor, SeedFinishedAt                                                          time.Time
	SeedSeconds                                                                     float64
	MetricRows, LatencyRows, MetricRollupRows, TrafficRollupRows, LatencyRollupRows int64
	LastMetricID                                                                    int64
	DatabaseBytes                                                                   int64
	CounterOffset                                                                   uint64
}

// Populate the actual workload database, retaining every production index.
// SQL-generated history is setup data, never counted as live Agent reports.
func seedJointHistory(t *testing.T, ctx context.Context, path string, database *store.Store, ids []string, backlogHours int) jointHistorySeed {
	t.Helper()
	if len(ids) != 5 && len(ids) != 50 && len(ids) != 100 {
		t.Fatal("dense joint history requires 5/50/100 nodes")
	}
	usage, err := disk.Usage(t.TempDir())
	if err != nil || usage.Free < uint64(len(ids))*((500<<20)+uint64(backlogHours)*(1<<20))+(8<<30) {
		t.Fatal("dense history requires 500 MiB per node plus 1 MiB per backlog hour/node and 8 GiB free on the fixture volume")
	}
	started := time.Now()
	anchor := jointHistoryAnchor(started, backlogHours)
	origin := anchor.Add(-365 * 24 * time.Hour)
	rawStart := anchor.Add(-7 * 24 * time.Hour)
	result := jointHistorySeed{Mode: "dense-year", Anchor: anchor, BacklogHours: backlogHours}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys=ON"); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "CREATE TEMP TABLE joint_seed_nodes(id TEXT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, "INSERT INTO joint_seed_nodes VALUES(?)", id); err != nil {
			t.Fatal(err)
		}
	}
	// Counters use one year-wide origin, so historical layers and live reports
	// join without an artificial counter reset at the setup boundary.
	template := protocol.Report{CapturedAt: rawStart, CPU: protocol.CPUMetric{UsagePercent: 17, LogicalCores: 2}, Memory: protocol.MemoryMetric{TotalBytes: 2 << 30, UsedBytes: 300 << 20, UsagePercent: 14.65}, Networks: []protocol.NetworkMetric{{Interface: "synthetic0"}}}
	raw, _ := json.Marshal(template)
	const sequence = `WITH RECURSIVE seq(i) AS (VALUES(0) UNION ALL SELECT i+1 FROM seq WHERE i+1<?) `
	const metricInsert = `INSERT INTO metric_samples(node_id,captured_at,cpu_usage,memory_used,memory_total,disk_used,disk_total,net_rx_rate,net_tx_rate,net_rx_total,net_tx_total,report_json)
 SELECT n.id,strftime('%Y-%m-%dT%H:%M:%SZ',?+i*15,'unixepoch'),17,314572800,2147483648,0,0,0,0,
 ((?+i*15-?)/15+1)*1000,((?+i*15-?)/15+1)*2000,
 json_set(?,'$.captured_at',strftime('%Y-%m-%dT%H:%M:%SZ',?+i*15,'unixepoch'),
 '$.networks[0].rx_total_bytes',((?+i*15-?)/15+1)*1000,'$.networks[0].tx_total_bytes',((?+i*15-?)/15+1)*2000)
 FROM joint_seed_nodes n CROSS JOIN seq`
	insertMetrics := func(q interface {
		ExecContext(context.Context, string, ...any) (sql.Result, error)
	}, first time.Time, count int) {
		t.Helper()
		if count <= 0 {
			return
		}
		_, err := q.ExecContext(ctx, sequence+metricInsert, count, first.Unix(), first.Unix(), origin.Unix(), first.Unix(), origin.Unix(), string(raw), first.Unix(), first.Unix(), origin.Unix(), first.Unix(), origin.Unix())
		if err != nil {
			t.Fatalf("seed raw metrics: %v", err)
		}
	}
	insertMetrics(tx, rawStart.Add(-15*time.Second), 7*24*60*4+1)
	t.Logf("dense history seeded %d raw metric rows", len(ids)*(7*24*60*4+1))
	if _, err := tx.ExecContext(ctx, sequence+`INSERT INTO latency_samples(node_id,target_id,kind,captured_at,success,latency_ms)
 SELECT n.id,t.id,t.kind,strftime('%Y-%m-%dT%H:%M:%SZ',?+i*60,'unixepoch'),1,10
 FROM joint_seed_nodes n CROSS JOIN targets t CROSS JOIN seq`, 7*24*60, rawStart.Unix()); err != nil {
		t.Fatal(err)
	}
	for _, layer := range []struct {
		seconds, rows int
		start         time.Time
	}{{60, 23 * 24 * 60, anchor.Add(-30 * 24 * time.Hour)}, {300, 335 * 24 * 12, origin}} {
		samples := layer.seconds / 15
		if _, err := tx.ExecContext(ctx, sequence+`INSERT INTO metric_rollups(node_id,bucket_seconds,bucket_at,sample_count,cpu_sum,memory_percent_sum,disk_percent_sum,net_rx_rate_sum,net_tx_rate_sum)
 SELECT n.id,?,strftime('%Y-%m-%dT%H:%M:%SZ',?+i*?,'unixepoch'),?,?,?,0,0,0 FROM joint_seed_nodes n CROSS JOIN seq`, layer.rows, layer.seconds, layer.start.Unix(), layer.seconds, samples, samples*17, float64(samples)*300/2048*100); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, sequence+`INSERT INTO traffic_rollups(node_id,bucket_seconds,bucket_at,rx_bytes,tx_bytes)
 SELECT n.id,?,strftime('%Y-%m-%dT%H:%M:%SZ',?+i*?,'unixepoch'),?,? FROM joint_seed_nodes n CROSS JOIN seq`, layer.rows, layer.seconds, layer.start.Unix(), layer.seconds, samples*1000, samples*2000); err != nil {
			t.Fatal(err)
		}
		latencySamples := layer.seconds / 60
		if _, err := tx.ExecContext(ctx, sequence+`INSERT INTO latency_rollups(node_id,target_id,kind,bucket_seconds,bucket_at,sample_count,success_count,latency_count,latency_sum)
 SELECT n.id,t.id,t.kind,?,strftime('%Y-%m-%dT%H:%M:%SZ',?+i*?,'unixepoch'),?,?,?,?
 FROM joint_seed_nodes n CROSS JOIN targets t CROSS JOIN seq`, layer.rows, layer.seconds, layer.start.Unix(), layer.seconds, latencySamples, latencySamples, latencySamples, latencySamples*10); err != nil {
			t.Fatal(err)
		}
		t.Logf("dense history seeded %d-second layer: %d metric/traffic and %d latency buckets", layer.seconds, len(ids)*layer.rows, len(ids)*layer.rows*3)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// Setup time is real. Fill its elapsed tail instead of leaving a gap between
	// the fixed history anchor and the subsequently started live workload.
	finished := time.Now().UTC().Truncate(15 * time.Second)
	insertMetrics(db, anchor, int(finished.Sub(anchor)/(15*time.Second)))
	tailMinutes := int((finished.Sub(anchor) + time.Minute - 1) / time.Minute)
	if _, err := db.ExecContext(ctx, sequence+`INSERT INTO latency_samples(node_id,target_id,kind,captured_at,success,latency_ms)
 SELECT n.id,t.id,t.kind,strftime('%Y-%m-%dT%H:%M:%SZ',?+i*60,'unixepoch'),1,10
 FROM joint_seed_nodes n CROSS JOIN targets t CROSS JOIN seq WHERE i<?`, max(1, tailMinutes), anchor.Unix(), tailMinutes); err != nil {
		t.Fatal(err)
	}
	result.CounterOffset = uint64(finished.Sub(origin)/(15*time.Second)) + 1
	prime := template
	prime.CapturedAt = finished
	prime.Networks = []protocol.NetworkMetric{{Interface: "synthetic0", RXTotalBytes: result.CounterOffset * 1000, TXTotalBytes: result.CounterOffset * 2000}}
	for _, id := range ids {
		if err := database.SaveReport(ctx, id, prime); err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []struct {
		name   string
		target *int64
	}{{"metric_samples", &result.MetricRows}, {"latency_samples", &result.LatencyRows}, {"metric_rollups", &result.MetricRollupRows}, {"traffic_rollups", &result.TrafficRollupRows}, {"latency_rollups", &result.LatencyRollupRows}} {
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table.name).Scan(table.target); err != nil {
			t.Fatal(err)
		}
	}
	if result.MetricRollupRows != int64(len(ids)*(23*24*60+335*24*12)) || result.TrafficRollupRows != result.MetricRollupRows || result.LatencyRollupRows != result.MetricRollupRows*3 {
		t.Fatal("dense history rollup count mismatch")
	}
	expectedMetricRows := int64(len(ids) * (7*24*60*4 + 1 + int(finished.Sub(anchor)/(15*time.Second)) + 1))
	if result.MetricRows != expectedMetricRows || result.LatencyRows != int64(len(ids)*3*(7*24*60+tailMinutes)) {
		t.Fatal("dense raw history row count mismatch")
	}
	if err := db.QueryRowContext(ctx, "SELECT COALESCE(MAX(id),0) FROM metric_samples").Scan(&result.LastMetricID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT COALESCE(MAX(id),0) FROM latency_samples").Scan(&result.LastLatencyID); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	result.DatabaseBytes = info.Size()
	result.SeedFinishedAt = time.Now().UTC()
	result.SeedSeconds = time.Since(started).Seconds()
	t.Logf("dense history complete: nodes=%d database_bytes=%d setup_seconds=%.2f", len(ids), result.DatabaseBytes, result.SeedSeconds)
	return result
}

func (s jointHistorySeed) String() string {
	return fmt.Sprintf("%s/%d metrics/%d latency", s.Mode, s.MetricRows, s.LatencyRows)
}

// Check complete historical buckets; the range edge and live edge are partial.
func validateJointHistory(raw []byte, rangeName string, anchor, requestedAt time.Time) error {
	var body struct {
		Range   string                      `json:"range"`
		Bucket  int                         `json:"bucket_seconds"`
		Metrics []store.MetricHistoryPoint  `json:"metrics"`
		Latency []store.LatencyHistoryPoint `json:"latency"`
		Traffic []store.TrafficHistoryPoint `json:"traffic"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return err
	}
	days, seconds := 30, 3600
	if rangeName == "1y" {
		days, seconds = 365, 43200
	}
	if body.Range != rangeName || body.Bucket != seconds {
		return fmt.Errorf("wrong history range/bucket")
	}
	step := time.Duration(seconds) * time.Second
	first := requestedAt.Add(-time.Duration(days) * 24 * time.Hour).Truncate(step).Add(2 * step)
	last := anchor.Truncate(step).Add(-step)
	metrics := make(map[int64]store.MetricHistoryPoint)
	traffic := make(map[int64]store.TrafficHistoryPoint)
	latency := make(map[int64]map[string]bool)
	for _, p := range body.Metrics {
		if _, exists := metrics[p.Time.Unix()]; exists {
			return fmt.Errorf("duplicate metric bucket")
		}
		metrics[p.Time.Unix()] = p
	}
	for _, p := range body.Traffic {
		if _, exists := traffic[p.Time.Unix()]; exists {
			return fmt.Errorf("duplicate traffic bucket")
		}
		traffic[p.Time.Unix()] = p
	}
	for _, p := range body.Latency {
		if p.Time.Before(first) || p.Time.After(last) {
			continue
		}
		if p.Kind != "tcping" || p.LatencyMS == nil || math.Abs(*p.LatencyMS-10) > 1e-9 || p.SuccessRate != 100 {
			return fmt.Errorf("incorrect historical latency")
		}
		if latency[p.Time.Unix()] == nil {
			latency[p.Time.Unix()] = make(map[string]bool)
		}
		if latency[p.Time.Unix()][p.TargetID] {
			return fmt.Errorf("duplicate latency bucket")
		}
		latency[p.Time.Unix()][p.TargetID] = true
	}
	for at := first; !at.After(last); at = at.Add(step) {
		p, ok := metrics[at.Unix()]
		if !ok || math.Abs(p.CPUPercent-17) > 1e-9 || math.Abs(p.MemoryPercent-300.0/2048*100) > 1e-9 {
			return fmt.Errorf("missing/incorrect metric bucket at %s", at)
		}
		if len(latency[at.Unix()]) != 3 {
			return fmt.Errorf("missing latency targets at %s", at)
		}
		current, ok := traffic[at.Unix()]
		previous, previousOK := traffic[at.Unix()-int64(seconds)]
		if !ok || !previousOK || current.RXBytes-previous.RXBytes != uint64(seconds/15)*1000 || current.TXBytes-previous.TXBytes != uint64(seconds/15)*2000 || current.Total != current.RXBytes+current.TXBytes {
			return fmt.Errorf("missing/incorrect traffic bucket at %s", at)
		}
	}
	return nil
}

func checkJointHistoryNodes(t *testing.T, client *http.Client, base string, ids []string, anchor time.Time) {
	t.Helper()
	for _, id := range ids {
		for _, rangeName := range []string{"30d", "1y"} {
			response, err := client.Get(base + "/api/v1/public/nodes/" + id + "/history?range=" + rangeName)
			if err != nil {
				t.Fatal(err)
			}
			var raw json.RawMessage
			err = json.NewDecoder(response.Body).Decode(&raw)
			response.Body.Close()
			if err != nil || response.StatusCode != 200 {
				t.Fatalf("dense history preflight: status=%d error=%v", response.StatusCode, err)
			}
			if err := validateJointHistory(raw, rangeName, anchor, time.Now().UTC()); err != nil {
				t.Fatalf("dense history %s preflight: %v", rangeName, err)
			}
		}
	}
}
