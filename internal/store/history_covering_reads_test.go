package store

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Seeking the right rows is insufficient on a cold, dense database if each
// retained sample still needs another B-tree lookup for its aggregate values.
// Assert the real planner's covering/range contract rather than a wall-clock
// threshold that would fluctuate with CI hardware and caches.
func TestLatencyHistoryRetainedReadsAreCovering(t *testing.T) {
	s, node, _ := historyFixture(t)
	end := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	for _, span := range []time.Duration{time.Hour, 30 * 24 * time.Hour, 365 * 24 * time.Hour} {
		args, err := historyQueryArgs(node.ID, end.Add(-span), end, 3600, false)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := s.db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+latencyHistoryQuery, args...)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		covered := false
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
			if strings.Contains(detail, "SEARCH latency_rollups USING COVERING INDEX ") &&
				strings.Contains(detail, "node_id=? AND target_id=? AND kind=? AND bucket_seconds=? AND bucket_at>? AND bucket_at<?") {
				covered = true
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !covered {
			t.Fatalf("retained latency needs table lookups instead of a covering time seek:\n%s", strings.Join(plan, "\n"))
		}
	}
}

func TestHistoryCoveringMigrationRollbackAndExistingRows(t *testing.T) {
	s, node, target := historyFixture(t)
	ctx := context.Background()
	// Simulate an already populated pre-050 database, rather than testing only
	// the empty schema used during a fresh installation.
	for _, query := range []string{
		"DROP INDEX idx_latency_rollups_history_cover",
		"DELETE FROM schema_migrations WHERE version='050_history_covering_reads.sql'",
	} {
		if _, err := s.db.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, row := range []struct {
		kind   string
		level  int
		offset time.Duration
		n, ok  int
		valid  int
		sum    float64
	}{
		{"tcping", 60, 0, 10, 8, 4, 42.5},
		{"tcping", 300, 300 * time.Second, 50, 0, 0, 0},
		{"ping", 60, 0, 3, 3, 0, 0},
	} {
		if _, err := s.db.ExecContext(ctx, "INSERT INTO latency_rollups VALUES(?,?,?,?,?,?,?,?,?)", node.ID, target.ID, row.kind, row.level, formatTime(base.Add(row.offset)), row.n, row.ok, row.valid, row.sum); err != nil {
			t.Fatal(err)
		}
	}
	args, err := historyQueryArgs(node.ID, base, base.Add(time.Hour), 3600, true)
	if err != nil {
		t.Fatal(err)
	}
	want := previousLatencyPoints(t, s, args)
	if len(want) != 2 || want[0].LatencyMS != nil || want[0].SuccessRate != 100 || want[1].LatencyMS == nil || *want[1].LatencyMS != 10.625 || want[1].SuccessRate != 100.0*8/60 {
		t.Fatalf("invalid independently weighted migration fixture: %+v", want)
	}
	if _, err := s.db.ExecContext(ctx, "CREATE TRIGGER reject050 BEFORE INSERT ON schema_migrations WHEN NEW.version='050_history_covering_reads.sql' BEGIN SELECT RAISE(ABORT,'synthetic receipt failure'); END"); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err == nil {
		t.Fatal("failed index/receipt transaction reported success")
	}
	assertCount(t, s, "SELECT COUNT(*) FROM sqlite_master WHERE name='idx_latency_rollups_history_cover'", 0)
	assertCount(t, s, "SELECT COUNT(*) FROM schema_migrations WHERE version='050_history_covering_reads.sql'", 0)
	if !reflect.DeepEqual(previousLatencyPoints(t, s, args), want) {
		t.Fatal("rolled-back index migration changed existing aggregates")
	}
	if _, err := s.db.ExecContext(ctx, "DROP TRIGGER reject050"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	assertCount(t, s, "SELECT COUNT(*) FROM schema_migrations WHERE version='050_history_covering_reads.sql'", 1)
	got, err := s.AdminLatencyHistoryRange(ctx, node.ID, base, base.Add(time.Hour), 3600)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("migration changed weighted/null/kind results: got%+v want%+v error%v", got, want, err)
	}
	// Exercise maintenance of the covering values, not only its initial build.
	if _, err := s.db.ExecContext(ctx, "UPDATE latency_rollups SET latency_sum=50 WHERE node_id=? AND kind='tcping' AND bucket_seconds=60", node.ID); err != nil {
		t.Fatal(err)
	}
	got, err = s.AdminLatencyHistoryRange(ctx, node.ID, base, base.Add(time.Hour), 3600)
	if err != nil || got[1].LatencyMS == nil || *got[1].LatencyMS != 12.5 {
		t.Fatal("covering read did not see updated aggregate", got, err)
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM latency_rollups WHERE node_id=? AND kind='ping'", node.ID); err != nil {
		t.Fatal(err)
	}
	got, err = s.AdminLatencyHistoryRange(ctx, node.ID, base, base.Add(time.Hour), 3600)
	if err != nil || len(got) != 1 || got[0].Kind != "tcping" {
		t.Fatal("covering read retained a deleted kind", got, err)
	}
}
