package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

func TestNodeTimelineObservationEvidenceAndScope(t *testing.T) {
	s, node, _ := historyFixture(t)
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 1, time.UTC)
	if _, err := s.db.Exec(`UPDATE nodes SET collection_seconds=5,report_seconds=5 WHERE id=?`, node.ID); err != nil {
		t.Fatal(err)
	}
	for i, offset := range []time.Duration{-time.Second, 0, time.Second, 30 * time.Second, 31 * time.Second} {
		uptime := uint64(100 + i)
		if i >= 3 {
			uptime = uint64(i)
		}
		if err := s.SaveReport(ctx, node.ID, protocol.Report{CapturedAt: base.Add(offset), Uptime: uptime, PublicIP: "198.51.100.123"}); err != nil {
			t.Fatal(err)
		}
	}
	p, err := s.NodeTimelineRange(ctx, node.ID, base, base.Add(31*time.Second), false, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"report_resumed", "reboot_hint", "report_gap"}
	if len(p.Events) != len(want) || p.GapThresholdSeconds != 20 {
		t.Fatalf("events: %+v", p)
	}
	for i, kind := range want {
		if p.Events[i].Kind != kind || !p.Events[i].Approximate || p.Events[i].IncidentID != "" {
			t.Fatalf("event %d: %+v", i, p.Events[i])
		}
	}
	if !p.Events[2].Time.Equal(base.Add(21 * time.Second)) {
		t.Fatal("gap lost nanosecond precision or ignored predecessor")
	}
	raw, _ := json.Marshal(p)
	if strings.Contains(string(raw), "198.51") || strings.Contains(string(raw), "uptime_seconds") {
		t.Fatal("raw observation payload leaked")
	}
	// Slow collection must not be classified as loss solely by a faster report interval.
	if _, err := s.db.Exec(`UPDATE nodes SET collection_seconds=3600,hidden=1 WHERE id=?`, node.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.NodeTimelineRange(ctx, node.ID, base, base.Add(time.Hour), false, "", 100); !errors.Is(err, ErrNotFound) {
		t.Fatalf("public hidden node: %v", err)
	}
	p, err = s.NodeTimelineRange(ctx, node.ID, base, base.Add(time.Hour), true, "", 100)
	if err != nil || p.GapThresholdSeconds != 10800 || len(p.Events) != 1 || p.Events[0].Kind != "reboot_hint" {
		t.Fatalf("slow collector admin: %+v %v", p, err)
	}
}

