package store

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

func rebuildLatencyHours(t *testing.T, s *Store, limit int) bool {
	t.Helper()
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	more, err := refreshLatencyHistoryHours(context.Background(), tx, limit)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return more
}

func TestLatencyHoursWindowsInvalidationAndRollback(t *testing.T) {
	s, node, target := historyFixture(t)
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, item := range []struct {
		at   string
		tier int
		kind string
		sum  float64
	}{
		{formatTime(base), 60, "tcping", 5},
		{formatTime(base.Add(5 * time.Minute)), 300, "tcping", 13},
		{formatTime(base.Add(time.Hour)), 60, "ping", 0},
		{formatTime(base.Add(2 * time.Hour)), 300, "tcping", 19},
		{formatTime(base.Add(time.Hour - time.Minute)), 300, "tcping", 23},
		{base.Add(2*time.Hour + time.Second).Format("2006-01-02T15:04:05.000Z"), 60, "ping", 29},
	} {
		valid := 2
		if item.kind == "ping" {
			valid = 0
		}
		if _, err := s.db.Exec("INSERT INTO latency_rollups VALUES(?,?,?,?,?,7,3,?,?)",
			node.ID, target.ID, item.kind, item.tier, item.at, valid, item.sum); err != nil {
			t.Fatal(err)
		}
	}
	assertCount(t, s, "SELECT COUNT(*) FROM latency_history_hours_dirty", 3)
	check := func() {
		t.Helper()
		for _, window := range []struct {
			start, end time.Time
			bucket     int
		}{
			{base, base.Add(3 * time.Hour), 3600},
			{base.Add(time.Nanosecond), base.Add(3*time.Hour - time.Nanosecond), 3600},
			{base.Add(59 * time.Minute), base.Add(61 * time.Minute), 3600},
			{base.Add(59 * time.Minute), base.Add(2*time.Hour + 30*time.Second), 3600},
			{base, base.Add(30 * 24 * time.Hour), 43200},
			{base, base.Add(2 * time.Hour), 900},
		} {
			for _, admin := range []bool{false, true} {
				args, err := historyQueryArgs(node.ID, window.start, window.end, window.bucket, admin)
				if err != nil {
					t.Fatal(err)
				}
				want := previousLatencyPoints(t, s, args)
				got, err := s.latencyHistoryRange(ctx, node.ID, window.start, window.end, window.bucket, admin)
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("window %v..%v bucket %d admin %t: got %+v want %+v error %v",
						window.start, window.end, window.bucket, admin, got, want, err)
				}
			}
		}
	}
	check()
	if !rebuildLatencyHours(t, s, 1) {
		t.Fatal("one group incorrectly completed the backlog")
	}
	assertCount(t, s, "SELECT COUNT(*) FROM latency_history_hours_dirty", 2)
	check()
	if rebuildLatencyHours(t, s, -1) {
		t.Fatal("full rebuild left pending hours")
	}
	assertCount(t, s, "SELECT COUNT(*) FROM latency_history_hours", 3)
	check()
	// REPLACE must invalidate even when its implicit delete trigger is skipped.
	conn, err := s.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"PRAGMA recursive_triggers=OFF",
		"INSERT OR REPLACE INTO latency_rollups VALUES('" + node.ID + "','" + target.ID + "','tcping',60,'" + formatTime(base) + "',7,3,2,31)",
		"PRAGMA recursive_triggers=ON"} {
		if _, err := conn.ExecContext(ctx, query); err != nil {
			conn.Close()
			t.Fatal(err)
		}
	}
	conn.Close()
	assertCount(t, s, "SELECT COUNT(*) FROM latency_history_hours_dirty", 1)
	check()
	before := historyRows(t, s, "latency_history_hours")
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := refreshLatencyHistoryHours(ctx, tx, 128); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, historyRows(t, s, "latency_history_hours")) {
		t.Fatal("rollback consumed pending work")
	}
	assertCount(t, s, "SELECT COUNT(*) FROM latency_history_hours_dirty", 1)
	check()
	rebuildLatencyHours(t, s, -1)
	if _, err := s.db.Exec("UPDATE latency_rollups SET bucket_at=?,kind='ping' WHERE bucket_at=? AND kind='tcping'",
		formatTime(base.Add(4*time.Hour)), formatTime(base)); err != nil {
		t.Fatal(err)
	}
	check()
	rebuildLatencyHours(t, s, -1)
	if _, err := s.db.Exec("DELETE FROM latency_rollups WHERE node_id=?", node.ID); err != nil {
		t.Fatal(err)
	}
	check()
	rebuildLatencyHours(t, s, -1)
	assertCount(t, s, "SELECT COUNT(*) FROM latency_history_hours", 0)
	assertCount(t, s, "SELECT COUNT(*) FROM latency_history_hours_dirty", 0)
	if _, err := s.db.Exec("INSERT INTO latency_rollups VALUES(?,?,'tcping',60,?,7,3,2,5)",
		node.ID, target.ID, formatTime(base)); err != nil {
		t.Fatal(err)
	}
	rebuildLatencyHours(t, s, -1)
	if _, err := s.db.Exec("DELETE FROM targets WHERE id=?", target.ID); err != nil {
		t.Fatal("source cascade could not invalidate its summary", err)
	}
	rebuildLatencyHours(t, s, -1)
	assertCount(t, s, "SELECT COUNT(*) FROM latency_history_hours", 0)
	assertCount(t, s, "SELECT COUNT(*) FROM latency_history_hours_dirty", 0)
}

