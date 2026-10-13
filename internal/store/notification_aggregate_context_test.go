package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAggregateContextExactMembersAndRedaction(t *testing.T) {
	s, members, _, at := aggregateFixture(t, 2)
	ctx := context.Background()
	g := claimAggregate(t, s, at.Add(30*time.Second))
	page, err := s.AggregateContext(ctx, g.ID, 0, 25)
	if err != nil || page.Totals.Inflight != 2 || len(page.Attempts) != 1 || page.Attempts[0].Content != nil || page.Attempts[0].Outcome != "prepared" {
		t.Fatalf("prepared context: %+v %v", page, err)
	}
	for _, m := range page.Attempts[0].Members {
		if m.Included {
			t.Fatal("prepared membership shown as sent")
		}
	}
	if _, err := s.ObserveAlert(ctx, members[0].rule, members[0].node, AlertObservation{At: at.Add(31 * time.Second), Known: true, Active: false}); err != nil {
		t.Fatal(err)
	}
	send := prepareAggregate(t, s, g, at.Add(31*time.Second))
	if err := s.CompleteAggregate(ctx, g.ID, g.LeaseToken, at.Add(32*time.Second), DeliveryOutcome{Delivered: true}); err != nil {
		t.Fatal(err)
	}
	page, err = s.AggregateContext(ctx, g.ID, 0, 25)
	if err != nil || page.Aggregate.Status != "finished" || page.Totals.Delivered != 1 || page.Totals.Canceled != 1 || len(page.Attempts[0].Content.Members) != 1 {
		t.Fatalf("mixed outcomes: %+v %v", page, err)
	}
	included := 0
	for _, m := range page.Attempts[0].Members {
		if m.Included {
			included++
		}
	}
	if included != 1 || len(page.Attempts[0].Members) != 2 {
		t.Fatal("exact membership missing")
	}
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{g.LeaseToken, send.IdempotencyKey, "lease_token", "lease_until", "idempotency_key", "config_encrypted", "payload_json", "synthetic", "notification_origin", "render_version", "template_event_time", "TemplateEventTime"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("private field exposed: %s", secret)
		}
	}
}

func TestAggregateContextUnknownRetryAndPagination(t *testing.T) {
	s, _, _, at := aggregateFixture(t, 1)
	ctx := context.Background()
	g := claimAggregate(t, s, at.Add(30*time.Second))
	first := prepareAggregate(t, s, g, at.Add(30*time.Second))
	retry := claimAggregate(t, s, at.Add(90*time.Second))
	second := prepareAggregate(t, s, retry, at.Add(90*time.Second))
	if string(first.Payload) != string(second.Payload) || first.IdempotencyKey != second.IdempotencyKey {
		t.Fatal("unchanged content changed")
	}
	if err := s.CompleteAggregate(ctx, retry.ID, retry.LeaseToken, at.Add(91*time.Second), DeliveryOutcome{Delivered: true}); err != nil {
		t.Fatal(err)
	}
	page, err := s.AggregateContext(ctx, g.ID, 0, 1)
	if err != nil || len(page.Attempts) != 1 || page.Attempts[0].Number != 2 || page.NextBefore != 2 || page.Totals.Delivered != 1 {
		t.Fatalf("first page: %+v %v", page, err)
	}
	older, err := s.AggregateContext(ctx, g.ID, page.NextBefore, 1)
	if err != nil || len(older.Attempts) != 1 || older.Attempts[0].Number != 1 || older.Attempts[0].Outcome != "unknown" || older.NextBefore != 0 {
		t.Fatalf("older page: %+v %v", older, err)
	}
	if older.Attempts[0].Content.SnapshotID == "" || older.Attempts[0].Content.SnapshotID != page.Attempts[0].Content.SnapshotID || !older.Attempts[0].Members[0].Included {
		t.Fatal("unknown actual payload lost")
	}
	if _, err := s.AggregateContext(ctx, "missing", 0, 1); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	for _, q := range []struct {
		id            string
		before, limit int
	}{{"bad/id", 0, 1}, {g.ID, -1, 1}, {g.ID, 0, 0}, {g.ID, 0, 101}} {
		if _, err := s.AggregateContext(ctx, q.id, q.before, q.limit); !errors.Is(err, ErrInvalidAggregateQuery) {
			t.Fatal(err)
		}
	}
}

func TestAggregateListSequenceAndEmptyArray(t *testing.T) {
	s, _, _, _ := aggregateFixture(t, 11)
	ctx := context.Background()
	page, err := s.ListNotificationAggregates(ctx, 0, 1)
	if err != nil || len(page.Aggregates) != 1 || page.NextBefore == 0 || page.Aggregates[0].ChannelName != "fixture" {
		t.Fatalf("first list: %+v %v", page, err)
	}
	older, err := s.ListNotificationAggregates(ctx, page.NextBefore, 1)
	if err != nil || len(older.Aggregates) != 1 || older.NextBefore != 0 || older.Aggregates[0].Seq >= page.Aggregates[0].Seq || older.Aggregates[0].Totals.Pending+page.Aggregates[0].Totals.Pending != 11 {
		t.Fatalf("next list: %+v %v", older, err)
	}
	empty, err := s.ListNotificationAggregates(ctx, older.Aggregates[0].Seq, 1)
	if err != nil || empty.Aggregates == nil || len(empty.Aggregates) != 0 {
		t.Fatalf("empty list: %+v %v", empty, err)
	}
	if _, err := s.ListNotificationAggregates(ctx, -1, 1); !errors.Is(err, ErrInvalidAggregateQuery) {
		t.Fatal(err)
	}
}

