package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCanceledAttemptOutcomeRefinesHistoryWithoutChangingJob(t *testing.T) {
	for _, tc := range []struct {
		name    string
		outcome DeliveryOutcome
		want    string
	}{
		{"ack", DeliveryOutcome{Delivered: true}, "delivered"},
		{"rejected", DeliveryOutcome{ErrorClass: "http_client_error", Permanent: true}, "failed"},
		{"timeout", DeliveryOutcome{ErrorClass: "transport_error", Ambiguous: true}, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, rule, node := incidentFixture(t, `{"threshold_percent":90}`)
			ctx := context.Background()
			at := time.Now().UTC().Truncate(time.Second)
			i := observe(t, s, rule, node, at, true, true)
			old, err := s.ClaimDelivery(ctx, at, time.Minute)
			if err != nil || old == nil {
				t.Fatalf("claim: %+v %v", old, err)
			}
			w, err := s.CreateMaintenanceWindow(ctx, MaintenanceWindow{Reason: "work", Timezone: "UTC", CreatedBy: "fixture", NodeIDs: []string{node.ID}, StartsAt: at.Add(time.Second), EndsAt: at.Add(time.Minute)})
			if err != nil {
				t.Fatal(err)
			}
			observe(t, s, rule, node, at.Add(2*time.Second), true, true)
			canceled, err := s.Delivery(ctx, old.ID)
			if err != nil || canceled.Status != "canceled" {
				t.Fatalf("cancellation: %+v %v", canceled, err)
			}
			// A new summary may finish while the older network operation is returning.
			if err := s.CancelMaintenanceWindow(ctx, w.ID, 1, at.Add(3*time.Second)); err != nil {
				t.Fatal(err)
			}
			observe(t, s, rule, node, at.Add(4*time.Second), true, true)
			summary, err := s.ClaimDelivery(ctx, at.Add(4*time.Second), time.Minute)
			if err != nil || summary == nil || summary.ID == old.ID {
				t.Fatalf("summary: %+v %v", summary, err)
			}
			if err := s.CompleteDelivery(ctx, summary.ID, summary.LeaseToken, at.Add(5*time.Second), DeliveryOutcome{Delivered: true}); err != nil {
				t.Fatal(err)
			}
			if err := s.CompleteDelivery(ctx, old.ID, "wrong-token", at.Add(20*time.Second), tc.outcome); !errors.Is(err, ErrDeliveryLeaseLost) {
				t.Fatalf("wrong token: %v", err)
			}
			if err := s.CompleteDelivery(ctx, old.ID, old.LeaseToken, at.Add(20*time.Second), tc.outcome); err != nil {
				t.Fatal(err)
			}
			if err := s.CompleteDelivery(ctx, old.ID, old.LeaseToken, at.Add(21*time.Second), tc.outcome); err != nil {
				t.Fatalf("same outcome replay: %v", err)
			}
			final, err := s.Delivery(ctx, old.ID)
			if err != nil || final.Status != "canceled" || final.ErrorClass != "maintenance" || final.AttemptCount != canceled.AttemptCount || !final.UpdatedAt.Equal(canceled.UpdatedAt) || final.FinishedAt == nil || !final.FinishedAt.Equal(*canceled.FinishedAt) {
				t.Fatalf("job resurrected/changed: %+v %v", final, err)
			}
			attempts, err := s.ListDeliveryAttempts(ctx, old.ID)
			if err != nil || len(attempts) != 1 || attempts[0].Outcome != tc.want || attempts[0].CompletedAt == nil || !attempts[0].CompletedAt.Equal(at.Add(20*time.Second)) {
				t.Fatalf("attempt: %+v %v", attempts, err)
			}
			current, err := s.Incident(ctx, i.ID)
			if err != nil || current.LastEnqueuedAt == nil || !current.LastEnqueuedAt.Equal(at.Add(5*time.Second)) {
				t.Fatalf("late outcome changed repeat clock: %+v %v", current, err)
			}
			if count := rowCount(t, s, "alert_events"); count != 2 {
				t.Fatalf("legacy completion history duplicated/missing: %d", count)
			}
			if extra, err := s.ClaimDelivery(ctx, at.Add(25*time.Second), time.Minute); err != nil || extra != nil {
				t.Fatalf("canceled retry revived: %+v %v", extra, err)
			}
			if err := s.CompleteDelivery(ctx, old.ID, old.LeaseToken, at.Add(26*time.Second), DeliveryOutcome{ErrorClass: "different_error"}); !errors.Is(err, ErrDeliveryLeaseLost) {
				t.Fatalf("conflicting outcome overwrite: %v", err)
			}
		})
	}
}

func TestCanceledLateOutcomeIsAtomicAndRejectsSupersededLease(t *testing.T) {
	s, rule, node := incidentFixture(t, `{"threshold_percent":90}`)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Second)
	observe(t, s, rule, node, at, true, true)
	first, err := s.ClaimDelivery(ctx, at, time.Minute)
	if err != nil || first == nil {
		t.Fatalf("first: %+v %v", first, err)
	}
	second, err := s.ClaimDelivery(ctx, at.Add(time.Minute), time.Minute)
	if err != nil || second == nil {
		t.Fatalf("second: %+v %v", second, err)
	}
	if _, err := s.CreateMaintenanceWindow(ctx, MaintenanceWindow{Reason: "work", Timezone: "UTC", CreatedBy: "fixture", NodeIDs: []string{node.ID}, StartsAt: at.Add(61 * time.Second), EndsAt: at.Add(120 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckDeliveryLease(ctx, second.ID, second.LeaseToken, at.Add(62*time.Second)); !errors.Is(err, ErrDeliveryCanceled) {
		t.Fatalf("pre-send: %v", err)
	}
	if err := s.CompleteDelivery(ctx, first.ID, first.LeaseToken, at.Add(63*time.Second), DeliveryOutcome{Delivered: true}); !errors.Is(err, ErrDeliveryLeaseLost) {
		t.Fatalf("superseded completion accepted: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER fail_late_history BEFORE INSERT ON alert_events BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteDelivery(ctx, second.ID, second.LeaseToken, at.Add(64*time.Second), DeliveryOutcome{Delivered: true}); err == nil {
		t.Fatal("history failure accepted")
	}
	attempts, err := s.ListDeliveryAttempts(ctx, second.ID)
	if err != nil || len(attempts) != 2 || attempts[1].Outcome != "unknown" {
		t.Fatalf("partial refinement: %+v %v", attempts, err)
	}
	if _, err := s.db.ExecContext(ctx, `DROP TRIGGER fail_late_history`); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteDelivery(ctx, second.ID, second.LeaseToken, at.Add(65*time.Second), DeliveryOutcome{Delivered: true}); err != nil {
		t.Fatal(err)
	}
	attempts, err = s.ListDeliveryAttempts(ctx, second.ID)
	if err != nil || attempts[0].Outcome != "unknown" || attempts[1].Outcome != "delivered" {
		t.Fatalf("attempt ownership changed: %+v %v", attempts, err)
	}
}
