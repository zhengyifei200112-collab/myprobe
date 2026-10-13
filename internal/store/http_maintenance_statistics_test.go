package store

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
)

func httpMaintenanceFixture(t *testing.T) (*Store, HTTPService, []Node, time.Time) {
	t.Helper()
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "http-maintenance.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	nodes := []Node{}
	for _, name := range []string{"first observer", "second observer"} {
		n, _, err := s.CreateNode(ctx, CreateNodeParams{Name: name})
		if err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, n)
	}
	v, err := s.SaveHTTPService(ctx, HTTPService{Name: "fixture", Enabled: true, IntervalSeconds: 30, NodeIDs: []string{nodes[0].ID, nodes[1].ID}, Spec: httpcheck.Spec{URL: "http://example.test/private-marker", Method: "GET", StatusCodes: []int{200}, TimeoutMS: 1000, MaxBodyBytes: 1024}})
	if err != nil {
		t.Fatal(err)
	}
	// A deterministic historical schedule, not a real-clock execution claim.
	at := time.Date(2026, 2, 28, 23, 59, 0, 0, time.UTC)
	v.CreatedAt, v.UpdatedAt = at, at
	if _, err := s.db.Exec(`UPDATE http_services SET created_at=?,updated_at=? WHERE id=?`, formatTime(at), formatTime(at), v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE http_schedule_epochs SET start_ns=? WHERE service_id=?`, at.UnixNano(), v.ID); err != nil {
		t.Fatal(err)
	}
	for i, outcome := range []string{"success", "failure", "unobserved", "", "success", "failure", "", "success"} {
		if outcome == "" {
			continue
		}
		point := at.Add(time.Duration(i) * 30 * time.Second)
		task, err := s.CreateHTTPTask(ctx, v.ID, nodes[0].ID, point, point)
		if err != nil {
			t.Fatal(err)
		}
		result := httpcheck.Result{TaskID: task.ID, ServiceID: v.ID, Revision: task.Revision, ScheduledAt: point, CompletedAt: point.Add(time.Millisecond), Outcome: outcome, StatusCode: 200}
		if outcome == "failure" {
			result.StatusCode = 503
			result.ErrorClass = "status_mismatch"
		}
		if outcome == "unobserved" {
			result.StatusCode = 0
			result.ErrorClass = "busy"
		}
		if err := s.SaveHTTPResult(ctx, nodes[0].ID, result, result.CompletedAt); err != nil {
			t.Fatal(err)
		}
	}
	return s, v, nodes, at
}

func statisticsWindow(t *testing.T, s *Store, node Node, at time.Time, start, end, created time.Duration) MaintenanceWindow {
	t.Helper()
	w, err := s.CreateMaintenanceWindow(context.Background(), MaintenanceWindow{Reason: "private maintenance reason", Timezone: "Asia/Shanghai", CreatedBy: "fixture-admin", NodeIDs: []string{node.ID}, StartsAt: at.Add(start), EndsAt: at.Add(end)})
	if err != nil {
		t.Fatal(err)
	}
	// Only test fixtures set CreatedAt; the production value is the server clock.
	if _, err := s.db.Exec(`UPDATE maintenance_windows SET created_at=? WHERE id=?`, formatTime(at.Add(created)), w.ID); err != nil {
		t.Fatal(err)
	}
	return w
}

func TestHTTPMaintenanceStatisticsUnionRawCountsAndFullRestore(t *testing.T) {
	s, v, nodes, at := httpMaintenanceFixture(t)
	ctx := context.Background()
	statisticsWindow(t, s, nodes[0], at, 30*time.Second, 90*time.Second, -time.Hour)
	statisticsWindow(t, s, nodes[0], at, 60*time.Second, 150*time.Second, -time.Hour)
	statisticsWindow(t, s, nodes[0], at, 150*time.Second, 180*time.Second, -time.Hour)
	statisticsWindow(t, s, nodes[1], at, 0, 240*time.Second, -time.Hour)
	canceled := statisticsWindow(t, s, nodes[0], at, 0, 30*time.Second, -time.Hour)
	if err := s.CancelMaintenanceWindow(ctx, canceled.ID, 1, at.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	end := at.Add(240 * time.Second)
	now := end.Add(126 * time.Second)
	raw, err := s.HTTPServiceStatistics(ctx, v.ID, nodes[0].ID, at, end, now)
	if err != nil {
		t.Fatal(err)
	}
	adjusted, err := s.HTTPServiceStatistics(ctx, v.ID, nodes[0].ID, at, end, now, true)
	if err != nil {
		t.Fatal(err)
	}
	if raw.MaintenanceExcluded || !adjusted.MaintenanceExcluded || raw.Expected != 8 || raw.Success != 3 || raw.Failure != 2 || raw.Missing != 3 || raw.Unobserved != 1 {
		t.Fatal("raw statistics changed")
	}
	excluded := adjusted.MaintenanceExcludedCounts
	if excluded.Expected != 5 || excluded.Success != 1 || excluded.Failure != 2 || excluded.Unobserved != 1 || excluded.Missing != 2 {
		t.Fatalf("union exclusion: %+v", excluded)
	}
	if adjusted.Expected != 3 || adjusted.Success != 2 || adjusted.Failure != 0 || adjusted.Unobserved != 0 || adjusted.Missing != 1 || adjusted.SuccessRate == nil || *adjusted.SuccessRate != 1 || adjusted.Coverage == nil || *adjusted.Coverage != float64(2)/3 {
		t.Fatalf("adjusted: %+v", adjusted.HTTPStatisticsCounts)
	}
	if !reflect.DeepEqual(raw.HTTPStatisticsCounts, adjusted.Raw) || !reflect.DeepEqual(raw.MaintenanceExcludedCounts, excluded) {
		t.Fatal("raw/excluded evidence depends on selected mode")
	}
	other, err := s.HTTPServiceStatistics(ctx, v.ID, nodes[1].ID, at, end, now, true)
	if err != nil || other.Raw.Expected != 8 || other.MaintenanceExcludedCounts.Expected != 8 || other.Expected != 0 || other.SuccessRate != nil || other.Coverage != nil {
		t.Fatal("all-excluded observer produced health or leaked samples")
	}
	if rowCount(t, s, "http_tasks") != 6 || rowCount(t, s, "alert_incidents") != 0 {
		t.Fatal("statistics changed source results or created event")
	}
	backup := filepath.Join(t.TempDir(), "full-maintenance.db")
	if err := s.ConsistentBackup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	path := s.DatabasePath()
	s.Close()
	if _, err := StageDatabaseRestore(ctx, path, backup); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyPendingRestore(ctx, path, now); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	after, err := restored.HTTPServiceStatistics(ctx, v.ID, nodes[0].ID, at, end, now, true)
	if err != nil || !reflect.DeepEqual(adjusted, after) {
		t.Fatal("full restore changed maintenance statistics")
	}
}

func TestHTTPMaintenanceStatisticsExactCreationCancellationAndChangeBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		start, end, created, cancel time.Duration
		want                        int64
	}{
		{"half-open", 30 * time.Second, 90 * time.Second, -time.Hour, 0, 2},
		{"just-after-slot", 30*time.Second + time.Millisecond, 90 * time.Second, -time.Hour, 0, 1},
		{"just-after-end", 30 * time.Second, 90*time.Second + time.Millisecond, -time.Hour, 0, 3},
		{"created-at-slot", 0, 120 * time.Second, 60 * time.Second, 0, 2},
		{"created-after-slot", 0, 120 * time.Second, 60*time.Second + time.Nanosecond, 0, 1},
		{"cancel-at-slot", 30 * time.Second, 180 * time.Second, -time.Hour, 90 * time.Second, 2},
		{"cancel-before-start", 30 * time.Second, 180 * time.Second, -time.Hour, time.Second, 0},
		{"cancel-after-end", 30 * time.Second, 90 * time.Second, -time.Hour, 180 * time.Second, 2},
		{"created-after-end", 0, 120 * time.Second, 180 * time.Second, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, v, nodes, at := httpMaintenanceFixture(t)
			w := statisticsWindow(t, s, nodes[0], at, tc.start, tc.end, tc.created)
			if tc.cancel != 0 {
				if err := s.CancelMaintenanceWindow(context.Background(), w.ID, 1, at.Add(tc.cancel)); err != nil {
					t.Fatal(err)
				}
			}
			end := at.Add(240 * time.Second)
			got, err := s.HTTPServiceStatistics(context.Background(), v.ID, nodes[0].ID, at, end, end.Add(126*time.Second), true)
			if err != nil || got.MaintenanceExcludedCounts.Expected != tc.want || got.Expected != 8-tc.want {
				t.Fatal("boundary mismatch", tc.name, got.MaintenanceExcludedCounts.Expected, err)
			}
		})
	}
	s, v, nodes, at := httpMaintenanceFixture(t)
	old := statisticsWindow(t, s, nodes[0], at, 0, 240*time.Second, -time.Hour)
	if err := s.CancelMaintenanceWindow(context.Background(), old.ID, 1, at.Add(90*time.Second)); err != nil {
		t.Fatal(err)
	}
	statisticsWindow(t, s, nodes[0], at, 60*time.Second, 180*time.Second, 90*time.Second+time.Nanosecond)
	end := at.Add(240 * time.Second)
	got, err := s.HTTPServiceStatistics(context.Background(), v.ID, nodes[0].ID, at, end, end.Add(126*time.Second), true)
	// Old [0,90) plus new (90,180): the exact 90s slot lies in neither.
	if err != nil || got.MaintenanceExcludedCounts.Expected != 5 || got.Expected != 3 {
		t.Fatal("cancel/recreate rewrote old history or excluded creation gap")
	}
}

func TestHTTPMaintenanceStatisticsMaturityAndRetentionKeepMatchingDenominators(t *testing.T) {
	s, v, nodes, at := httpMaintenanceFixture(t)
	ctx := context.Background()
	statisticsWindow(t, s, nodes[0], at, 0, 240*time.Second, -time.Hour)
	end := at.Add(240 * time.Second)
	immature, err := s.HTTPServiceStatistics(ctx, v.ID, nodes[0].ID, at, end, at.Add(time.Minute), true)
	if err != nil || immature.Raw.Expected != 0 || immature.MaintenanceExcludedCounts.Expected != 0 || immature.Expected != 0 || immature.Coverage != nil {
		t.Fatal("maintenance added immature slots")
	}
	mature, err := s.HTTPServiceStatistics(ctx, v.ID, nodes[0].ID, at, end, at.Add(186*time.Second), true)
	if err != nil || mature.Raw.Expected != 2 || mature.MaintenanceExcludedCounts.Expected != 2 || mature.Expected != 0 {
		t.Fatal("maintenance ignored maturity bound")
	}
	if err := s.ApplyRetention(ctx, at.Add(HTTPHistoryRetention+90*time.Second), DefaultRetentionPolicy()); err != nil {
		t.Fatal(err)
	}
	retained, err := s.HTTPServiceStatistics(ctx, v.ID, nodes[0].ID, at, end, end.Add(126*time.Second), true)
	if err != nil || retained.Raw.Expected != 5 || retained.MaintenanceExcludedCounts.Expected != 5 || retained.Expected != 0 || !retained.Start.Equal(at.Add(90*time.Second)) {
		t.Fatal("maintenance denominator crossed retention floor")
	}
}

func TestHTTPMaintenanceStatisticsUsesWindowReadSnapshot(t *testing.T) {
	s, _, nodes, at := httpMaintenanceFixture(t)
	ctx := context.Background()
	w := statisticsWindow(t, s, nodes[0], at, 0, 240*time.Second, -time.Hour)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var seen int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes`).Scan(&seen); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelMaintenanceWindow(ctx, w.ID, 1, at.Add(90*time.Second)); err != nil {
		t.Fatal(err)
	}
	intervals, err := httpMaintenanceIntervals(ctx, tx, nodes[0].ID, at, at.Add(240*time.Second))
	if err != nil || len(intervals) != 1 || intervals[0].end != at.Add(240*time.Second).UnixNano() {
		t.Fatal("window read left established transaction snapshot")
	}
}

