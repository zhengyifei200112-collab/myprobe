package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestSingleCompletionRechecksAdminStateWithoutEvaluation(t *testing.T) {
	for _, result := range []string{"success", "rejection", "unknown", "rate-limited"} {
		t.Run(result, func(t *testing.T) {
			s, rule, node := incidentFixture(t, `{"threshold_percent":90}`)
			ctx := context.Background()
			at := time.Now().UTC()
			i := observe(t, s, rule, node, at, true, true)
			d, err := s.ClaimDelivery(ctx, at, time.Minute)
			if err != nil || d == nil {
				t.Fatal(err)
			}
			c, err := s.NotificationChannel(ctx, rule.ChannelID)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.CheckDeliveryLease(ctx, d.ID, d.LeaseToken, at, c.DeliveryRevision); err != nil {
				t.Fatal(err)
			}
			if _, err := s.UpdateAlertRule(ctx, rule.ID, node.ID, rule.ChannelID, rule.Kind, rule.Config, false, rule.CooldownSeconds); err != nil {
				t.Fatal(err)
			}
			outcome := DeliveryOutcome{ChannelRevision: c.DeliveryRevision, Delivered: result == "success", Ambiguous: result == "unknown", ErrorClass: "http_server_error", RetryAt: at.Add(time.Minute)}
			if result == "unknown" {
				outcome.ErrorClass = "transport_error"
			}
			if result == "rate-limited" {
				outcome.ErrorClass = "http_rate_limited"
				outcome.ChannelRetryAt = at.Add(2 * time.Minute)
			}
			if err := s.CompleteDelivery(ctx, d.ID, "wrong-token", at.Add(time.Second), outcome); !errors.Is(err, ErrDeliveryLeaseLost) {
				t.Fatal("foreign completion accepted")
			}
			untouched, err := s.Delivery(ctx, d.ID)
			if err != nil || untouched.Status != "inflight" {
				t.Fatal("foreign owner canceled current job")
			}
			if err := s.CompleteDelivery(ctx, d.ID, d.LeaseToken, at.Add(time.Second), outcome); err != nil {
				t.Fatal(err)
			}
			canceled, err := s.Delivery(ctx, d.ID)
			if err != nil || canceled.Status != "canceled" || canceled.ErrorClass != "configuration_changed" || canceled.AttemptCount != 1 {
				t.Fatal("obsolete single retained authority")
			}
			attempts, err := s.ListDeliveryAttempts(ctx, d.ID)
			want := map[string]string{"success": "delivered", "rejection": "failed", "unknown": "unknown", "rate-limited": "failed"}[result]
			if err != nil || len(attempts) != 1 || attempts[0].Outcome != want {
				t.Fatal("receipt lost")
			}
			current, err := s.Incident(ctx, i.ID)
			if err != nil || !reflect.DeepEqual(*i, current) {
				t.Fatal("receipt changed event without evaluation")
			}
			c, err = s.NotificationChannel(ctx, c.ID)
			if err != nil || c.DeliveryPausedUntil != nil {
				t.Fatal("obsolete sender regained channel wait")
			}
			if err := s.CompleteDelivery(ctx, d.ID, d.LeaseToken, at.Add(2*time.Second), outcome); err != nil {
				t.Fatal("identical late result replay failed")
			}
			if rowCount(t, s, "alert_events") != 1 {
				t.Fatal("replay duplicated legacy receipt")
			}
		})
	}
}

func TestAggregateAdministrativeCompletionRollsBackAllMembers(t *testing.T) {
	s, members, p, base := aggregateFixture(t, 2)
	ctx := context.Background()
	at := base.Add(30 * time.Second)
	g := claimAggregate(t, s, at)
	send := prepareAggregate(t, s, g, at)
	p.Enabled = false
	if _, err := s.SaveAlertPolicy(ctx, p); err != nil {
		t.Fatal(err)
	}
	// Fail after the earlier member's cancellation and receipt have been written.
	jobs, err := s.ListIncidentDeliveries(ctx, members[1].incident.ID, 0, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER reject_revoked_receipt BEFORE UPDATE OF outcome ON delivery_attempts WHEN NEW.delivery_id='` + jobs[0].ID + `' AND NEW.outcome='delivered' BEGIN SELECT RAISE(ABORT,'fixture completion failure'); END`); err != nil {
		t.Fatal(err)
	}
	outcome := DeliveryOutcome{Delivered: true}
	if err := s.CompleteAggregate(ctx, g.ID, g.LeaseToken, at.Add(time.Second), outcome); err == nil {
		t.Fatal("forced completion succeeded")
	}
	for _, m := range members {
		jobs, err := s.ListIncidentDeliveries(ctx, m.incident.ID, 0, 10)
		if err != nil || len(jobs) != 1 || jobs[0].Status != "inflight" {
			t.Fatal("partial cancellation committed")
		}
		attempts, err := s.ListDeliveryAttempts(ctx, jobs[0].ID)
		if err != nil || len(attempts) != 1 || attempts[0].Outcome != "started" {
			t.Fatal("partial receipt committed")
		}
		current, err := s.Incident(ctx, m.incident.ID)
		if err != nil || !reflect.DeepEqual(*m.incident, current) {
			t.Fatal("partial clock change")
		}
	}
	if rowCount(t, s, "alert_events") != 0 {
		t.Fatal("partial legacy history")
	}
	detail, err := s.AggregateContext(ctx, g.ID, 0, 25)
	if err != nil || len(detail.Attempts) != 1 || detail.Attempts[0].Outcome != "started" {
		t.Fatal("partial parent completion")
	}
	if _, err := s.db.Exec(`DROP TRIGGER reject_revoked_receipt`); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteAggregate(ctx, g.ID, g.LeaseToken, at.Add(2*time.Second), outcome); err != nil {
		t.Fatal(err)
	}
	detail, err = s.AggregateContext(ctx, g.ID, 0, 25)
	if err != nil || detail.Aggregate.Status != "finished" || detail.Totals.Canceled != 2 || detail.Attempts[0].Outcome != "delivered" || detail.Attempts[0].Content == nil {
		t.Fatal("revoked aggregate lost frozen receipt")
	}
	var frozen, key string
	if err := s.db.QueryRow(`SELECT payload_json,idempotency_key FROM notification_aggregate_attempts WHERE aggregate_id=? AND attempt_number=1`, g.ID).Scan(&frozen, &key); err != nil || frozen != string(send.Payload) || key != send.IdempotencyKey {
		t.Fatal("administrative cancellation rewrote frozen content")
	}
}
