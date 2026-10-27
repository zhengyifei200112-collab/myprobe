package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/notificationlink"
	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
)

func TestHTTPDiagnosticsPaginationCountsRetentionAndRestore(t *testing.T) {
	s, v, nodes, at := httpMaintenanceFixture(t)
	ctx := context.Background()
	end, now := at.Add(4*time.Minute), at.Add(10*time.Minute)
	query := func(after string, limit int) HTTPDiagnostics {
		t.Helper()
		d, err := s.HTTPServiceDiagnostics(ctx, v.ID, nodes[0].ID, at, end, now, limit, after)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	first := query("", 2)
	if first.Statistics.Expected != 8 || first.Statistics.Success != 3 || first.Statistics.Failure != 2 || first.Statistics.Unobserved != 1 || first.Statistics.Missing != 3 || len(first.Observations) != 2 || first.NextCursor == "" || !first.Observer.CurrentlyAssigned {
		t.Fatal("diagnostic counts/page mismatch")
	}
	ids := []string{}
	for page := first; ; page = query(page.NextCursor, 2) {
		for _, row := range page.Observations {
			ids = append(ids, row.TaskID)
		}
		if page.NextCursor == "" {
			break
		}
	}
	all := query("", 100)
	if len(ids) != 6 || len(all.Observations) != len(ids) {
		t.Fatal("missing/duplicate page rows")
	}
	for i, row := range all.Observations {
		if ids[i] != row.TaskID {
			t.Fatal("pagination order")
		}
	}
	body, _ := json.Marshal(all)
	for _, private := range []string{"private-marker", "example.test", "spec", "task_json", "certificates", "assertion"} {
		if strings.Contains(string(body), private) {
			t.Fatal("private diagnostic field")
		}
	}
	for _, change := range []struct {
		service, node string
		start, end    time.Time
		after         string
		limit         int
	}{
		{v.ID, nodes[1].ID, at, end, first.NextCursor, 2}, {v.ID, nodes[0].ID, at.Add(time.Nanosecond), end, first.NextCursor, 2}, {v.ID, nodes[0].ID, at, end, "bad-cursor", 2}, {v.ID, nodes[0].ID, at, end, "", 101}, {v.ID, nodes[0].ID, at, at.Add(32 * 24 * time.Hour), "", 2},
	} {
		if _, err := s.HTTPServiceDiagnostics(ctx, change.service, change.node, change.start, change.end, now, change.limit, change.after); !errors.Is(err, ErrInvalidHTTPService) {
			t.Fatal("invalid context accepted")
		}
	}
	// A surviving historical observer remains queryable after assignment changes.
	v.NodeIDs = []string{nodes[1].ID}
	if _, err := s.SaveHTTPService(ctx, v); err != nil {
		t.Fatal(err)
	}
	removed := query("", 100)
	if removed.Observer.CurrentlyAssigned || len(removed.Observations) != 6 || removed.Observations[0].Revision == removed.Service.Revision {
		t.Fatal("historical assignment/version lost")
	}
	if _, err := s.db.Exec(`UPDATE http_history_state SET retained_from_ns=? WHERE id=1`, at.Add(90*time.Second).UnixNano()); err != nil {
		t.Fatal(err)
	}
	retained := query("", 100)
	if retained.Statistics.Expected != 5 || len(retained.Observations) != 3 || retained.Observations[2].ScheduledAt.Before(at.Add(90*time.Second)) {
		t.Fatal("retention floor ignored")
	}
	backup := filepath.Join(httpFixtureDir(t), "diagnostics.db")
	if err := s.ConsistentBackup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	copy, err := Open(ctx, backup)
	if err != nil {
		t.Fatal(err)
	}
	defer copy.Close()
	restored, err := copy.HTTPServiceDiagnostics(ctx, v.ID, nodes[0].ID, at, end, now, 100, "")
	if err != nil || !reflect.DeepEqual(restored, retained) {
		t.Fatal("restore changed diagnostics")
	}
	if _, err := s.HTTPServiceDiagnostics(ctx, "missing", nodes[0].ID, at, end, now, 2, ""); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing service")
	}
	if _, err := s.HTTPServiceDiagnostics(ctx, v.ID, "missing", at, end, now, 2, ""); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing observer")
	}
}

