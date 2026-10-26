package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestDeliveryRetryDoesNotRoundDeadlineDown(t *testing.T) {
	for _, fraction := range []time.Duration{0, time.Nanosecond, 999999 * time.Nanosecond} {
		t.Run(fraction.String(), func(t *testing.T) {
			s, rule, node := incidentFixture(t, `{"threshold_percent":90}`)
			ctx := context.Background()
			at := time.Now().UTC().Truncate(time.Second)
			observe(t, s, rule, node, at, true, true)
			job, err := s.ClaimDelivery(ctx, at, time.Minute)
			if err != nil || job == nil {
				t.Fatalf("claim: %+v %v", job, err)
			}
			deadline := at.Add(120*time.Second + fraction)
			if err := s.CompleteDelivery(ctx, job.ID, job.LeaseToken, at.Add(time.Second), DeliveryOutcome{ErrorClass: "http_rate_limited", RetryAt: deadline}); err != nil {
				t.Fatal(err)
			}
			saved, err := s.Delivery(ctx, job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if saved.AvailableAt.Before(deadline) || saved.AvailableAt.Sub(deadline) >= time.Millisecond {
				t.Fatalf("deadline %s stored as %s", deadline, saved.AvailableAt)
			}
			if early, err := s.ClaimDelivery(ctx, deadline.Add(-time.Nanosecond), time.Minute); err != nil || early != nil {
				t.Fatalf("early retry: %+v %v", early, err)
			}
			if due, err := s.ClaimDelivery(ctx, saved.AvailableAt, time.Minute); err != nil || due == nil {
				t.Fatalf("due retry: %+v %v", due, err)
			}
		})
	}
}

func TestDeliveryLeaseExpiryAndStaleCompletion(t *testing.T) {
	s, rule, node := incidentFixture(t, `{"threshold_percent":90}`)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Second)
	observe(t, s, rule, node, at, true, true)
	first, err := s.ClaimDelivery(ctx, at, time.Minute)
	if err != nil || first == nil {
		t.Fatalf("claim %v %v", first, err)
	}
	reopened, err := Open(ctx, s.path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if job, err := reopened.ClaimDelivery(ctx, at.Add(59*time.Second), time.Minute); err != nil || job != nil {
		t.Fatal("unexpired lease stolen")
	}
	next, err := reopened.ClaimDelivery(ctx, at.Add(time.Minute), time.Minute)
	if err != nil || next == nil || next.AttemptCount != 2 || next.LeaseToken == first.LeaseToken {
		t.Fatal("expired lease not recovered")
	}
	if err := s.CompleteDelivery(ctx, first.ID, first.LeaseToken, at.Add(61*time.Second), DeliveryOutcome{Delivered: true}); !errors.Is(err, ErrDeliveryLeaseLost) {
		t.Fatalf("old completion=%v", err)
	}
	if err := reopened.CheckDeliveryLease(ctx, next.ID, next.LeaseToken, at.Add(61*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := reopened.CompleteDelivery(ctx, next.ID, next.LeaseToken, at.Add(62*time.Second), DeliveryOutcome{Delivered: true}); err != nil {
		t.Fatal(err)
	}
	saved, err := s.Delivery(ctx, next.ID)
	if err != nil || saved.Status != "delivered" || saved.AttemptCount != 2 {
		t.Fatalf("saved=%+v %v", saved, err)
	}
	var outcome string
	if err := s.db.QueryRow("SELECT outcome FROM delivery_attempts WHERE delivery_id=? AND attempt_number=1", first.ID).Scan(&outcome); err != nil || outcome != "unknown" {
		t.Fatal("expired attempt lost ambiguity")
	}
	if rowCount(t, s, "alert_events") != 1 {
		t.Fatal("stale worker created delivery history")
	}
}
func TestConcurrentDeliveryClaimAndRetryBudget(t *testing.T) {
	s, rule, node := incidentFixture(t, `{"threshold_percent":90}`)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Second)
	observe(t, s, rule, node, at, true, true)
	jobs := make(chan *NotificationDelivery, 4)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := s.ClaimDelivery(ctx, at, time.Minute)
			if err != nil {
				t.Error(err)
			}
			jobs <- d
		}()
	}
	wg.Wait()
	close(jobs)
	var job *NotificationDelivery
	claims := 0
	for d := range jobs {
		if d != nil {
			job = d
			claims++
		}
	}
	if claims != 1 {
		t.Fatalf("claims=%d", claims)
	}
	for attempt := 1; attempt <= 5; attempt++ {
		now := at.Add(time.Duration(attempt-1) * 31 * time.Second)
		if attempt > 1 {
			var err error
			job, err = s.ClaimDelivery(ctx, now, time.Minute)
			if err != nil || job == nil {
				t.Fatal("retry missing")
			}
		}
		if err := s.CompleteDelivery(ctx, job.ID, job.LeaseToken, now, DeliveryOutcome{ErrorClass: "delivery_failed", RetryAt: now.Add(30 * time.Second)}); err != nil {
			t.Fatal(err)
		}
		if attempt < 5 {
			if early, err := s.ClaimDelivery(ctx, now.Add(29*time.Second), time.Minute); err != nil || early != nil {
				t.Fatal("retry ignored deadline")
			}
		}
	}
	saved, err := s.Delivery(ctx, job.ID)
	if err != nil || saved.Status != "failed" || saved.AttemptCount != 5 {
		t.Fatal("retry budget not enforced")
	}
	if d, err := s.ClaimDelivery(ctx, at.Add(time.Hour), time.Minute); err != nil || d != nil {
		t.Fatal("sixth attempt claimed")
	}
	if rowCount(t, s, "delivery_attempts") != 5 {
		t.Fatal("attempt history incomplete")
	}
}
func TestDeliveryFinalStateCheckAndPermanentFailure(t *testing.T) {
	for _, change := range []string{"disabled", "resolved", "permanent"} {
		t.Run(change, func(t *testing.T) {
			s, rule, node := incidentFixture(t, `{"threshold_percent":90}`)
			ctx := context.Background()
			at := time.Now().UTC().Truncate(time.Second)
			observe(t, s, rule, node, at, true, true)
			job, err := s.ClaimDelivery(ctx, at, time.Minute)
			if err != nil || job == nil {
				t.Fatal("claim failed")
			}
			switch change {
			case "disabled":
				if _, err := s.db.Exec("UPDATE notification_channels SET enabled=0 WHERE id=?", rule.ChannelID); err != nil {
					t.Fatal(err)
				}
				if err := s.CheckDeliveryLease(ctx, job.ID, job.LeaseToken, at.Add(time.Second)); !errors.Is(err, ErrDeliveryCanceled) {
					t.Fatalf("check=%v", err)
				}
			case "resolved":
				observe(t, s, rule, node, at.Add(time.Second), true, false)
				if err := s.CheckDeliveryLease(ctx, job.ID, job.LeaseToken, at.Add(2*time.Second)); !errors.Is(err, ErrDeliveryLeaseLost) {
					t.Fatal("resolved firing remains sendable")
				}
			case "permanent":
				if err := s.CompleteDelivery(ctx, job.ID, job.LeaseToken, at.Add(time.Second), DeliveryOutcome{Permanent: true, ErrorClass: "authentication_failed"}); err != nil {
					t.Fatal(err)
				}
				if d, err := s.ClaimDelivery(ctx, at.Add(time.Hour), time.Minute); err != nil || d != nil {
					t.Fatal("permanent error retried")
				}
			}
		})
	}
}
func TestDeliveryCompletionFailureRollsBack(t *testing.T) {
	s, rule, node := incidentFixture(t, `{"threshold_percent":90}`)
	ctx := context.Background()
	at := time.Now().UTC()
	observe(t, s, rule, node, at, true, true)
	job, err := s.ClaimDelivery(ctx, at, time.Minute)
	if err != nil || job == nil {
		t.Fatal("claim failed")
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_legacy_history BEFORE INSERT ON alert_events BEGIN SELECT RAISE(FAIL,'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteDelivery(ctx, job.ID, job.LeaseToken, at.Add(time.Second), DeliveryOutcome{Delivered: true}); err == nil {
		t.Fatal("expected storage failure")
	}
	saved, err := s.Delivery(ctx, job.ID)
	if err != nil || saved.Status != "inflight" {
		t.Fatal("completion partially committed")
	}
	var outcome string
	if err := s.db.QueryRow("SELECT outcome FROM delivery_attempts WHERE delivery_id=?", job.ID).Scan(&outcome); err != nil || outcome != "started" {
		t.Fatal("attempt partially completed")
	}
}

func TestDeliveryFreshnessIsRecheckedBeforeNetworkSend(t *testing.T) {
	s, rule, node := incidentFixture(t, `{"threshold_percent":90}`)
	ctx := context.Background()
	at := time.Now().UTC()
	observe(t, s, rule, node, at, true, true)
	job, err := s.ClaimDelivery(ctx, at, time.Minute)
	if err != nil || job == nil {
		t.Fatal("claim failed")
	}
	observe(t, s, rule, node, at.Add(time.Second), false, false)
	if err := s.CheckDeliveryLease(ctx, job.ID, job.LeaseToken, at.Add(2*time.Second)); !errors.Is(err, ErrDeliveryDeferred) {
		t.Fatalf("stale claimed job remains sendable: %v", err)
	}
	if job, err := s.ClaimDelivery(ctx, at.Add(time.Hour), time.Minute); err != nil || job != nil {
		t.Fatal("stale pending job claimed")
	}
	observe(t, s, rule, node, at.Add(time.Hour+time.Second), true, true)
	if job, err := s.ClaimDelivery(ctx, at.Add(time.Hour+2*time.Second), time.Minute); err != nil || job == nil {
		t.Fatal("fresh observation did not resume delivery")
	}
}
