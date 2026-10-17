package store

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func seedHistoryBatch(t *testing.T, s *Store, now time.Time) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.db.Exec(`INSERT INTO targets(id,name,kind,host,created_at,updated_at) VALUES('target','Synthetic target','ping','example.test',?,?)`, formatTime(now), formatTime(now)); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		if _, _, err := s.CreateNode(ctx, CreateNodeParams{ID: id, Name: "Synthetic " + id}); err != nil {
			t.Fatal(err)
		}
		index := 0
		for _, age := range []time.Duration{38 * time.Minute, 18 * time.Minute, 8 * time.Minute} {
			for offset := range 24 {
				at := now.Add(-age).Add(time.Duration(offset) * 15 * time.Second)
				if offset%3 == 0 {
					at = at.Add(110 * time.Millisecond)
				}
				report := historyCapacityReport(at, index)
				// Include counter resets as well as gaps between windows.
				report.Networks[0].RXTotalBytes = uint64(index % 17 * 100)
				report.Networks[0].TXTotalBytes = uint64(index % 13 * 200)
				if err := s.SaveReport(ctx, id, report); err != nil {
					t.Fatal(err)
				}
				if _, err := s.db.Exec(`INSERT INTO latency_samples(node_id,target_id,kind,captured_at,success,latency_ms) VALUES(?,'target','ping',?,?,?)`, id, formatTime(at), index%2, index); err != nil {
					t.Fatal(err)
				}
				index++
			}
		}
	}
}