func TestHTTPDiagnosticsNanosecondsPendingAndSafeEvidence(t *testing.T) {
	s, v, nodes, at := httpMaintenanceFixture(t)
	ctx := context.Background()
	for _, offset := range []time.Duration{0, time.Nanosecond, 10 * time.Nanosecond, 100 * time.Millisecond, 110 * time.Millisecond} {
		point := at.Add(time.Hour + offset)
		if _, err := s.CreateHTTPTask(ctx, v.ID, nodes[0].ID, point, point); err != nil {
			t.Fatal(err)
		}
	}
	start, end := at.Add(time.Hour), at.Add(time.Hour+111*time.Millisecond)
	d, err := s.HTTPServiceDiagnostics(ctx, v.ID, nodes[0].ID, start, end, end, 2, "")
	if err != nil || len(d.Observations) != 2 || d.Observations[0].ScheduledAt.Sub(start) != 110*time.Millisecond || d.Observations[1].ScheduledAt.Sub(start) != 100*time.Millisecond || d.Observations[0].Status != "pending" {
		t.Fatal("nanosecond order/pending")
	}
	next, err := s.HTTPServiceDiagnostics(ctx, v.ID, nodes[0].ID, start, end, end, 100, d.NextCursor)
	if err != nil || len(next.Observations) != 3 || next.Observations[0].ScheduledAt.Sub(start) != 10*time.Nanosecond {
		t.Fatal("nanosecond cursor")
	}
	clipped, err := s.HTTPServiceDiagnostics(ctx, v.ID, nodes[0].ID, start.Add(time.Nanosecond), start.Add(10*time.Nanosecond), end, 100, "")
	if err != nil || len(clipped.Observations) != 1 || clipped.Observations[0].ScheduledAt.Sub(start) != time.Nanosecond {
		t.Fatal("half-open nanosecond bounds")
	}
	first := d.Observations[0]
	for _, sample := range []struct {
		now  time.Time
		want string
	}{{first.ExpiresAt.Add(time.Minute), "pending"}, {first.ExpiresAt.Add(time.Minute + time.Nanosecond), "missing"}} {
		got, err := s.HTTPServiceDiagnostics(ctx, v.ID, nodes[0].ID, start, end, sample.now, 1, "")
		if err != nil || got.Observations[0].Status != sample.want {
			t.Fatal("receipt grace")
		}
	}
	for _, class := range []string{"dns", "refused", "timeout", "tls_invalid", "certificate_expired", "status_mismatch", "content_mismatch", "response_too_large", "redirect_limit", "unsupported", "busy", "cancelled", "invalid_task", "policy_denied", "internal"} {
		r := httpcheck.Result{TaskID: first.TaskID, ServiceID: v.ID, Revision: uint64(first.Revision), ScheduledAt: first.ScheduledAt, CompletedAt: first.ScheduledAt, Outcome: "failure", ErrorClass: class, DurationMS: 2, StatusCode: 503}
		if class == "unsupported" || class == "busy" || class == "cancelled" || class == "invalid_task" || class == "policy_denied" || class == "internal" {
			r.Outcome, r.StatusCode = "unobserved", 0
		}
		body, _ := json.Marshal(r)
		if _, err := s.db.Exec(`UPDATE http_tasks SET result_json=?,received_at=? WHERE id=?`, string(body), formatTime(end), first.TaskID); err != nil {
			t.Fatal(err)
		}
		got, err := s.HTTPServiceDiagnostics(ctx, v.ID, nodes[0].ID, start, end, end, 1, "")
		if err != nil || got.Observations[0].ErrorClass != class || (r.Outcome == "unobserved" && got.Observations[0].DurationMS != nil) {
			t.Fatal("classification/safe projection")
		}
	}
	if _, err := s.db.Exec(`UPDATE http_tasks SET result_json=? WHERE id=?`, `{"outcome":"failure","error_class":"https://private-marker"}`, first.TaskID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HTTPServiceDiagnostics(ctx, v.ID, nodes[0].ID, start, end, end, 1, ""); err == nil {
		t.Fatal("invalid imported evidence accepted")
	}
	var plan string
	rows, err := s.db.Query(`EXPLAIN QUERY PLAN SELECT id FROM http_tasks WHERE service_id=? AND node_id=? AND `+diagnosticTimeKey+`>=? AND `+diagnosticTimeKey+`<? ORDER BY `+diagnosticTimeKey+` DESC,id DESC LIMIT 51`, v.ID, nodes[0].ID, diagnosticKey(start), diagnosticKey(end))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var a, b, c int
		var line string
		if err := rows.Scan(&a, &b, &c, &line); err != nil {
			t.Fatal(err)
		}
		plan += line
	}
	if err := rows.Err(); err != nil || !strings.Contains(plan, "idx_http_diagnostics_page") || strings.Contains(plan, "TEMP B-TREE") {
		t.Fatal("diagnostic index not used")
	}
}

