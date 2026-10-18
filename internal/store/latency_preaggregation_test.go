package store

import (
	"context"
	"database/sql"
	"math"
	"strings"
	"testing"
	"time"
)

// Keep the previous one-pass query as a differential reference for this
// performance change. Explicit weighted expectations below check the reference
// independently, including successful samples that contain no latency value.
const latencyHistoryBeforePreaggregation = `WITH history AS (
 SELECT node_id,target_id,kind,substr(captured_at,1,19)||'Z' AS sample_at,1 AS sample_count,success AS success_count,
 CASE WHEN success=1 AND latency_ms IS NOT NULL THEN 1 ELSE 0 END AS latency_count,
 CASE WHEN success=1 THEN COALESCE(latency_ms,0) ELSE 0 END AS latency_sum
 FROM latency_samples WHERE node_id=:node AND ` + historyRawBounds + `
 UNION ALL
 SELECT node_id,target_id,kind,bucket_at,sample_count,success_count,latency_count,latency_sum
 FROM latency_rollups WHERE node_id=:node AND ` + historyRollupBounds + `
 ) SELECT (unixepoch(substr(sample_at,1,19)||'Z')/:bucket)*:bucket AS bucket,history.target_id,t.name,history.kind,
 CASE WHEN SUM(latency_count)>0 THEN SUM(latency_sum)/SUM(latency_count) END,
 100.0*SUM(success_count)/SUM(sample_count)
 FROM history JOIN targets t ON t.id=history.target_id JOIN nodes n ON n.id=history.node_id
 WHERE (n.hidden=0 OR :include_hidden) GROUP BY bucket,history.target_id,t.name,history.kind
 ORDER BY bucket,t.sort_order,t.name`

