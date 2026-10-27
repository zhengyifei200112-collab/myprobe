package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAggregateSpecialCharactersFitWireBound(t *testing.T) {
	for _, value := range []string{"<", "&", "\"", "\\", "\u2028"} {
		t.Run(value, func(t *testing.T) {
			at := time.Now().UTC()
			g := NotificationAggregate{ID: strings.Repeat("a", 32), RenderVersion: 2, NotificationOrigin: "https://" + strings.Repeat("a", 504), NotificationType: "firing", EndsAt: at}
			members := []NotificationDelivery{}
			for n := 0; n < 10; n++ {
				raw, _ := json.Marshal(DeliveryNotification{NodeID: strings.Repeat("n", 128), NodeName: strings.Repeat(value, 100), Message: strings.Repeat(value, 100), Kind: "cpu", Timestamp: at})
				members = append(members, NotificationDelivery{IncidentID: strings.Repeat("i", 32), Payload: raw})
			}
			raw, _, err := renderAggregate(g, members)
			if err != nil {
				t.Fatal(err)
			}
			var content AggregateNotification
			if err := json.Unmarshal(raw, &content); err != nil {
				t.Fatal(err)
			}
			if len(raw) > AggregatePayloadByteLimit || len(content.Message) > AggregateMessageByteLimit || len(content.Members) != 10 || content.Members[0].NodeID != membersNodeID() {
				t.Fatal("metadata lost or bound exceeded")
			}
		})
	}
}

func TestAggregatePreparationFailureIsAtomicKnownUnsent(t *testing.T) {
	s, members, _, at := aggregateFixture(t, 11)
	ctx := context.Background()
	g := claimAggregate(t, s, at.Add(30*time.Second))
	selected := []NotificationDelivery{}
	for _, m := range members {
		jobs, err := s.ListIncidentDeliveries(ctx, m.incident.ID, 0, 10)
		if err != nil {
			t.Fatal(err)
		}
		if jobs[0].AggregateID == g.ID {
			selected = append(selected, jobs[0])
		}
	}
	if len(selected) != 10 {
		t.Fatal("fixture shard")
	}
	if _, err := s.db.Exec(`UPDATE notification_deliveries SET payload_json='{"timestamp":"private-render-marker"}' WHERE id=?`, selected[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_preparation BEFORE UPDATE OF status ON notification_deliveries WHEN NEW.status='failed' AND (SELECT count(*) FROM notification_deliveries WHERE status='failed')=1 BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PrepareAggregateSend(ctx, g.ID, g.LeaseToken, at.Add(31*time.Second)); err == nil {
		t.Fatal("partial failure committed")
	}
	page, err := s.AggregateContext(ctx, g.ID, 0, 25)
	if err != nil || page.Totals.Inflight != 10 || page.Attempts[0].Outcome != "prepared" {
		t.Fatalf("rollback: %+v %v", page, err)
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_preparation`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PrepareAggregateSend(ctx, g.ID, g.LeaseToken, at.Add(31*time.Second)); !errors.Is(err, ErrAggregatePreparationFailed) {
		t.Fatal(err)
	}
	page, err = s.AggregateContext(ctx, g.ID, 0, 25)
	if err != nil || page.Aggregate.Status != "finished" || page.Totals.Failed != 10 || page.Attempts[0].Outcome != "failed" || page.Attempts[0].Content != nil || page.Attempts[0].ErrorClass != "aggregate_payload_invalid" {
		t.Fatalf("known unsent: %+v %v", page, err)
	}
	for _, ref := range page.Attempts[0].Members {
		if ref.Included {
			t.Fatal("local failure authorized a member")
		}
	}
	if rowCount(t, s, "alert_events") != 0 {
		t.Fatal("local failure invented provider receipts")
	}
	for _, d := range selected {
		attempts, err := s.ListDeliveryAttempts(ctx, d.ID)
		if err != nil || len(attempts) != 1 || attempts[0].Outcome != "failed" {
			t.Fatal("leaf local failure missing")
		}
	}
	if err := s.CompleteAggregate(ctx, g.ID, g.LeaseToken, at.Add(32*time.Second), DeliveryOutcome{Delivered: true}); !errors.Is(err, ErrDeliveryLeaseLost) {
		t.Fatal("unsent work accepted a fake late success")
	}
	other := claimAggregate(t, s, at.Add(32*time.Second))
	if other.ID == g.ID {
		t.Fatal("local failure blocked other work")
	}
	prepareAggregate(t, s, other, at.Add(32*time.Second))
	if err := s.CompleteAggregate(ctx, other.ID, other.LeaseToken, at.Add(33*time.Second), DeliveryOutcome{Delivered: true}); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(page)
	if strings.Contains(string(raw), "private-render-marker") {
		t.Fatal("rendering detail leaked")
	}
	// Recover an event that never entered an authorized payload: no orphan recovery.
	for _, m := range members {
		if m.incident.ID == selected[0].IncidentID {
			if _, err := s.ObserveAlert(ctx, m.rule, m.node, AlertObservation{At: at.Add(34 * time.Second), Known: true, Active: false}); err != nil {
				t.Fatal(err)
			}
		}
	}
	jobs, err := s.ListIncidentDeliveries(ctx, selected[0].IncidentID, 0, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatal("known-unsent event queued a recovery")
	}
}
func membersNodeID() string { return strings.Repeat("n", 128) }
