package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func legacyIncidentStore(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() >= "015_incident_delivery.sql" {
			continue
		}
		raw, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, string(raw)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, "INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)", entry.Name(), nowText()); err != nil {
			t.Fatal(err)
		}
	}
	return &Store{db: db, path: path}
}

func TestLegacyRetryPreservesRepeatOverrideAndMilliseconds(t *testing.T) {
	s := legacyIncidentStore(t)
	ctx := context.Background()
	node, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "legacy retry"})
	if err != nil {
		t.Fatal(err)
	}
	channel, err := s.CreateNotificationChannel(ctx, "legacy", ChannelKindWebhook, "placeholder")
	if err != nil {
		t.Fatal(err)
	}
	rule, err := s.CreateAlertRule(ctx, node.ID, channel.ID, "cpu", json.RawMessage(`{"threshold_percent":90,"repeat_seconds":120}`), 900)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 1, 12, 0, 0, 456000000, time.UTC)
	if err := s.RecordAlertAttempt(ctx, rule.ID, node.ID, "cpu:"+rule.ID+":"+node.ID, "fault", true, false, "legacy error", at); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	items, err := s.ListIncidents(ctx, IncidentFilter{Limit: 10})
	if err != nil || len(items) != 1 {
		t.Fatalf("incidents: %+v %v", items, err)
	}
	jobs, err := s.ListIncidentDeliveries(ctx, items[0].ID, 0, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs: %+v %v", jobs, err)
	}
	want := at.Add(120 * time.Second)
	if !jobs[0].AvailableAt.Equal(want) {
		t.Fatalf("deadline=%v want=%v", jobs[0].AvailableAt, want)
	}
	// Exercise reopening the upgraded database and the runtime policy cutover,
	// not just the incident schema migration in isolation.
	path := s.path
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for attempt, expected := range []int{1, 0} {
		if mapped, err := s.PrepareAlertPolicyRules(ctx, at.Add(time.Duration(attempt+1)*time.Second)); err != nil || mapped != expected {
			t.Fatalf("policy cutover %d: mapped=%d err=%v", attempt, mapped, err)
		}
	}
	if err := s.ReconcileIncidents(ctx, at.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	current, err := s.Incident(ctx, items[0].ID)
	if err != nil || current.State != "firing" || current.RuleID != rule.ID || !sameSnapshot(current.RuleSnapshot, items[0].RuleSnapshot) {
		t.Fatalf("cutover changed upgraded incident: %+v %v", current, err)
	}
	policies, err := s.ListAlertPolicies(ctx)
	if err != nil || len(policies) != 1 || policies[0].ID != rule.ID {
		t.Fatalf("legacy policy identity: %+v %v", policies, err)
	}
	var timing struct {
		Recovery *int `json:"recovery_seconds"`
	}
	if err := json.Unmarshal(policies[0].Config, &timing); err != nil || timing.Recovery == nil || *timing.Recovery != 0 {
		t.Fatal("policy cutover changed legacy immediate recovery", err)
	}
	continued, err := s.ListIncidentDeliveries(ctx, current.ID, 0, 10)
	if err != nil || len(continued) != 1 || continued[0].ID != jobs[0].ID || !continued[0].AvailableAt.Equal(want) || string(continued[0].Payload) != string(jobs[0].Payload) {
		t.Fatalf("cutover changed upgraded retry: %+v %v", continued, err)
	}
	if job, err := s.ClaimDelivery(ctx, want.Add(-time.Millisecond), time.Minute); err != nil || job != nil {
		t.Fatalf("early claim: %+v %v", job, err)
	}
	job, err := s.ClaimDelivery(ctx, want, time.Minute)
	if err != nil || job == nil || job.ID != jobs[0].ID || job.AttemptCount != 2 {
		t.Fatalf("continued retry: %+v %v", job, err)
	}
}
func TestIncidentMigrationPreservesLegacyStatesWithoutMassResend(t *testing.T) {
	s := legacyIncidentStore(t)
	ctx := context.Background()
	node, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "legacy node"})
	if err != nil {
		t.Fatal(err)
	}
	channel, err := s.CreateNotificationChannel(ctx, "legacy channel", ChannelKindWebhook, "encrypted-test-placeholder")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	states := []struct {
		name                                 string
		active, delivered, pending, disabled bool
		want                                 string
		queued                               bool
	}{
		{name: "active sent", active: true, delivered: true, want: "firing"},
		{name: "active failed", active: true, want: "firing", queued: true},
		{name: "pending", pending: true, want: "pending"},
		{name: "resolved sent", delivered: true, want: "absent"},
		{name: "resolved failed", want: "resolved", queued: true},
		{name: "disabled active", active: true, delivered: true, disabled: true, want: "resolved"},
	}
	ruleIDs := map[string]string{}
	for _, test := range states {
		rule, err := s.CreateAlertRule(ctx, node.ID, channel.ID, "expiry", json.RawMessage(`{"days_before":1,"duration_seconds":60}`), 900)
		if err != nil {
			t.Fatal(err)
		}
		ruleIDs[test.name] = rule.ID
		fingerprint := "expiry:" + rule.ID + ":" + node.ID
		if test.pending {
			if err := s.SetAlertPending(ctx, rule.ID, node.ID, fingerprint, "pending fault", &now, now); err != nil {
				t.Fatal(err)
			}
		} else {
			message := ""
			if !test.delivered {
				message = "old provider error with secret-like text"
			}
			if err := s.RecordAlertAttempt(ctx, rule.ID, node.ID, fingerprint, "fault description", test.active, test.delivered, message, now); err != nil {
				t.Fatal(err)
			}
		}
		if test.disabled {
			if _, err := s.db.ExecContext(ctx, "UPDATE alert_rules SET enabled=0 WHERE id=?", rule.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if mapped, err := s.PrepareAlertPolicyRules(ctx, now.Add(time.Second)); err != nil || mapped != len(states) {
		t.Fatalf("legacy state policy cutover: %d %v", mapped, err)
	}
	if err := s.ReconcileIncidents(ctx, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	for _, test := range states {
		t.Run(test.name, func(t *testing.T) {
			var id, state, origin, snapshot string
			var approximate int
			err := s.db.QueryRowContext(ctx, "SELECT id,state,origin,approximate_start,rule_snapshot_json FROM alert_incidents WHERE rule_id=?", ruleIDs[test.name]).Scan(&id, &state, &origin, &approximate, &snapshot)
			if test.want == "absent" {
				if err != sql.ErrNoRows {
					t.Fatalf("invented history: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if state != test.want || origin != "legacy" || approximate != 1 {
				t.Fatalf("state=%s origin=%s approximate=%d", state, origin, approximate)
			}
			var captured struct {
				Config struct {
					Recovery *int `json:"recovery_seconds"`
				} `json:"config"`
			}
			if err := json.Unmarshal([]byte(snapshot), &captured); err != nil || captured.Config.Recovery == nil || *captured.Config.Recovery != 0 {
				t.Fatalf("legacy recovery changed: %s", snapshot)
			}
			var jobs int
			if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM notification_deliveries WHERE incident_id=?", id).Scan(&jobs); err != nil {
				t.Fatal(err)
			}
			want := 0
			if test.queued {
				want = 1
			}
			if jobs != want {
				t.Fatalf("jobs=%d want=%d", jobs, want)
			}
			if test.queued {
				var count int
				var available int64
				var payload, class string
				if err := s.db.QueryRowContext(ctx, "SELECT attempt_count,available_at_ms,payload_json,error_class FROM notification_deliveries WHERE incident_id=?", id).Scan(&count, &available, &payload, &class); err != nil {
					t.Fatal(err)
				}
				if count != 1 || available != now.Add(900*time.Second).UnixMilli() || class != "legacy_delivery_failed" {
					t.Fatalf("retry=%d %d %s", count, available, class)
				}
				if strings.Contains(payload, "secret-like") || strings.Contains(payload, "encrypted-test") {
					t.Fatal("legacy errors or credentials copied into payload")
				}
			}
		})
	}
	var incidents, jobs, attempts, history int
	for table, destination := range map[string]*int{"alert_incidents": &incidents, "notification_deliveries": &jobs, "delivery_attempts": &attempts, "alert_events": &history} {
		if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(destination); err != nil {
			t.Fatal(err)
		}
	}
	if incidents != 5 || jobs != 2 || attempts != 2 || history != 5 {
		t.Fatalf("counts=%d/%d/%d/%d", incidents, jobs, attempts, history)
	}
	// Applying migrations again and reopening do not import a second copy.
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, s.path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var again int
	if err := reopened.db.QueryRowContext(ctx, "SELECT count(*) FROM alert_incidents").Scan(&again); err != nil || again != incidents {
		t.Fatalf("reopen count=%d err=%v", again, err)
	}
	// Historical snapshots survive deletion of the source rule/node.
	if err := s.DeleteNode(ctx, node.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM alert_incidents").Scan(&again); err != nil || again != incidents {
		t.Fatal("node deletion erased incident history")
	}
}

func TestIncidentSchemaRejectsDuplicateActiveStateAndInvalidLease(t *testing.T) {
	s, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	insert := `INSERT INTO alert_incidents(id,fingerprint,rule_id,node_id,node_name,kind,rule_snapshot_json,state,started_at,updated_at) VALUES(?,?,'rule','node','name','cpu','{}','pending',?,?)`
	now := nowText()
	if _, err := s.db.Exec(insert, "first", "one", now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(insert, "second", "one", now, now); err == nil {
		t.Fatal("duplicate active incident accepted")
	}
	if _, err := s.db.Exec("UPDATE alert_incidents SET state='resolved',resolved_at=? WHERE id='first'", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(insert, "second", "one", now, now); err != nil {
		t.Fatal("new incident after resolution rejected")
	}
	for index, fields := range []struct {
		status       string
		token, until any
		attempts     int
	}{
		{status: "inflight", attempts: 1},
		{status: "pending", token: "orphan", attempts: 1},
		{status: "pending", attempts: 6},
	} {
		_, err := s.db.Exec(`INSERT INTO notification_deliveries(id,incident_id,channel_id,channel_name,provider,notification_type,idempotency_key,payload_json,status,attempt_count,available_at_ms,lease_token,lease_until_ms,created_at,updated_at) VALUES(?,'second','channel','channel','webhook','firing',?,'{}',?,?,0,?,?,?,?)`, fmt.Sprint(index), fmt.Sprint(index), fields.status, fields.attempts, fields.token, fields.until, now, now)
		if err == nil {
			t.Fatalf("invalid lease accepted: %+v", fields)
		}
	}
}
