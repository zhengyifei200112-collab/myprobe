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
)

func TestAggregateClaimRollbackAndQueueCreationRollback(t *testing.T) {
	s, members, _, at := aggregateFixture(t, 3)
	ctx := context.Background()
	if _, err := s.db.Exec(`CREATE TRIGGER fail_group_member BEFORE INSERT ON notification_aggregate_attempt_members WHEN (SELECT count(*) FROM notification_aggregate_attempt_members)=1 BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimAggregate(ctx, at.Add(30*time.Second), time.Minute); err == nil {
		t.Fatal("partial claim accepted")
	}
	if rowCount(t, s, "delivery_attempts") != 0 || rowCount(t, s, "notification_aggregate_attempts") != 0 || rowCount(t, s, "notification_aggregate_attempt_members") != 0 {
		t.Fatal("partial claim rows")
	}
	for _, m := range members {
		jobs, err := s.ListIncidentDeliveries(ctx, m.incident.ID, 0, 10)
		if err != nil || len(jobs) != 1 || jobs[0].Status != "pending" || jobs[0].AttemptCount != 0 {
			t.Fatal("partial leaf reservation")
		}
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_group_member`); err != nil {
		t.Fatal(err)
	}
	g := claimAggregate(t, s, at.Add(30*time.Second))
	prepareAggregate(t, s, g, at.Add(30*time.Second))
	if err := s.CompleteAggregate(ctx, g.ID, g.LeaseToken, at.Add(31*time.Second), DeliveryOutcome{Delivered: true}); err != nil {
		t.Fatal(err)
	}
	before, err := s.Incident(ctx, members[0].incident.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_group_queue BEFORE INSERT ON notification_deliveries BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ObserveAlert(ctx, members[0].rule, members[0].node, AlertObservation{At: at.Add(400 * time.Second), Known: true, Active: true}); err == nil {
		t.Fatal("queue rollback failure ignored")
	}
	after, err := s.Incident(ctx, before.ID)
	if err != nil || !reflect.DeepEqual(before, after) || rowCount(t, s, "notification_aggregates") != 1 || rowCount(t, s, "notification_deliveries") != 3 {
		t.Fatal("orphan group or mutated clock after queue failure")
	}
}

func TestAggregateRestartAndFullRestoreRetainWindowAndHistory(t *testing.T) {
	s, _, _, at := aggregateFixture(t, 3)
	ctx := context.Background()
	snapshot := filepath.Join(t.TempDir(), "snapshot.db")
	if err := s.ConsistentBackup(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	path := s.DatabasePath()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if g, err := s.ClaimAggregate(ctx, at.Add(29*time.Second), time.Minute); err != nil || g != nil {
		t.Fatal("restart reset deadline")
	}
	g := claimAggregate(t, s, at.Add(30*time.Second))
	first := prepareAggregate(t, s, g, at.Add(30*time.Second))
	if err := s.CompleteAggregate(ctx, g.ID, g.LeaseToken, at.Add(31*time.Second), DeliveryOutcome{Delivered: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := StageDatabaseRestore(ctx, path, snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyPendingRestore(ctx, path, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if rowCount(t, s, "notification_aggregate_attempts") != 0 || rowCount(t, s, "alert_events") != 0 {
		t.Fatal("restore retained newer sends")
	}
	restored := claimAggregate(t, s, at.Add(30*time.Second))
	second := prepareAggregate(t, s, restored, at.Add(30*time.Second))
	if restored.ID != g.ID || first.IdempotencyKey != second.IdempotencyKey || !reflect.DeepEqual(first.Payload, second.Payload) {
		t.Fatal("restore rebuilt a different body or identity")
	}
	if err := s.CompleteAggregate(ctx, restored.ID, restored.LeaseToken, at.Add(31*time.Second), DeliveryOutcome{Delivered: true}); err != nil {
		t.Fatal(err)
	}
	if g, err := s.ClaimAggregate(ctx, at.Add(32*time.Second), time.Minute); err != nil || g != nil {
		t.Fatal("restore completion duplicated queue")
	}
}

func TestAggregateLateResultAfterTakeoverRefinesHistoryOnly(t *testing.T) {
	s, members, _, at := aggregateFixture(t, 2)
	ctx := context.Background()
	old, err := s.ClaimAggregate(ctx, at.Add(30*time.Second), 10*time.Second)
	if err != nil || old == nil {
		t.Fatal(err)
	}
	prepareAggregate(t, s, old, at.Add(30*time.Second))
	next := claimAggregate(t, s, at.Add(40*time.Second))
	prepareAggregate(t, s, next, at.Add(40*time.Second))
	if err := s.CompleteAggregate(ctx, next.ID, next.LeaseToken, at.Add(41*time.Second), DeliveryOutcome{Delivered: true}); err != nil {
		t.Fatal(err)
	}
	before, err := s.Incident(ctx, members[0].incident.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteAggregate(ctx, old.ID, old.LeaseToken, at.Add(42*time.Second), DeliveryOutcome{Delivered: true}); err != nil {
		t.Fatal("real old receipt discarded", err)
	}
	if err := s.CompleteAggregate(ctx, old.ID, old.LeaseToken, at.Add(43*time.Second), DeliveryOutcome{Delivered: true}); err != nil {
		t.Fatal(err)
	}
	after, err := s.Incident(ctx, before.ID)
	if err != nil || !reflect.DeepEqual(before, after) || rowCount(t, s, "alert_events") != 4 {
		t.Fatal("historical receipt changed current clock or duplicated history")
	}
	jobs, err := s.ListIncidentDeliveries(ctx, before.ID, 0, 10)
	if err != nil || len(jobs) != 1 || jobs[0].Status != "delivered" || jobs[0].AttemptCount != 2 {
		t.Fatal("takeover job changed")
	}
	attempts, err := s.ListDeliveryAttempts(ctx, jobs[0].ID)
	if err != nil || len(attempts) != 2 || attempts[0].Outcome != "delivered" || attempts[1].Outcome != "delivered" {
		t.Fatal("real duplicate delivery evidence hidden")
	}
	if err := s.CompleteAggregate(ctx, old.ID, "wrong-token", at.Add(44*time.Second), DeliveryOutcome{Delivered: true}); !errors.Is(err, ErrDeliveryLeaseLost) {
		t.Fatal("wrong owner refined history")
	}
}

func TestAggregationSettingDoesNotRecreateIncident(t *testing.T) {
	s, members, p, at := aggregateFixture(t, 1)
	ctx := context.Background()
	p.Config = json.RawMessage(`{"threshold_percent":90,"recovery_seconds":0,"aggregation_seconds":60}`)
	if _, err := s.SaveAlertPolicy(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncAlertPolicyRules(ctx, at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileIncidents(ctx, at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	current, err := s.Incident(ctx, members[0].incident.ID)
	if err != nil || current.State != "firing" || current.ResolutionReason != "" {
		t.Fatal("transport setting manufactured resolution")
	}
	rules, err := s.ListAlertRules(ctx)
	if err != nil || len(rules) != 1 {
		t.Fatal(err)
	}
	if i, err := s.ObserveAlert(ctx, rules[0], members[0].node, AlertObservation{At: at.Add(2 * time.Second), Known: true, Active: true}); err != nil || i.ID != current.ID {
		t.Fatal("transport setting made a new event")
	}
}

func TestAggregateKeysSeparateBoundaryPolicyChannelProviderAndType(t *testing.T) {
	s, members, p, at := aggregateFixture(t, 1)
	ctx := context.Background()
	call := func(channel, provider, kind string, when time.Time) string {
		t.Helper()
		tx, err := beginAggregateWrite(ctx, s)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		id, _, err := aggregateForNotification(ctx, tx, members[0].rule.ID, channel, provider, kind, when)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		return id
	}
	base := call(p.ChannelID, "webhook", "firing", at.Add(time.Second))
	seen := map[string]bool{base: true}
	other, err := s.CreateNotificationChannel(ctx, "other boundary fixture", "webhook", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{call(other.ID, "webhook", "firing", at.Add(time.Second)), call(p.ChannelID, "discord", "firing", at.Add(time.Second)), call(p.ChannelID, "webhook", "resolved", at.Add(time.Second)), call(p.ChannelID, "webhook", "firing", at.Add(30*time.Second))} {
		if seen[id] {
			t.Fatal("unrelated group keys merged")
		}
		seen[id] = true
	}
	p.Name = "renamed policy"
	if _, err := s.SaveAlertPolicy(ctx, p); err != nil {
		t.Fatal(err)
	}
	if id := call(p.ChannelID, "webhook", "firing", at.Add(time.Second)); seen[id] {
		t.Fatal("policy revision boundary merged")
	}
}

func TestAggregateLeafRetryBudgetDoesNotDiscardDeferredMember(t *testing.T) {
	s, members, _, at := aggregateFixture(t, 2)
	ctx := context.Background()
	observe(t, s, members[1].rule, members[1].node, at.Add(time.Second), false, false)
	for n := 0; n < 5; n++ {
		now := at.Add(time.Duration(30+n*2) * time.Second)
		g := claimAggregate(t, s, now)
		send := prepareAggregate(t, s, g, now)
		if send.MaxMemberAttempt != n+1 {
			t.Fatal("wrong leaf retry budget")
		}
		if err := s.CompleteAggregate(ctx, g.ID, g.LeaseToken, now.Add(time.Second), DeliveryOutcome{ErrorClass: "http_server_error", RetryAt: now.Add(2 * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := s.ListIncidentDeliveries(ctx, members[0].incident.ID, 0, 10)
	if err != nil || len(jobs) != 1 || jobs[0].Status != "failed" || jobs[0].AttemptCount != 5 {
		t.Fatal("leaf retry budget exceeded")
	}
	observe(t, s, members[1].rule, members[1].node, at.Add(46*time.Second), true, true)
	g := claimAggregate(t, s, at.Add(46*time.Second))
	send := prepareAggregate(t, s, g, at.Add(46*time.Second))
	if g.AttemptCount != 6 || send.MaxMemberAttempt != 1 {
		t.Fatal("transport budget consumed fresh deferred member")
	}
	if err := s.CompleteAggregate(ctx, g.ID, g.LeaseToken, at.Add(47*time.Second), DeliveryOutcome{Delivered: true}); err != nil {
		t.Fatal(err)
	}
}

func TestAggregateChannelConfigurationSnapshotChangesRetryKey(t *testing.T) {
	s, _, p, at := aggregateFixture(t, 2)
	ctx := context.Background()
	g := claimAggregate(t, s, at.Add(30*time.Second))
	first := prepareAggregate(t, s, g, at.Add(30*time.Second))
	if err := s.CompleteAggregate(ctx, g.ID, g.LeaseToken, at.Add(31*time.Second), DeliveryOutcome{ErrorClass: "http_server_error", RetryAt: at.Add(40 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	encrypted := "different synthetic encrypted configuration"
	if _, err := s.UpdateNotificationChannel(ctx, p.ChannelID, "changed fixture", "webhook", &encrypted, true); err != nil {
		t.Fatal(err)
	}
	if first.Channel.ConfigEncrypted == encrypted {
		t.Fatal("later change mutated original snapshot")
	}
	next := claimAggregate(t, s, at.Add(40*time.Second))
	second := prepareAggregate(t, s, next, at.Add(40*time.Second))
	if !reflect.DeepEqual(first.Payload, second.Payload) || first.IdempotencyKey == second.IdempotencyKey || second.Channel.ConfigEncrypted != encrypted {
		t.Fatal("configuration change reused identity or lost snapshot")
	}
	raw, err := json.Marshal(second)
	if err != nil || strings.Contains(string(raw), encrypted) {
		t.Fatal("channel secrets exposed by send metadata serialization")
	}
}
