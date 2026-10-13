package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func restorePreTemplateTimeSchema(t *testing.T, s *Store) {
	t.Helper()
	// Recreate only the pre-032 boundary in a disposable database.
	for _, query := range []string{`ALTER TABLE notification_aggregates DROP COLUMN template_event_time`, `DELETE FROM schema_migrations WHERE version='032_aggregate_template_time.sql'`} {
		if _, err := s.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestAggregateTemplateTimeMigrationPreservesLegacyRetry(t *testing.T) {
	s, _, _, at := aggregateFixture(t, 1)
	ctx := context.Background()
	g := claimAggregate(t, s, at.Add(30*time.Second))
	old := prepareAggregate(t, s, g, at.Add(30*time.Second))
	if g.TemplateEventTime != g.EndsAt.Local().Format("2006-01-02 15:04:05") || old.TemplateEventTime != g.TemplateEventTime {
		t.Fatal("new template clock was not captured")
	}
	if err := s.CompleteAggregate(ctx, g.ID, g.LeaseToken, at.Add(31*time.Second), DeliveryOutcome{ErrorClass: "temporary", RetryAt: at.Add(40 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	restorePreTemplateTimeSchema(t, s)
	retry := claimAggregate(t, s, at.Add(40*time.Second))
	again := prepareAggregate(t, s, retry, at.Add(40*time.Second))
	if retry.TemplateEventTime != "" || again.TemplateEventTime != "" || string(old.Payload) != string(again.Payload) || old.IdempotencyKey != again.IdempotencyKey {
		t.Fatal("migration rewrote old transport content or identity")
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestNewMembersDoNotAppendUnfrozenLegacyClock(t *testing.T) {
	s, _, _, at := aggregateFixture(t, 1)
	ctx := context.Background()
	restorePreTemplateTimeSchema(t, s)
	node, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "new clock fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SyncAlertPolicyRules(ctx, at); err != nil {
		t.Fatal(err)
	}
	rules, err := s.ListAlertRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range rules {
		if rule.NodeID == node.ID {
			if _, err := s.ObserveAlert(ctx, rule, node, AlertObservation{At: at.Add(time.Second), Known: true, Active: true}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if rowCount(t, s, "notification_aggregates") != 2 {
		t.Fatal("new member reused the legacy unfrozen clock")
	}
	old := claimAggregate(t, s, at.Add(30*time.Second))
	if old.TemplateEventTime != "" {
		t.Fatal("legacy clock changed")
	}
	prepareAggregate(t, s, old, at.Add(30*time.Second))
	newGroup := claimAggregate(t, s, at.Add(31*time.Second))
	newSend := prepareAggregate(t, s, newGroup, at.Add(31*time.Second))
	if newGroup.ID == old.ID || newSend.TemplateEventTime == "" || newSend.TemplateEventTime != newGroup.EndsAt.Local().Format("2006-01-02 15:04:05") {
		t.Fatal("new group lost its captured clock")
	}
	for _, value := range []any{newGroup, newSend} {
		raw, err := json.Marshal(value)
		if err != nil || strings.Contains(string(raw), "TemplateEventTime") || strings.Contains(string(raw), "template_event_time") || strings.Contains(string(raw), newSend.TemplateEventTime) {
			t.Fatal("private transport clock serialized")
		}
	}
}