func previousLatencyPoints(t *testing.T, s *Store, args []any) []LatencyHistoryPoint {
	t.Helper()
	rows, err := s.db.Query(latencyHistoryBeforePreaggregation, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	points := make([]LatencyHistoryPoint, 0)
	for rows.Next() {
		var p LatencyHistoryPoint
		var seconds int64
		var latency sql.NullFloat64
		if err = rows.Scan(&seconds, &p.TargetID, &p.Name, &p.Kind, &latency, &p.SuccessRate); err != nil {
			t.Fatal(err)
		}
		p.Time = time.Unix(seconds, 0).UTC()
		if latency.Valid {
			v := latency.Float64
			p.LatencyMS = &v
		}
		points = append(points, p)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return points
}

func TestLatencyHistoryAccessPlansMatchWindow(t *testing.T) {
	s, node, _ := historyFixture(t)
	end := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name string
		span time.Duration
		long bool
	}{
		{"short", time.Hour, false},
		{"exact thirty days", 30 * 24 * time.Hour, false},
		{"over thirty days", 30*24*time.Hour + time.Nanosecond, true},
		{"year", 365 * 24 * time.Hour, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			start := end.Add(-test.span)
			query := latencyHistorySQL(start, end)
			if (query == latencyHistoryLongQuery) != test.long {
				t.Fatal("wrong access strategy for history window")
			}
			args, err := historyQueryArgs(node.ID, start, end, 43200, false)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := s.db.Query("EXPLAIN QUERY PLAN "+query, args...)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			matched := false
			for rows.Next() {
				var a, b, c int
				var detail string
				if err := rows.Scan(&a, &b, &c, &detail); err != nil {
					t.Fatal(err)
				}
				if test.long && strings.Contains(detail, "sqlite_autoindex_latency_rollups_1 (node_id=?)") {
					matched = true
				}
				if !test.long && strings.Contains(detail, "latency_rollups") && strings.Contains(detail, "node_id=? AND bucket_seconds=? AND bucket_at>? AND bucket_at<?") {
					matched = true
				}
				if strings.HasPrefix(detail, "SCAN latency_rollups") {
					t.Fatal("history scans all nodes", detail)
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if !matched {
				t.Fatal("required node/range access path absent")
			}
		})
	}
}

func TestLatencyPreaggregationPreservesWeightedSourcesAndScopes(t *testing.T) {
	s, node, target := historyFixture(t)
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	other, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "unrelated node"})
	if err != nil {
		t.Fatal(err)
	}
	failed, err := s.CreateTarget(ctx, CreateTargetParams{Name: "first failed", Kind: "tcping", Host: "example.test", Port: target.Port, IntervalSeconds: 30, TimeoutMS: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("UPDATE targets SET sort_order=-1 WHERE id=?", failed.ID); err != nil {
		t.Fatal(err)
	}
	nullOnly, err := s.CreateTarget(ctx, CreateTargetParams{Name: "successful without duration", Kind: "tcping", Host: "example.test", Port: target.Port, IntervalSeconds: 30, TimeoutMS: 1000})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []struct {
		SQL  string
		Args []any
	}{
		{`INSERT INTO latency_rollups VALUES(?,?,?,60,?,10,8,6,60)`, []any{node.ID, target.ID, "tcping", formatTime(base)}},
		{`INSERT INTO latency_rollups VALUES(?,?,?,300,?,100,60,30,600)`, []any{node.ID, target.ID, "tcping", formatTime(base.Add(300 * time.Second))}},
		{`INSERT INTO latency_rollups VALUES(?,?,?,60,?,10,0,0,0)`, []any{node.ID, failed.ID, "tcping", formatTime(base)}},
		{`INSERT INTO latency_rollups VALUES(?,?,?,60,?,2,2,0,0)`, []any{node.ID, nullOnly.ID, "tcping", formatTime(base)}},
		{`INSERT INTO latency_rollups VALUES(?,?,?,60,?,999,999,999,999999)`, []any{other.ID, target.ID, "tcping", formatTime(base)}},
	} {
		if _, err = s.db.Exec(q.SQL, q.Args...); err != nil {
			t.Fatal(err)
		}
	}
	insert := func(id, nodeID, kind string, at time.Time, success int, value any) {
		t.Helper()
		if _, err := s.db.Exec(`INSERT INTO latency_samples(node_id,target_id,kind,captured_at,success,latency_ms) VALUES(?,?,?,?,?,?)`, nodeID, id, kind, formatTime(at), success, value); err != nil {
			t.Fatal(err)
		}
	}
	for i, v := range []struct {
		Success int
		Value   any
	}{{1, 30.0}, {1, nil}, {0, 999.0}, {1, 50.0}, {0, nil}} {
		insert(target.ID, node.ID, "tcping", base.Add(time.Duration(610+i)*time.Second), v.Success, v.Value)
	}
	insert(target.ID, node.ID, "ping", base.Add(618*time.Second), 1, 7.0)
	insert(failed.ID, node.ID, "tcping", base.Add(620*time.Second), 0, 1234.0)
	insert(failed.ID, node.ID, "tcping", base.Add(621*time.Second), 0, nil)
	insert(target.ID, other.ID, "tcping", base.Add(620*time.Second), 1, 99999.0)
	for i, offset := range []time.Duration{-1, 0, 1, time.Second - 1, time.Second, time.Second + 1} {
		insert(target.ID, node.ID, "tcping", base.Add(1200*time.Second+offset), 1, float64(i+1)/10)
	}
	for _, hidden := range []bool{false, true} {
		if _, err = s.db.Exec("UPDATE nodes SET hidden=? WHERE id=?", hidden, node.ID); err != nil {
			t.Fatal(err)
		}
		for _, admin := range []bool{false, true} {
			for _, window := range []struct {
				Start, End time.Time
				Bucket     int
			}{{base, base.Add(time.Hour), 3600}, {base, base.Add(12 * time.Hour), 43200}, {base, base.Add(30 * 24 * time.Hour), 3600}, {base, base.Add(30*24*time.Hour + time.Nanosecond), 43200}, {base, base.Add(365 * 24 * time.Hour), 43200}, {base.Add(time.Nanosecond), base.Add(900*time.Second - time.Nanosecond), 60}, {base.Add(1200 * time.Second), base.Add(1201 * time.Second), 1}, {base.Add(1200*time.Second + 1), base.Add(1201*time.Second - 1), 1}, {base.Add(2 * time.Hour), base.Add(3 * time.Hour), 300}} {
				args, err := historyQueryArgs(node.ID, window.Start, window.End, window.Bucket, admin)
				if err != nil {
					t.Fatal(err)
				}
				want := previousLatencyPoints(t, s, args)
				got, err := s.latencyHistoryRange(ctx, node.ID, window.Start, window.End, window.Bucket, admin)
				if err != nil {
					t.Fatal(err)
				}
				if len(got) != len(want) {
					t.Fatalf("hidden=%t admin=%t bucket=%d: got%d want%d", hidden, admin, window.Bucket, len(got), len(want))
				}
				for i, p := range got {
					w := want[i]
					if p.TargetID != w.TargetID || p.Name != w.Name || p.Kind != w.Kind || !p.Time.Equal(w.Time) || math.Abs(p.SuccessRate-w.SuccessRate) > 1e-10 || (p.LatencyMS == nil) != (w.LatencyMS == nil) {
						t.Fatalf("point%d mismatch: got%+v want%+v", i, p, w)
					}
					if p.LatencyMS != nil && math.Abs(*p.LatencyMS-*w.LatencyMS) > 1e-10 {
						t.Fatal("latency weight changed", *p.LatencyMS, *w.LatencyMS)
					}
				}
			}
		}
	}
	// Exclude the fractional-boundary records at20minutes and combine just the
	// minute, five-minute and five raw inputs in the first15-minute bucket.
	got, err := s.AdminLatencyHistoryRange(ctx, node.ID, base, base.Add(900*time.Second), 900)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, p := range got {
		switch {
		case p.TargetID == target.ID && p.Kind == "tcping":
			if p.LatencyMS == nil || math.Abs(*p.LatencyMS-740.0/38) > 1e-10 || math.Abs(p.SuccessRate-100.0*71/115) > 1e-10 {
				t.Fatalf("weighted result: %+v", p)
			}
			seen["weighted"] = true
		case p.TargetID == target.ID && p.Kind == "ping":
			if p.LatencyMS == nil || *p.LatencyMS != 7 || p.SuccessRate != 100 {
				t.Fatal(p)
			}
			seen["kind"] = true
		case p.TargetID == failed.ID:
			if p.LatencyMS != nil || p.SuccessRate != 0 {
				t.Fatal(p)
			}
			seen["failed"] = true
		case p.TargetID == nullOnly.ID:
			if p.LatencyMS != nil || p.SuccessRate != 100 {
				t.Fatal(p)
			}
			seen["null"] = true
		default:
			t.Fatal("unexpected target", p)
		}
	}
	if len(seen) != 4 || len(got) != 4 || got[0].TargetID != failed.ID {
		t.Fatal("missing group or changed target order", seen, got)
	}
	if _, err = s.db.Exec("UPDATE targets SET name='renamed target' WHERE id=?", target.ID); err != nil {
		t.Fatal(err)
	}
	renamed, err := s.AdminLatencyHistoryRange(ctx, node.ID, base, base.Add(900*time.Second), 900)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range renamed {
		if p.TargetID == target.ID && p.Name != "renamed target" {
			t.Fatal("stale metadata", p)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = s.AdminLatencyHistoryRange(canceled, node.ID, base, base.Add(time.Hour), 3600); err == nil {
		t.Fatal("canceled history query succeeded")
	}
}