func TestAggregateRenderingKeepsLegacyVersionAndBoundsNewLinks(t *testing.T) {
	at := time.Now().UTC().Truncate(time.Second)
	origin := "https://" + strings.Repeat("a", 504) // 512 bytes, synthetic maximum-length configuration.
	members := []NotificationDelivery{}
	for n := 0; n < 10; n++ {
		p, _ := json.Marshal(DeliveryNotification{NodeID: "node", NodeName: strings.Repeat("界", 100), Message: strings.Repeat("界", 100), Timestamp: at, Kind: "cpu"})
		members = append(members, NotificationDelivery{IncidentID: strings.Repeat("a", 128), Payload: p})
	}
	g := NotificationAggregate{ID: strings.Repeat("b", 128), EndsAt: at, NotificationType: "firing", RenderVersion: 2, NotificationOrigin: origin}
	raw, _, err := renderAggregate(g, members)
	if err != nil {
		t.Fatal(err)
	}
	var content AggregateNotification
	if err := json.Unmarshal(raw, &content); err != nil {
		t.Fatal(err)
	}
	if len(content.Message) > AggregateMessageByteLimit || len(raw) > AggregatePayloadByteLimit || len(content.SnapshotID) != 64 || !strings.HasPrefix(content.GroupPath, origin+"/admin?") || !strings.HasPrefix(content.Members[0].EventPath, origin+"/admin?") {
		t.Fatal("new link bounds or routing")
	}
	g.RenderVersion = 1
	g.NotificationOrigin = "https://ignored.example.com"
	// Legacy IDs are server-generated and bounded; use the original short fixture.
	g.ID = "group"
	members = members[:1]
	members[0].IncidentID = "incident"
	old, key, err := renderAggregate(g, members)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(old), "snapshot_id") || strings.Contains(string(old), "group_path") || strings.Contains(string(old), "ignored.example.com") {
		t.Fatal("legacy payload rewritten")
	}
	g.NotificationOrigin = ""
	same, sameKey, err := renderAggregate(g, members)
	if err != nil || string(old) != string(same) || key != sameKey {
		t.Fatal("legacy retry changed")
	}
}

func TestAggregateOriginIsFrozenAndSeparatesWindows(t *testing.T) {
	s, _, _, at := aggregateFixture(t, 1)
	ctx := context.Background()
	created := []Node{}
	for n := 0; n < 4; n++ {
		node, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "origin fixture"})
		if err != nil {
			t.Fatal(err)
		}
		created = append(created, node)
	}
	if err := s.SyncAlertPolicyRules(ctx, at); err != nil {
		t.Fatal(err)
	}
	rules, err := s.ListAlertRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	origins := []string{"https://first.example.com/", "https://first.example.com", "https://second.example.com", "https://secret-marker@invalid.example.com"}
	for n, node := range created {
		for _, rule := range rules {
			if rule.NodeID != node.ID {
				continue
			}
			_, err := s.ObserveAlert(ctx, rule, node, AlertObservation{At: at.Add(time.Second), Known: true, Active: true, NotificationOrigin: origins[n]})
			if n == 3 {
				if err == nil {
					t.Fatal("invalid origin queued")
				}
			} else if err != nil {
				t.Fatal(err)
			}
		}
	}
	if rowCount(t, s, "notification_aggregates") != 3 || rowCount(t, s, "notification_deliveries") != 4 || rowCount(t, s, "alert_incidents") != 4 {
		t.Fatal("origin grouping or invalid-origin rollback")
	}
	g := claimAggregate(t, s, at.Add(31*time.Second))
	send := prepareAggregate(t, s, g, at.Add(31*time.Second))
	var first AggregateNotification
	if err := json.Unmarshal(send.Payload, &first); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first.GroupPath, "/admin?") {
		t.Fatal("existing relative origin rewritten")
	}
	if err := s.CompleteAggregate(ctx, g.ID, g.LeaseToken, at.Add(32*time.Second), DeliveryOutcome{Delivered: true}); err != nil {
		t.Fatal(err)
	}
	g = claimAggregate(t, s, at.Add(32*time.Second))
	send = prepareAggregate(t, s, g, at.Add(32*time.Second))
	if err := json.Unmarshal(send.Payload, &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Members) != 2 || !strings.HasPrefix(first.GroupPath, "https://first.example.com/admin?") {
		t.Fatal("normalized origin members or frozen origin lost")
	}
}

func TestAggregateLinkMigrationPreservesLegacyRetry(t *testing.T) {
	s, _, _, at := aggregateFixture(t, 1)
	ctx := context.Background()
	if _, err := s.db.Exec(`UPDATE notification_aggregates SET render_version=1`); err != nil {
		t.Fatal(err)
	}
	g := claimAggregate(t, s, at.Add(30*time.Second))
	old := prepareAggregate(t, s, g, at.Add(30*time.Second))
	if err := s.CompleteAggregate(ctx, g.ID, g.LeaseToken, at.Add(31*time.Second), DeliveryOutcome{ErrorClass: "temporary", RetryAt: at.Add(40 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	// Recreate the exact additive-column boundary in this disposable fixture. No
	// production migration is reversed; Migrate must add defaults to old rows.
	for _, query := range []string{`ALTER TABLE notification_aggregates DROP COLUMN notification_origin`, `ALTER TABLE notification_aggregates DROP COLUMN render_version`, `DELETE FROM schema_migrations WHERE version='031_aggregate_notification_links.sql'`} {
		if _, err := s.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	retry := claimAggregate(t, s, at.Add(40*time.Second))
	again := prepareAggregate(t, s, retry, at.Add(40*time.Second))
	if retry.RenderVersion != 1 || retry.NotificationOrigin != "" || string(old.Payload) != string(again.Payload) || old.IdempotencyKey != again.IdempotencyKey {
		t.Fatal("migration rewrote existing transport content or key")
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
}