func TestNodeTimelinePrivateEventsPaginationAndPrecision(t *testing.T) {
	s, rule, node := incidentFixture(t, `{"threshold_percent":90,"duration_seconds":30,"recovery_seconds":0}`)
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	incident := observe(t, s, rule, node, base, true, true)
	observe(t, s, rule, node, base.Add(30*time.Second+1), true, true)
	observe(t, s, rule, node, base.Add(31*time.Second), true, false)
	// Audit fixtures include unrelated nodes, private text and sub-second bounds.
	for i := 0; i < 8; i++ {
		if _, err := s.db.Exec(`INSERT INTO audit_log(action,object_type,object_id,remote_ip,details_json,created_at) VALUES('update','node',?,'198.51.100.123','{"secret":"do-not-expose"}',?)`, node.ID, formatTime(base.Add(time.Duration(i)*time.Nanosecond))); err != nil {
			t.Fatal(err)
		}
	}
	for _, fixture := range []struct{ action, object, id, details string }{
		{"update", "node", "unrelated", `{}`},
		{"batch_update", "nodes", "batch", `{"changes":[{"node_id":"` + node.ID + `"}]}`},
		{"reorder", "nodes", "", `{"before":[{"id":"` + node.ID + `"}]}`},
		{"batch_update", "nodes", "other", `{"changes":[{"node_id":"unrelated"}]}`},
		{"batch_update", "nodes", "malformed-array", `{"changes":["legacy-text",null,1]}`},
		{"reorder", "nodes", "malformed-array", `{"before":["legacy-text",false]}`},
		{"batch_update", "nodes", "legacy-invalid-json", `not-json`},
	} {
		if _, err := s.db.Exec(`INSERT INTO audit_log(action,object_type,object_id,details_json,created_at) VALUES(?,?,?,?,?)`, fixture.action, fixture.object, fixture.id, fixture.details, formatTime(base)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`INSERT INTO maintenance_windows(id,revision,reason,timezone,starts_at_ms,ends_at_ms,canceled_at_ms,created_by,created_at) VALUES('maintenance',1,'secret reason','UTC',?,?,?,'actor',?)`, base.UnixMilli(), base.Add(time.Hour).UnixMilli(), base.Add(10*time.Second).UnixMilli(), formatTime(base)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO maintenance_window_nodes(window_id,node_id) VALUES('maintenance',?)`, node.ID); err != nil {
		t.Fatal(err)
	}
	all, err := s.NodeTimelineRange(ctx, node.ID, base, base.Add(time.Minute), true, "", 100)
	if err != nil || len(all.Events) != 15 {
		t.Fatalf("private sources: %d %+v %v", len(all.Events), all, err)
	}
	counts := map[string]int{}
	for _, e := range all.Events {
		counts[e.Kind]++
		if strings.HasPrefix(e.Kind, "incident_") && e.IncidentID != incident.ID {
			t.Fatal("incorrect incident subject")
		}
	}
	for _, kind := range []string{"incident_observed", "incident_fired", "incident_recovered", "maintenance_started", "maintenance_canceled", "node_order_saved"} {
		if counts[kind] != 1 {
			t.Fatalf("kind %s: %v", kind, counts)
		}
	}
	if counts["maintenance_ended"] != 0 || counts["configuration_saved"] != 9 {
		t.Fatalf("incorrect canceled window or audit membership: %v", counts)
	}
	var paged []NodeEvent
	cursor := ""
	for n := 0; n < 10; n++ {
		page, err := s.NodeTimelineRange(ctx, node.ID, base, base.Add(time.Minute), true, cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		paged = append(paged, page.Events...)
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
		for _, scope := range []struct {
			id    string
			admin bool
			end   time.Time
		}{{node.ID, false, base.Add(time.Minute)}, {"other", true, base.Add(time.Minute)}, {node.ID, true, base.Add(2 * time.Minute)}} {
			if _, err := s.NodeTimelineRange(ctx, scope.id, base, scope.end, scope.admin, cursor, 2); !errors.Is(err, ErrTimelineCursor) {
				t.Fatalf("cursor escaped scope: %v", err)
			}
		}
	}
	a, _ := json.Marshal(all.Events)
	b, _ := json.Marshal(paged)
	if string(a) != string(b) {
		t.Fatalf("paging lost/duplicated equal-time events: %s != %s", a, b)
	}
	if strings.Contains(string(a), "secret") || strings.Contains(string(a), "198.51") || strings.Contains(string(a), "actor") {
		t.Fatal("timeline escaped allowlist")
	}
	public, err := s.NodeTimelineRange(ctx, node.ID, base, base.Add(time.Minute), false, "", 100)
	if err != nil || len(public.Events) != 0 {
		t.Fatalf("private events publicly visible: %+v %v", public, err)
	}
	precise, err := s.NodeTimelineRange(ctx, node.ID, base.Add(time.Nanosecond), base.Add(2*time.Nanosecond), true, "", 100)
	if err != nil || len(precise.Events) != 1 || !precise.Events[0].Time.Equal(base.Add(time.Nanosecond)) {
		t.Fatalf("fractional boundary: %+v %v", precise, err)
	}
}

func TestNodeTimelineRawBudgetAndLegacyDuplicates(t *testing.T) {
	s, node, _ := historyFixture(t)
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Many duplicate legacy captures must consume the work budget without
	// pretending the unscanned earlier history was empty or a first connection.
	if err := s.SaveReport(ctx, node.ID, protocol.Report{CapturedAt: base, Uptime: 100}); err != nil {
		t.Fatal(err)
	}
	_, err := s.db.Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<?) INSERT INTO metric_samples(node_id,captured_at,cpu_usage,memory_used,memory_total,disk_used,disk_total,net_rx_rate,net_tx_rate,net_rx_total,net_tx_total,report_json) SELECT ?,?,0,0,0,0,0,0,0,0,0,'{"uptime_seconds":100}' FROM n`, timelineObservationBudget+1, node.ID, formatTime(base.Add(time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.NodeTimelineRange(ctx, node.ID, base, base.Add(2*time.Second), false, "", 100)
	if err != nil || !p.ObservationsTruncated || len(p.Events) != 0 {
		t.Fatalf("legacy budget invented first observation/reboot: %+v %v", p, err)
	}
}

func TestNodeTimelineIndexesAndMigrationReopen(t *testing.T) {
	s, node, _ := historyFixture(t)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rows, err := s.db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+timelinePrivateSQL, sql.Named("node", node.ID), sql.Named("start", formatCapturedTime(base)), sql.Named("end", formatCapturedTime(base.Add(time.Hour))), sql.Named("before", formatCapturedTime(base.Add(time.Hour))), sql.Named("id", ""), sql.Named("limit", 51))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintln(&plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, index := range []string{"idx_incidents_node_started_ns", "idx_incidents_node_fired_ns", "idx_incidents_node_resolved_ns", "idx_audit_object_time_ns", "idx_audit_action_time_ns", "idx_maintenance_nodes_lookup"} {
		if !strings.Contains(plan.String(), index) {
			t.Fatalf("timeline lost %s:\n%s", index, plan.String())
		}
	}
}

func TestNodeTimelineMigrationFailureRollsBackAndPreservesEvidence(t *testing.T) {
	s := preNodeBatchIntegrationStore(t, false)
	ctx := context.Background()
	node, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "pre-timeline node"})
	if err != nil {
		t.Fatal(err)
	}
	at := "2026-01-01T00:00:00.000000001Z"
	if _, err := s.db.Exec(`INSERT INTO audit_log(action,object_type,object_id,details_json,created_at) VALUES('update','node',?,'{"private":"unchanged"}',?)`, node.ID, at); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail047 BEFORE INSERT ON schema_migrations WHEN NEW.version='047_node_timeline_indexes.sql' BEGIN SELECT RAISE(ABORT,'synthetic receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err == nil {
		t.Fatal("failed migration was reported applied")
	}
	assertCount(t, s, `SELECT count(*) FROM schema_migrations WHERE version='046_node_batches.sql'`, 1)
	assertCount(t, s, `SELECT count(*) FROM schema_migrations WHERE version='047_node_timeline_indexes.sql'`, 0)
	assertCount(t, s, `SELECT count(*) FROM pragma_table_xinfo('audit_log') WHERE name='created_sort_ns'`, 0)
	assertCount(t, s, `SELECT count(*) FROM pragma_table_xinfo('alert_incidents') WHERE name='started_sort_ns'`, 0)
	if _, err := s.db.Exec(`DROP TRIGGER fail047`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	assertCount(t, s, `SELECT count(*) FROM schema_migrations WHERE version='047_node_timeline_indexes.sql'`, 1)
	var original, details, key string
	if err := s.db.QueryRow(`SELECT created_at,details_json,created_sort_ns FROM audit_log WHERE object_id=?`, node.ID).Scan(&original, &details, &key); err != nil || original != at || key != at || details != `{"private":"unchanged"}` {
		t.Fatalf("migration changed evidence: %q %q %q %v", original, details, key, err)
	}
	reopened, err := Open(ctx, s.path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	page, err := reopened.NodeTimelineRange(ctx, node.ID, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC), true, "", 50)
	if err != nil || len(page.Events) != 1 || page.Events[0].Kind != "configuration_saved" {
		t.Fatalf("reopen lost timeline evidence: %+v %v", page, err)
	}
}
