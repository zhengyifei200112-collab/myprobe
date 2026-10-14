package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNotificationQueueDiagnosticsSnapshotPrivacyAndNoMutation(t *testing.T) {
	s, _, _, at := aggregateFixture(t, 3)
	ctx := context.Background()
	initial := s.NotificationQueueDiagnostics(ctx, at)
	if initial.Status != "available" || initial.Deliveries.Pending != 3 || initial.Deliveries.AggregateMembers != 3 || initial.Aggregates.Pending != 1 || initial.Aggregates.Collecting != 1 || initial.Aggregates.PendingDueByTime != 0 {
		t.Fatalf("initial queue: %+v %+v", initial.Deliveries, initial.Aggregates)
	}
	var group string
	if err := s.db.QueryRow(`SELECT id FROM notification_aggregates`).Scan(&group); err != nil {
		t.Fatal(err)
	}
	// Persist several real row states, including an expired lease. Reading must
	// leave the lease/frozen payload and channel wait untouched.
	if _, err := s.db.Exec(`UPDATE notification_deliveries SET available_at_ms=? WHERE seq=(SELECT MIN(seq) FROM notification_deliveries)`, at.Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE notification_deliveries SET status='inflight',lease_token='synthetic-lease-secret',lease_until_ms=? WHERE seq=(SELECT MAX(seq) FROM notification_deliveries)`, at.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE notification_aggregates SET status='inflight',lease_token='synthetic-group-lease',lease_until_ms=?,sealed=1`, at.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE notification_channels SET delivery_paused_until_ms=?`, at.Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	state := func() string {
		var value string
		if err := s.db.QueryRow(`SELECT json_object('status',status,'sealed',sealed,'lease',lease_token,'until',lease_until_ms,'available',available_at_ms) FROM notification_aggregates WHERE id=?`, group).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := state()
	got := s.NotificationQueueDiagnostics(ctx, at.Add(time.Minute))
	if got.Status != "available" || got.Deliveries.Pending != 2 || got.Deliveries.PendingDueByTime != 1 || got.Deliveries.PendingFuture != 1 || got.Deliveries.Inflight != 1 || got.Deliveries.ExpiredLeases != 1 || got.Deliveries.AggregateMembers != 3 || got.Aggregates.Inflight != 1 || got.Aggregates.ExpiredLeases != 1 {
		t.Fatalf("counts: %+v %+v", got.Deliveries, got.Aggregates)
	}
	if before != state() {
		t.Fatal("diagnostic changed aggregate state")
	}
	assertCount(t, s, `SELECT COUNT(*) FROM notification_deliveries WHERE status='inflight' AND lease_token='synthetic-lease-secret'`, 1)
	assertCount(t, s, `SELECT COUNT(*) FROM notification_channels WHERE delivery_paused_until_ms IS NOT NULL`, 1)
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{group, "synthetic-lease-secret", "synthetic-group-lease", "payload_json", "channel_id", "界"} {
		if strings.Contains(string(encoded), private) {
			t.Fatal("queue diagnostic exposed private material")
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	unavailable := s.NotificationQueueDiagnostics(canceled, at)
	if unavailable.Status != "unavailable" || unavailable.Deliveries != nil || unavailable.Aggregates != nil {
		t.Fatal("failed read invented zero queue")
	}
	if _, err := s.db.Exec(`DELETE FROM notification_deliveries;DELETE FROM notification_aggregates`); err != nil {
		t.Fatal(err)
	}
	empty := s.NotificationQueueDiagnostics(ctx, at)
	if empty.Status != "available" || empty.Deliveries.Pending != 0 || empty.Aggregates.Inflight != 0 || empty.Deliveries.OldestPendingAvailableMS != nil {
		t.Fatal("empty queue is not known zero")
	}
}