func TestLatencyHoursMigrationBackfillRollbackAndPlans(t *testing.T) {
	s, node, target := historyFixture(t)
	ctx := context.Background()
	for _, name := range []string{"latency_history_hours_insert", "latency_history_hours_delete",
		"latency_history_hours_update_old", "latency_history_hours_update_new"} {
		if _, err := s.db.Exec("DROP TRIGGER " + name); err != nil {
			t.Fatal(err)
		}
	}
	for _, query := range []string{
		"DROP TABLE latency_history_hours", "DROP TABLE latency_history_hours_dirty",
		"DROP INDEX idx_latency_rollups_unaligned_history",
		"DELETE FROM schema_migrations WHERE version='052_latency_history_hours.sql'",
	} {
		if _, err := s.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec("INSERT INTO latency_rollups VALUES(?,?,'tcping',60,'2026-01-01T00:00:00Z',7,3,2,11)",
		node.ID, target.ID); err != nil {
		t.Fatal(err)
	}
	before := historyRows(t, s, "latency_rollups")
	if _, err := s.db.Exec("CREATE TRIGGER reject052 BEFORE INSERT ON schema_migrations WHEN NEW.version='052_latency_history_hours.sql' BEGIN SELECT RAISE(ABORT,'synthetic receipt failure'); END"); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err == nil {
		t.Fatal("failed migration reported success")
	}
	assertCount(t, s, "SELECT COUNT(*) FROM sqlite_master WHERE name='latency_history_hours'", 0)
	if !reflect.DeepEqual(before, historyRows(t, s, "latency_rollups")) {
		t.Fatal("failed migration changed source buckets")
	}
	if _, err := s.db.Exec("DROP TRIGGER reject052"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	assertCount(t, s, "SELECT COUNT(*) FROM latency_history_hours", 1)
	assertCount(t, s, "SELECT COUNT(*) FROM latency_history_hours_dirty", 0)
	if !reflect.DeepEqual(before, historyRows(t, s, "latency_rollups")) {
		t.Fatal("backfill changed source buckets")
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	args, _ := historyQueryArgs(node.ID, start, start.Add(24*time.Hour), 3600, false)
	query, args := latencyHistoryReadQuery(start, start.Add(24*time.Hour), 3600, args)
	rows, err := s.db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var summarySeek, dirtySeek, unalignedSeek bool
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		summarySeek = summarySeek || strings.Contains(detail, "SEARCH h USING PRIMARY KEY") && strings.Contains(detail, "hour_unix>? AND hour_unix<?")
		dirtySeek = dirtySeek || strings.Contains(detail, "SEARCH d USING PRIMARY KEY") && strings.Contains(detail, "hour_unix>? AND hour_unix<?")
		unalignedSeek = unalignedSeek || strings.Contains(detail, "USING COVERING INDEX idx_latency_rollups_unaligned_history") && strings.Contains(detail, "bucket_at>? AND bucket_at<?")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !summarySeek || !dirtySeek || !unalignedSeek {
		t.Fatalf("unbounded hour path: summary %t dirty %t unaligned %t", summarySeek, dirtySeek, unalignedSeek)
	}
}

func TestLatencyHoursBoundedRetentionAndRefreshFailure(t *testing.T) {
	s, node, target := historyFixture(t)
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for index := range latencyHourBatch + 1 {
		if _, err := s.db.Exec("INSERT INTO latency_rollups VALUES(?,?,'tcping',60,?,7,3,2,5)",
			node.ID, target.ID, formatTime(base.Add(time.Duration(index)*time.Hour))); err != nil {
			t.Fatal(err)
		}
	}
	now := base.Add(time.Duration(latencyHourBatch+2) * time.Hour)
	policy := DefaultRetentionPolicy()
	if _, err := s.db.Exec("CREATE TRIGGER reject_hour_refresh BEFORE DELETE ON latency_history_hours_dirty BEGIN SELECT RAISE(ABORT,'synthetic refresh failure'); END"); err != nil {
		t.Fatal(err)
	}
	batch, err := s.RetainHistory(ctx, now, policy)
	if err == nil || batch != (HistoryRetentionBatch{}) {
		t.Fatal("failed refresh published a retention pass", batch, err)
	}
	assertCount(t, s, "SELECT COUNT(*) FROM latency_history_hours", 0)
	assertCount(t, s, "SELECT COUNT(*) FROM latency_history_hours_dirty", latencyHourBatch+1)
	if _, err := s.db.Exec("DROP TRIGGER reject_hour_refresh"); err != nil {
		t.Fatal(err)
	}
	batch, err = s.RetainHistory(ctx, now, policy)
	if err != nil || !batch.More {
		t.Fatal("bounded refresh did not preserve its backlog", batch, err)
	}
	assertCount(t, s, "SELECT COUNT(*) FROM latency_history_hours", latencyHourBatch)
	assertCount(t, s, "SELECT COUNT(*) FROM latency_history_hours_dirty", 1)
	batch, err = s.RetainHistory(ctx, now, policy)
	if err != nil || batch.More {
		t.Fatal("last refresh did not drain its backlog", batch, err)
	}
	assertCount(t, s, "SELECT COUNT(*) FROM latency_history_hours", latencyHourBatch+1)
	assertCount(t, s, "SELECT COUNT(*) FROM latency_history_hours_dirty", 0)
}
