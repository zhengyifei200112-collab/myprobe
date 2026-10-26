package store

import (
	"context"
	"database/sql"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

func previousRawHistoryQuery(query string) string {
	query = strings.ReplaceAll(query, "INDEXED BY idx_metric_samples_history_cover", "INDEXED BY idx_metric_samples_node_capture_ns")
	return strings.ReplaceAll(query, "INDEXED BY idx_latency_samples_history_cover", "INDEXED BY idx_latency_samples_node_target_capture_ns")
}

func rawHistoryRows(t *testing.T, s *Store, query string, args ...any) [][]any {
	t.Helper()
	rows, err := s.db.Query(query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var result [][]any
	for rows.Next() {
		values, pointers := make([]any, len(columns)), make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		result = append(result, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func rawCoveringFixture(t *testing.T) (*Store, Node, time.Time) {
	t.Helper()
	s, node, target := historyFixture(t)
	s.db.SetMaxOpenConns(1)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, row := range []struct {
		at           string
		cpu, latency float64
		rx           int64
	}{
		{"2026-01-01T00:00:00.1Z", 100, 1000, 40},
		{"2026-01-01T00:00:00.1Z", 5e-15, 5e-14, 80},
		{"2026-01-01T00:00:00.1Z", 5e-15, 5e-14, 30},
		{"2026-01-01T00:00:00Z", 8, 10, 100},
		{"2026-01-01T00:00:00.0Z", 9, 20, 10},
		{"2026-01-01T00:00:00.000Z", 10, 30, 80},
		{"2026-01-01T00:00:00.9Z", 17, 40, 15},
		{"2026-01-01T00:00:01Z", 18, 50, 25},
		{formatTime(base.Add(-time.Nanosecond)), 99, 99, 500},
		{"2026-01-01T00:00:02Z", 19, 60, -5},
	} {
		memoryTotal, diskTotal := 1024, 2048
		if i%3 == 0 {
			memoryTotal = 0
		}
		if i%2 == 0 {
			diskTotal = 0
		}
		if _, err := s.db.Exec(`INSERT INTO metric_samples(node_id,captured_at,cpu_usage,memory_used,memory_total,disk_used,disk_total,net_rx_rate,net_tx_rate,net_rx_total,net_tx_total,report_json) VALUES(?,?,?,512,?,1024,?,?,?, ?,?,'{}')`, node.ID, row.at, row.cpu, memoryTotal, diskTotal, row.cpu/10, row.cpu/5, row.rx, 2*row.rx); err != nil {
			t.Fatal(err)
		}
		kind, success := "tcping", 1
		var latency any = row.latency
		if i > 2 && i%2 == 1 {
			kind = "ping"
		}
		if i == 3 {
			latency = nil
		}
		if i == 4 {
			success = 0
		}
		if _, err := s.db.Exec(`INSERT INTO latency_samples(node_id,target_id,kind,captured_at,success,latency_ms) VALUES(?,?,?,?,?,?)`, node.ID, target.ID, kind, row.at, success, latency); err != nil {
			t.Fatal(err)
		}
	}
	for _, level := range []int{60, 300} {
		at := formatTime(base.Add(time.Duration(level) * time.Second))
		if _, err := s.db.Exec(`INSERT INTO metric_rollups VALUES(?,?,?,10,170,500,250,40,20)`, node.ID, level, at); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`INSERT INTO latency_rollups VALUES(?,?,'tcping',?,?,10,8,6,60)`, node.ID, target.ID, level, at); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`INSERT INTO traffic_rollups VALUES(?,?,?,500,1000)`, node.ID, level, at); err != nil {
			t.Fatal(err)
		}
	}
	other, _, err := s.CreateNode(context.Background(), CreateNodeParams{Name: "unrelated raw-covering node"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO metric_samples(node_id,captured_at,cpu_usage,memory_used,memory_total,disk_used,disk_total,net_rx_rate,net_tx_rate,net_rx_total,net_tx_total,report_json) VALUES(?,?,99,0,0,0,0,0,0,9999,9999,'{}')`, other.ID, formatTime(base)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO latency_samples(node_id,target_id,kind,captured_at,success,latency_ms) VALUES(?,?,'tcping',?,1,9999)`, other.ID, target.ID, formatTime(base)); err != nil {
		t.Fatal(err)
	}
	// Captured kinds survive later metadata/assignment changes.
	if _, err = s.db.Exec(`UPDATE targets SET name='renamed retained target',kind='ping',enabled=0 WHERE id=?`, target.ID); err != nil {
		t.Fatal(err)
	}
	return s, node, base
}

func TestRawCoveringPreservesValuesScopesAndEqualTimeOrder(t *testing.T) {
	s, node, base := rawCoveringFixture(t)
	ctx := context.Background()
	for _, query := range []string{metricHistoryQuery, latencyHistoryQuery, trafficHistoryRangeQuery} {
		if previousRawHistoryQuery(query) == query {
			t.Fatal("legacy reference did not select previous index")
		}
	}
	compare := func() {
		t.Helper()
		for _, hidden := range []bool{false, true} {
			if _, err := s.db.Exec("UPDATE nodes SET hidden=? WHERE id=?", hidden, node.ID); err != nil {
				t.Fatal(err)
			}
			for _, includeHidden := range []bool{false, true} {
				for _, window := range []struct {
					start, end time.Time
					bucket     int
				}{
					{base, base.Add(2 * time.Second), 1},
					{base.Add(100 * time.Millisecond), base.Add(100*time.Millisecond + time.Nanosecond), 1},
					{base.Add(time.Nanosecond), base.Add(2*time.Second - time.Nanosecond), 1},
					{base, base.Add(time.Hour), 60},
					{base, base.Add(30 * 24 * time.Hour), 3600},
					{base, base.Add(365 * 24 * time.Hour), 43200},
				} {
					args, err := historyQueryArgs(node.ID, window.start, window.end, window.bucket, includeHidden)
					if err != nil {
						t.Fatal(err)
					}
					for _, query := range []string{metricHistoryQuery, latencyHistoryQuery, trafficHistoryRangeQuery} {
						want := rawHistoryRows(t, s, previousRawHistoryQuery(query), args...)
						got := rawHistoryRows(t, s, query, args...)
						if !reflect.DeepEqual(got, want) {
							t.Fatalf("raw covering changed values/order: hidden=%t admin=%t start=%s bucket=%d\ngot%v\nwant%v", hidden, includeHidden, window.start, window.bucket, got, want)
						}
						if hidden && !includeHidden && len(got) != 0 {
							t.Fatal("hidden raw history exposed")
						}
					}
				}
			}
		}
	}
	compare()
	start, end := base.Add(100*time.Millisecond), base.Add(100*time.Millisecond+time.Nanosecond)
	metrics, err := s.AdminMetricHistoryRange(ctx, node.ID, start, end, 1)
	if err != nil || len(metrics) != 1 || metrics[0].CPUPercent != 100.0/3 {
		t.Fatal("equal-time metric floating sum order changed", metrics, err)
	}
	latency, err := s.AdminLatencyHistoryRange(ctx, node.ID, start, end, 1)
	if err != nil || len(latency) != 1 || latency[0].Kind != "tcping" || latency[0].LatencyMS == nil || latency[0].SuccessRate != 100 {
		t.Fatal("equal-time latency order/weights/kind changed", latency, err)
	}
	// SQLite's compensated SUM retains the two 5e-14 contributions: the
	// rounded sum is the next float above1000, before division by three.
	wantLatency := math.Nextafter(1000, math.Inf(1)) / 3
	if *latency[0].LatencyMS != wantLatency {
		t.Fatalf("compensated latency mean changed: got%.17g want%.17g", *latency[0].LatencyMS, wantLatency)
	}
	traffic, err := s.AdminTrafficHistoryRange(ctx, node.ID, start, end, 1)
	if err != nil || len(traffic) != 1 || traffic[0].RXBytes != 70 || traffic[0].TXBytes != 140 {
		t.Fatal("same-time row-ID counter resets changed", traffic, err)
	}
	// Updates must maintain both the generated seek key and covered payload.
	if _, err := s.db.Exec(`UPDATE metric_samples SET captured_at=?,cpu_usage=77,net_rx_total=12 WHERE id=1`, formatTime(base.Add(3*time.Second))); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE latency_samples SET captured_at=?,success=0,latency_ms=NULL WHERE id=1`, formatTime(base.Add(3*time.Second))); err != nil {
		t.Fatal(err)
	}
	compare()
	for _, table := range []string{"metric_samples", "latency_samples"} {
		if _, err := s.db.Exec("DELETE FROM " + table + " WHERE id=2"); err != nil {
			t.Fatal(err)
		}
	}
	compare()
}

func TestRawCoveringMigrationIsAtomicAndKeepsPopulatedRows(t *testing.T) {
	for _, failure := range []string{"receipt", "second index"} {
		t.Run(failure, func(t *testing.T) {
			s, _, _ := rawCoveringFixture(t)
			for _, query := range []string{"DROP INDEX idx_metric_samples_history_cover", "DROP INDEX idx_latency_samples_history_cover", "DELETE FROM schema_migrations WHERE version='051_raw_history_covering_reads.sql'"} {
				if _, err := s.db.Exec(query); err != nil {
					t.Fatal(err)
				}
			}
			beforeMetric := rawHistoryRows(t, s, "SELECT * FROM metric_samples ORDER BY id")
			beforeLatency := rawHistoryRows(t, s, "SELECT * FROM latency_samples ORDER BY id")
			if failure == "receipt" {
				if _, err := s.db.Exec(`CREATE TRIGGER reject051 BEFORE INSERT ON schema_migrations WHEN NEW.version='051_raw_history_covering_reads.sql' BEGIN SELECT RAISE(ABORT,'synthetic receipt failure'); END`); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := s.db.Exec(`CREATE INDEX idx_latency_samples_history_cover ON latency_samples(node_id)`); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Migrate(context.Background()); err == nil {
				t.Fatal("forced partial/receipt migration failure reported success")
			}
			assertCount(t, s, "SELECT COUNT(*) FROM sqlite_master WHERE name='idx_metric_samples_history_cover'", 0)
			assertCount(t, s, "SELECT COUNT(*) FROM schema_migrations WHERE version='051_raw_history_covering_reads.sql'", 0)
			if !reflect.DeepEqual(beforeMetric, rawHistoryRows(t, s, "SELECT * FROM metric_samples ORDER BY id")) || !reflect.DeepEqual(beforeLatency, rawHistoryRows(t, s, "SELECT * FROM latency_samples ORDER BY id")) {
				t.Fatal("failed migration rewrote raw history")
			}
			if failure == "receipt" {
				assertCount(t, s, "SELECT COUNT(*) FROM sqlite_master WHERE name='idx_latency_samples_history_cover'", 0)
				if _, err := s.db.Exec("DROP TRIGGER reject051"); err != nil {
					t.Fatal(err)
				}
			} else {
				assertCount(t, s, "SELECT COUNT(*) FROM sqlite_master WHERE name='idx_latency_samples_history_cover'", 1)
				if _, err := s.db.Exec("DROP INDEX idx_latency_samples_history_cover"); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				if err := s.Migrate(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			assertCount(t, s, "SELECT COUNT(*) FROM sqlite_master WHERE name IN ('idx_metric_samples_history_cover','idx_latency_samples_history_cover')", 2)
			assertCount(t, s, "SELECT COUNT(*) FROM schema_migrations WHERE version='051_raw_history_covering_reads.sql'", 1)
			if !reflect.DeepEqual(beforeMetric, rawHistoryRows(t, s, "SELECT * FROM metric_samples ORDER BY id")) || !reflect.DeepEqual(beforeLatency, rawHistoryRows(t, s, "SELECT * FROM latency_samples ORDER BY id")) {
				t.Fatal("successful migration rewrote raw history")
			}
		})
	}
}

// Generated sort-key columns can prevent EXPLAIN QUERY PLAN from labelling an
// index as covering. Check the actual VM's raw-table Column operations as well
// as the time seek; P1 operands of other opcodes are not necessarily cursors.
func rawHistoryTableColumns(t *testing.T, s *Store, table, query string, args []any) int {
	t.Helper()
	var rootPage int
	if err := s.db.QueryRow("SELECT rootpage FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&rootPage); err != nil || rootPage <= 0 {
		t.Fatal("raw table identity unavailable", table, err)
	}
	rows, err := s.db.Query("EXPLAIN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type instruction struct {
		op string
		p1 int
	}
	var instructions []instruction
	cursors := make(map[int]bool)
	for rows.Next() {
		var address, p1, p2, p3, p5 int
		var op string
		var p4, comment sql.NullString
		if err := rows.Scan(&address, &op, &p1, &p2, &p3, &p4, &p5, &comment); err != nil {
			t.Fatal(err)
		}
		if op == "OpenRead" && p2 == rootPage && p3 == 0 {
			cursors[p1] = true
		}
		instructions = append(instructions, instruction{op, p1})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, op := range instructions {
		if op.op == "Column" && cursors[op.p1] {
			count++
		}
	}
	return count
}

func TestRawHistoryPayloadsStayInTimeRangeIndexes(t *testing.T) {
	s, node, _ := historyFixture(t)
	end := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	for _, entry := range []struct{ name, table, query, bounds string }{
		{"metrics", "metric_samples", metricHistoryQuery, "node_id=? AND captured_sort_ns>? AND captured_sort_ns<?"},
		{"latency", "latency_samples", latencyHistoryQuery, "node_id=? AND target_id=? AND captured_sort_ns>? AND captured_sort_ns<?"},
		{"traffic", "metric_samples", trafficHistoryRangeQuery, "node_id=? AND captured_sort_ns>? AND captured_sort_ns<?"},
	} {
		t.Run(entry.name, func(t *testing.T) {
			for _, span := range []time.Duration{time.Hour, 30 * 24 * time.Hour, 365 * 24 * time.Hour} {
				args, err := historyQueryArgs(node.ID, end.Add(-span), end, 3600, false)
				if err != nil {
					t.Fatal(err)
				}
				columns := rawHistoryTableColumns(t, s, entry.table, entry.query, args)
				if columns != 0 {
					t.Fatalf("raw history payload still reads table columns: component=%s columns=%d", entry.name, columns)
				}
				rows, err := s.db.Query("EXPLAIN QUERY PLAN "+entry.query, args...)
				if err != nil {
					t.Fatal(err)
				}
				seek := false
				for rows.Next() {
					var id, parent, unused int
					var detail string
					if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
						t.Fatal(err)
					}
					seek = seek || strings.Contains(detail, entry.bounds)
				}
				err = rows.Err()
				rows.Close()
				if err != nil || !seek {
					t.Fatal("raw history lost a bounded node/time seek", entry.name, err)
				}
			}
		})
	}
}