func TestHTTPMaintenanceStatisticsRevisionAndRemovedAssignment(t *testing.T) {
	s, v, nodes, at := httpMaintenanceFixture(t)
	ctx := context.Background()
	v.IntervalSeconds = 60
	v.NodeIDs = []string{nodes[0].ID}
	v, err := s.SaveHTTPService(ctx, v)
	if err != nil {
		t.Fatal(err)
	}
	anchor := at.Add(75 * time.Second)
	if _, err := s.db.Exec(`UPDATE http_schedule_epochs SET end_ns=? WHERE service_id=? AND revision=1`, anchor.UnixNano(), v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE http_schedule_epochs SET start_ns=?,end_ns=? WHERE service_id=? AND revision=2`, anchor.UnixNano(), at.Add(225*time.Second).UnixNano(), v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE http_services SET updated_at=? WHERE id=?`, formatTime(anchor), v.ID); err != nil {
		t.Fatal(err)
	}
	for i, outcome := range []string{"success", "unobserved", "failure"} {
		point := anchor.Add(time.Duration(i) * time.Minute)
		task, err := s.CreateHTTPTask(ctx, v.ID, nodes[0].ID, point, point)
		if err != nil {
			t.Fatal(err)
		}
		r := httpcheck.Result{TaskID: task.ID, ServiceID: v.ID, Revision: task.Revision, ScheduledAt: point, CompletedAt: point.Add(time.Millisecond), Outcome: outcome, StatusCode: 200}
		if outcome == "failure" {
			r.StatusCode = 503
			r.ErrorClass = "status_mismatch"
		}
		if outcome == "unobserved" {
			r.StatusCode = 0
			r.ErrorClass = "busy"
		}
		if err := s.SaveHTTPResult(ctx, nodes[0].ID, r, r.CompletedAt); err != nil {
			t.Fatal(err)
		}
	}
	v.Enabled = false
	if _, err := s.SaveHTTPService(ctx, v); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE http_schedule_epochs SET end_ns=? WHERE service_id=? AND revision=2`, at.Add(225*time.Second).UnixNano(), v.ID); err != nil {
		t.Fatal(err)
	}
	statisticsWindow(t, s, nodes[0], at, 50*time.Second, 140*time.Second, -time.Hour)
	statisticsWindow(t, s, nodes[0], at, 240*time.Second, 315*time.Second, -time.Hour)
	end := at.Add(315 * time.Second)
	got, err := s.HTTPServiceStatistics(ctx, v.ID, nodes[0].ID, at, end, end.Add(126*time.Second), true)
	if err != nil || got.Raw.Expected != 6 || got.Raw.Success != 2 || got.Raw.Failure != 2 || got.Raw.Unobserved != 2 || got.MaintenanceExcludedCounts.Expected != 3 || got.Expected != 3 || got.Success != 1 || got.Failure != 2 || got.Missing != 0 {
		t.Fatal("maintenance lost revision anchor or counted disabled slots", err)
	}
	removed, err := s.HTTPServiceStatistics(ctx, v.ID, nodes[1].ID, at, end, end.Add(126*time.Second), true)
	if err != nil || removed.Raw.Expected != 3 || removed.Expected != 3 || removed.Missing != 3 {
		t.Fatal("removed assignment erased earlier expectations")
	}
}

func TestHTTPMaintenanceStatisticsNanosecondAnchorsAndIndexedWindowLimit(t *testing.T) {
	s, v, nodes, at := httpMaintenanceFixture(t)
	ctx := context.Background()
	if _, err := s.db.Exec(`DELETE FROM http_tasks;UPDATE http_schedule_epochs SET start_ns=start_ns+1`); err != nil {
		t.Fatal(err)
	}
	point := at.Add(30*time.Second + time.Nanosecond)
	task, err := s.CreateHTTPTask(ctx, v.ID, nodes[0].ID, point, point)
	if err != nil {
		t.Fatal(err)
	}
	result := httpcheck.Result{TaskID: task.ID, ServiceID: v.ID, Revision: task.Revision, ScheduledAt: point, CompletedAt: point.Add(time.Millisecond), Outcome: "success", StatusCode: 200}
	if err := s.SaveHTTPResult(ctx, nodes[0].ID, result, result.CompletedAt); err != nil {
		t.Fatal(err)
	}
	statisticsWindow(t, s, nodes[0], at, 30*time.Second, 90*time.Second, -time.Hour)
	end := at.Add(240 * time.Second)
	got, err := s.HTTPServiceStatistics(ctx, v.ID, nodes[0].ID, at, end, end.Add(126*time.Second), true)
	if err != nil || got.Raw.Expected != 8 || got.Raw.Success != 1 || got.MaintenanceExcludedCounts.Expected != 2 || got.MaintenanceExcludedCounts.Success != 1 || got.Expected != 6 || got.SuccessRate != nil {
		t.Fatal("nanosecond plan rounded to window boundary")
	}
	rows, err := s.db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+httpMaintenanceIntervalsQuery, nodes[0].ID, end.UnixMilli(), at.UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	var plan strings.Builder
	for rows.Next() {
		var id, parent, aux int
		var detail string
		if err := rows.Scan(&id, &parent, &aux, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if !strings.Contains(plan.String(), "idx_maintenance_nodes_lookup") {
		t.Fatal("window query lost node membership index")
	}
	// Imported/corrupt databases may exceed the normal creation guard. Never
	// silently truncate exclusion and report partially adjusted availability.
	if _, err := s.db.Exec(`WITH RECURSIVE ids(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM ids WHERE x<1000) INSERT INTO maintenance_windows(id,revision,reason,timezone,starts_at_ms,ends_at_ms,created_by,created_at) SELECT 'limit-'||x,1,'fixture','UTC',?,?,'fixture',? FROM ids`, at.UnixMilli(), end.UnixMilli(), formatTime(at.Add(-time.Hour))); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO maintenance_window_nodes(window_id,node_id) SELECT id,? FROM maintenance_windows WHERE id LIKE 'limit-%'`, nodes[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HTTPServiceStatistics(ctx, v.ID, nodes[0].ID, at, end, end.Add(126*time.Second), true); err == nil {
		t.Fatal("too many windows returned partial statistics")
	}
}

func TestHTTPMaintenanceStatisticsDoesNotExcludeUnreconstructableHistory(t *testing.T) {
	s, v, nodes, at := httpMaintenanceFixture(t)
	ctx := context.Background()
	known := at.Add(100 * time.Second)
	if _, err := s.db.Exec(`UPDATE http_services SET revision=2,updated_at=? WHERE id=?`, formatTime(known), v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM http_schedule_epochs WHERE service_id=?`, v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO http_schedule_epochs(service_id,node_id,revision,start_ns,interval_seconds) VALUES(?,?,2,?,30)`, v.ID, nodes[0].ID, known.UnixNano()); err != nil {
		t.Fatal(err)
	}
	statisticsWindow(t, s, nodes[0], at, 0, 180*time.Second, -time.Hour)
	end := at.Add(240 * time.Second)
	got, err := s.HTTPServiceStatistics(ctx, v.ID, nodes[0].ID, at, end, end.Add(126*time.Second), true)
	if err != nil || !got.ScheduleKnownFrom.Equal(known) || !got.Start.Equal(known) || got.Raw.Expected != 5 || got.Raw.Success != 0 || got.MaintenanceExcludedCounts.Expected != 3 || got.Expected != 2 || got.Missing != 2 || got.SuccessRate != nil {
		t.Fatal("maintenance manufactured unknown past schedule or retained old-version numerator")
	}
}