func historyRows(t *testing.T, s *Store, table string) []string {
	t.Helper()
	// These are fixed internal fixture tables. Exclude report arrival metadata,
	// which intentionally differs between independently seeded databases.
	query := `SELECT * FROM ` + table + ` ORDER BY 1,2,3,4,5`
	if table == "metric_samples" {
		query = `SELECT id,node_id,captured_at,net_rx_total,net_tx_total FROM metric_samples ORDER BY id`
	}
	rows, err := s.db.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var result []string
	for rows.Next() {
		values := make([]any, len(columns))
		ptrs := make([]any, len(columns))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		result = append(result, fmt.Sprint(values))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestHistoryBatchesMatchFullRetentionAcrossTiersAndReopen(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	policy := RetentionPolicy{Raw: 5 * time.Minute, OneMinute: 10 * time.Minute, FiveMinute: 30 * time.Minute}
	full, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer full.Close()
	path := filepath.Join(t.TempDir(), "batch.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	seedHistoryBatch(t, full, now)
	seedHistoryBatch(t, s, now)
	if err := full.ApplyRetention(ctx, now, policy); err != nil {
		t.Fatal(err)
	}
	passes := 0
	for ; passes < 200; passes++ {
		batch, err := s.RetainHistory(ctx, now, policy)
		if err != nil {
			t.Fatal(err)
		}
		if batch.RawBuckets > 1 || batch.MinuteBuckets > historyMinuteGroups || batch.RollupsDeleted > 3*historyPruneRows {
			t.Fatalf("unbounded pass: %+v", batch)
		}
		if passes == 2 {
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
		}
		if !batch.More {
			break
		}
	}
	if passes < 3 || passes == 200 {
		t.Fatalf("backlog did not drain incrementally: %d", passes)
	}
	for _, table := range []string{"metric_samples", "latency_samples", "metric_rollups", "latency_rollups", "traffic_rollups"} {
		if got, want := historyRows(t, s, table), historyRows(t, full, table); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s differs after drain\ngot %v\nwant %v", table, got, want)
		}
	}
	// Surviving anchors must not repeatedly create pending work, including
	// after a backwards clock step or an increase in retention durations.
	for _, at := range []time.Time{now, now.Add(-time.Hour)} {
		batch, err := s.RetainHistory(ctx, at, policy)
		if err != nil || batch.More || batch.RawBuckets != 0 || batch.MinuteBuckets != 0 {
			t.Fatalf("idle anchors repeated: %+v %v", batch, err)
		}
	}
	longer := RetentionPolicy{Raw: time.Hour, OneMinute: 2 * time.Hour, FiveMinute: 3 * time.Hour}
	if batch, err := s.RetainHistory(ctx, now, longer); err != nil || batch.More || batch.RawBuckets != 0 {
		t.Fatalf("longer retention repeated anchors: %+v %v", batch, err)
	}
}

func TestHistoryBatchFailureRollsBackRollupsPruningAndAnchors(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	seedHistoryBatch(t, s, now)
	before := historyRows(t, s, "metric_samples")
	if _, err := s.db.Exec(`CREATE TRIGGER fail_history_prune BEFORE DELETE ON latency_samples BEGIN SELECT RAISE(ABORT,'synthetic prune failure'); END`); err != nil {
		t.Fatal(err)
	}
	policy := RetentionPolicy{Raw: 5 * time.Minute, OneMinute: 10 * time.Minute, FiveMinute: 30 * time.Minute}
	batch, err := s.RetainHistory(ctx, now, policy)
	if err == nil || batch != (HistoryRetentionBatch{}) {
		t.Fatalf("failed batch published work: %+v %v", batch, err)
	}
	if !reflect.DeepEqual(before, historyRows(t, s, "metric_samples")) {
		t.Fatal("failed pass removed raw samples")
	}
	for _, table := range append([]string{"history_retention_anchors"}, historyRollupTables...) {
		assertCount(t, s, `SELECT COUNT(*) FROM `+table, 0)
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_history_prune`); err != nil {
		t.Fatal(err)
	}
	if batch, err := s.RetainHistory(ctx, now, policy); err != nil || batch.RawBuckets != 1 || !batch.More {
		t.Fatalf("retry did not preserve work: %+v %v", batch, err)
	}
}

func TestHistoryBatchCommitFailureAndCancellationPublishNoWork(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	seedHistoryBatch(t, s, now)
	before := historyRows(t, s, "metric_samples")
	if _, err := s.db.Exec(`CREATE TABLE history_commit_parent(id INTEGER PRIMARY KEY);
		CREATE TABLE history_commit_guard(id INTEGER REFERENCES history_commit_parent(id) DEFERRABLE INITIALLY DEFERRED);
		CREATE TRIGGER fail_history_commit AFTER INSERT ON history_retention_anchors BEGIN INSERT INTO history_commit_guard(id) VALUES(1); END`); err != nil {
		t.Fatal(err)
	}
	policy := RetentionPolicy{Raw: 5 * time.Minute, OneMinute: 10 * time.Minute, FiveMinute: 30 * time.Minute}
	if batch, err := s.RetainHistory(ctx, now, policy); err == nil || batch != (HistoryRetentionBatch{}) {
		t.Fatalf("commit failure published work: %+v %v", batch, err)
	}
	if !reflect.DeepEqual(before, historyRows(t, s, "metric_samples")) {
		t.Fatal("commit failure removed samples")
	}
	for _, table := range append([]string{"history_retention_anchors", "history_commit_guard"}, historyRollupTables...) {
		assertCount(t, s, `SELECT COUNT(*) FROM `+table, 0)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if batch, err := s.RetainHistory(canceled, now, policy); err == nil || batch != (HistoryRetentionBatch{}) {
		t.Fatalf("canceled pass published work: %+v %v", batch, err)
	}
}

func TestHistoryBatchBoundsExpiredRollupPruning(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, _, err := s.CreateNode(ctx, CreateNodeParams{ID: "a", Name: "Synthetic expiry"}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	if _, err := s.db.Exec(`WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i+1 FROM n WHERE i<2499)
		INSERT INTO traffic_rollups(node_id,bucket_seconds,bucket_at,rx_bytes,tx_bytes)
		SELECT 'a',300,strftime('%Y-%m-%dT%H:%M:%SZ',?,'+'||(i*300)||' seconds'),i,i*2 FROM n`, formatTime(now.Add(-400*24*time.Hour))); err != nil {
		t.Fatal(err)
	}
	for pass := range 26 {
		batch, err := s.RetainHistory(ctx, now, DefaultRetentionPolicy())
		want := max(0, 2500-(pass+1)*historyPruneRows)
		if err != nil || batch.More != (pass < 25) || batch.RollupsDeleted != int64(min(historyPruneRows, max(0, 2500-pass*historyPruneRows))) {
			t.Fatalf("expiry pass %d: %+v %v", pass, batch, err)
		}
		assertCount(t, s, `SELECT COUNT(*) FROM traffic_rollups`, want)
	}
}

func TestHistoryBatchMinuteSourcesSeekOnlyExpiredRange(t *testing.T) {
	s, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, item := range nodeMinuteRetentionQueries {
		rows, err := s.db.Query(`EXPLAIN QUERY PLAN `+item.query, "synthetic-node", "2026-10-01T00:05:00Z")
		if err != nil {
			t.Fatal(err)
		}
		var plan strings.Builder
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			plan.WriteString(detail + "\n")
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
		if !strings.Contains(plan.String(), "USING INDEX idx_"+item.table+"_retention_time (bucket_seconds=? AND bucket_at<?)") {
			t.Fatalf("minute source can scan retained history: %s", plan.String())
		}
	}
}

func TestHistoryBatchSplitsNodesWithoutSplittingFiveMinuteGroups(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	for i := range 17 {
		id := fmt.Sprintf("synthetic-%02d", i)
		if _, _, err := s.CreateNode(ctx, CreateNodeParams{ID: id, Name: id}); err != nil {
			t.Fatal(err)
		}
		for minute := range 5 {
			if _, err := s.db.Exec(`INSERT INTO metric_rollups(node_id,bucket_seconds,bucket_at,sample_count,cpu_sum,memory_percent_sum,disk_percent_sum,net_rx_rate_sum,net_tx_rate_sum) VALUES(?,60,?,4,68,80,0,0,0)`, id, formatTime(now.Add(-40*24*time.Hour).Add(time.Duration(minute)*time.Minute))); err != nil {
				t.Fatal(err)
			}
		}
	}
	for pass, groups := range []int64{8, 8, 1} {
		batch, err := s.RetainHistory(ctx, now, DefaultRetentionPolicy())
		if err != nil || batch.MinuteBuckets != groups || batch.More != (pass < 2) {
			t.Fatalf("node group pass %d: %+v %v", pass, batch, err)
		}
		var samples, cpu int
		if err := s.db.QueryRow(`SELECT SUM(sample_count),SUM(cpu_sum) FROM metric_rollups`).Scan(&samples, &cpu); err != nil || samples != 17*20 || cpu != 17*340 {
			t.Fatal("a visible pass lost or double-counted part of a node group", samples, cpu, err)
		}
		assertCount(t, s, `SELECT COUNT(*) FROM metric_rollups WHERE bucket_seconds=300 AND sample_count!=20`, 0)
	}
}

func TestHistoryBatchMigrationRollsBackWithoutBackfillingRawHistory(t *testing.T) {
	s := preNodeBatchIntegrationStore(t, false)
	ctx := context.Background()
	for _, name := range []string{"046_node_batches.sql", "047_node_timeline_indexes.sql", "048_certificate_runtime_indexes.sql"} {
		body, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(string(body)); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)`, name, nowText()); err != nil {
			t.Fatal(err)
		}
	}
	seedHistoryBatch(t, s, time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC))
	before := historyRows(t, s, "metric_samples")
	if _, err := s.db.Exec(`CREATE TRIGGER reject049 BEFORE INSERT ON schema_migrations WHEN NEW.version='049_history_retention_anchors.sql' BEGIN SELECT RAISE(ABORT,'synthetic receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err == nil {
		t.Fatal("failed anchor migration reported success")
	}
	assertCount(t, s, `SELECT COUNT(*) FROM sqlite_master WHERE name='history_retention_anchors'`, 0)
	assertCount(t, s, `SELECT COUNT(*) FROM schema_migrations WHERE version='049_history_retention_anchors.sql'`, 0)
	if !reflect.DeepEqual(before, historyRows(t, s, "metric_samples")) {
		t.Fatal("failed migration modified raw history")
	}
	if _, err := s.db.Exec(`DROP TRIGGER reject049`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	assertCount(t, s, `SELECT COUNT(*) FROM history_retention_anchors`, 0)
	assertCount(t, s, `SELECT COUNT(*) FROM schema_migrations WHERE version='049_history_retention_anchors.sql'`, 1)
	if !reflect.DeepEqual(before, historyRows(t, s, "metric_samples")) {
		t.Fatal("anchor migration backfilled or modified raw history")
	}
}