func TestHTTPDiagnosticsPageUsesSingleReadSnapshot(t *testing.T) {
	s, v, nodes, at := httpMaintenanceFixture(t)
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var seen int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes`).Scan(&seen); err != nil {
		t.Fatal(err)
	}
	// Concurrent committed writes must not mix new metadata/retention into old rows.
	writer, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	for _, mutation := range []struct {
		query string
		args  []any
	}{
		{`UPDATE http_services SET name=? WHERE id=?`, []any{"new service alias", v.ID}},
		{`UPDATE nodes SET name=? WHERE id=?`, []any{"new observer alias", nodes[0].ID}},
		{`UPDATE http_history_state SET retained_from_ns=? WHERE id=1`, []any{at.Add(90 * time.Second).UnixNano()}},
	} {
		if _, err := writer.ExecContext(ctx, mutation.query, mutation.args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Commit(); err != nil {
		t.Fatal(err)
	}
	old, err := readHTTPServiceDiagnostics(ctx, tx, v.ID, nodes[0].ID, at, at.Add(4*time.Minute), at.Add(10*time.Minute), 100, "")
	if err != nil || old.Service.Name != v.Name || old.Observer.Name != nodes[0].Name || old.Statistics.Expected != 8 || len(old.Observations) != 6 {
		t.Fatal("mixed diagnostic snapshot")
	}
	fresh, err := s.HTTPServiceDiagnostics(ctx, v.ID, nodes[0].ID, at, at.Add(4*time.Minute), at.Add(10*time.Minute), 100, "")
	if err != nil || fresh.Service.Name != "new service alias" || fresh.Observer.Name != "new observer alias" || fresh.Statistics.Expected != 5 || len(fresh.Observations) != 3 {
		t.Fatal("fresh page did not see committed changes")
	}
}

func TestServiceDiagnosticLinkCapturedAcrossRetryAndFullRestore(t *testing.T) {
	s, v, _, node, _ := serviceAlertFixture(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Millisecond).Add(time.Second)
	if _, err := s.PrepareAlertPolicyRules(ctx, at); err != nil {
		t.Fatal(err)
	}
	rules, err := s.ListAlertRules(ctx)
	if err != nil || len(rules) != 1 {
		t.Fatal("missing service rule")
	}
	i, err := s.ObserveAlert(ctx, rules[0], node, AlertObservation{At: at, Known: true, Active: true, NotificationOrigin: "https://original.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.ClaimDelivery(ctx, at, time.Minute)
	if err != nil || job == nil {
		t.Fatal("missing service delivery")
	}
	var payload DeliveryNotification
	if json.Unmarshal(job.Payload, &payload) != nil || payload.ServiceDiagnosticPath != notificationlink.ServiceDiagnosticAt("https://original.example.test", v.ID, node.ID, at) || payload.IncidentPath != notificationlink.Incident("https://original.example.test", i.ID) {
		t.Fatal("captured context mismatch")
	}
	if strings.Contains(string(job.Payload), v.Spec.URL) {
		t.Fatal("private URL in payload")
	}
	if err := s.CompleteDelivery(ctx, job.ID, job.LeaseToken, at, DeliveryOutcome{Ambiguous: true, ErrorClass: "transport_error", RetryAt: at.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(httpFixtureDir(t), "captured.db")
	if err := s.ConsistentBackup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	copy, err := Open(ctx, backup)
	if err != nil {
		t.Fatal(err)
	}
	defer copy.Close()
	retry, err := copy.ClaimDelivery(ctx, at.Add(time.Second), time.Minute)
	if err != nil || retry == nil || retry.IdempotencyKey != job.IdempotencyKey || string(retry.Payload) != string(job.Payload) {
		t.Fatal("restore changed captured payload/key")
	}
}
