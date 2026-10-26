package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestConcurrentSilenceCreateAndCancel(t *testing.T) {
	for _, sameKey := range []bool{true, false} {
		t.Run(map[bool]string{true: "replay", false: "different-actors"}[sameKey], func(t *testing.T) {
			s, _, _, actor, at, i := silenceFixture(t)
			ctx := context.Background()
			other, err := s.CreateUser(ctx, "other fixture", "synthetic")
			if err != nil {
				t.Fatal(err)
			}
			var winner SilenceMutationResult
			for _, operation := range []string{"create", "cancel"} {
				start := make(chan struct{})
				results := make(chan SilenceMutationResult, 16)
				failures := make(chan error, 16)
				var wg sync.WaitGroup
				for n := range 16 {
					wg.Add(1)
					go func() {
						defer wg.Done()
						<-start
						key, user := operation+"-one", actor.ID
						if !sameKey && n%2 == 1 {
							key, user = operation+"-two", other.ID
						}
						var result SilenceMutationResult
						var err error
						if operation == "create" {
							result, err = s.CreateIncidentSilence(ctx, i.ID, key, user, CreateIncidentSilenceParams{DurationSeconds: 60, Acknowledge: true}, at.Add(time.Second))
						} else {
							result, err = s.CancelIncidentSilence(ctx, i.ID, winner.SilenceID, key, user, 1, at.Add(2*time.Second))
						}
						if err != nil {
							failures <- err
						} else {
							results <- result
						}
					}()
				}
				close(start)
				wg.Wait()
				close(results)
				close(failures)
				for err := range failures {
					wanted := ErrSilenceActive
					if operation == "cancel" {
						wanted = ErrSilenceConflict
					}
					if sameKey || !errors.Is(err, wanted) {
						t.Fatal(err)
					}
				}
				newCount := 0
				for result := range results {
					if !result.Replayed {
						newCount++
					}
					if operation == "create" {
						if winner.SilenceID != "" && winner.SilenceID != result.SilenceID {
							t.Fatal("multiple intervals")
						}
						winner = result
					}
				}
				if newCount != 1 {
					t.Fatalf("%s committed %d actions", operation, newCount)
				}
			}
			if rowCount(t, s, "incident_silences") != 1 || rowCount(t, s, "incident_silence_requests") != 2 || rowCount(t, s, "audit_log") != 3 || rowCount(t, s, "incident_acknowledgements") != 1 {
				t.Fatal("duplicated durable history")
			}
		})
	}
}

func TestSilenceRestartAndFullRestorePreserveFrozenResults(t *testing.T) {
	s, _, _, actor, at, i := silenceFixture(t)
	ctx := context.Background()
	p := CreateIncidentSilenceParams{DurationSeconds: 60, Acknowledge: true, Reason: "backup note"}
	first, err := s.CreateIncidentSilence(ctx, i.ID, "durable", actor.ID, p, at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	cancel, err := s.CancelIncidentSilence(ctx, i.ID, first.SilenceID, "cancel", actor.ID, 1, at.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(t.TempDir(), "snapshot.db")
	if err := s.ConsistentBackup(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	path := s.DatabasePath()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	check := func(s *Store) {
		t.Helper()
		a, err := s.CreateIncidentSilence(ctx, i.ID, "durable", actor.ID, p, at.Add(time.Hour))
		if err != nil || !a.Replayed {
			t.Fatalf("create replay: %+v %v", a, err)
		}
		a.Replayed = false
		b, err := s.CancelIncidentSilence(ctx, i.ID, first.SilenceID, "cancel", actor.ID, 1, at.Add(time.Hour))
		if err != nil || !b.Replayed {
			t.Fatalf("cancel replay: %+v %v", b, err)
		}
		b.Replayed = false
		if !reflect.DeepEqual(a, first) || !reflect.DeepEqual(b, cancel) || rowCount(t, s, "audit_log") != 3 || rowCount(t, s, "delivery_silence_links") != 1 || rowCount(t, s, "incident_silence_decisions") != 1 {
			t.Fatal("frozen history changed")
		}
	}
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	check(s)
	if _, err := s.db.Exec(`DELETE FROM incident_silences`); err != nil {
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
	check(s)
}

func TestSilenceCancelsHeldLeaseAndPreservesLateResult(t *testing.T) {
	s, _, _, actor, at, i := silenceFixture(t)
	ctx := context.Background()
	job, err := s.ClaimDelivery(ctx, at, time.Minute)
	if err != nil || job == nil {
		t.Fatalf("claim: %+v %v", job, err)
	}
	if err := s.CheckDeliveryLease(ctx, job.ID, job.LeaseToken, at); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateIncidentSilence(ctx, i.ID, "create", actor.ID, CreateIncidentSilenceParams{DurationSeconds: 60}, at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckDeliveryLease(ctx, job.ID, job.LeaseToken, at.Add(2*time.Second)); !errors.Is(err, ErrDeliveryCanceled) {
		t.Fatalf("owner check: %v", err)
	}
	if err := s.CheckDeliveryLease(ctx, job.ID, "wrong-token", at.Add(2*time.Second)); !errors.Is(err, ErrDeliveryLeaseLost) {
		t.Fatalf("wrong owner: %v", err)
	}
	before, err := s.Delivery(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteDelivery(ctx, job.ID, job.LeaseToken, at.Add(3*time.Second), DeliveryOutcome{Delivered: true}); err != nil {
		t.Fatal(err)
	}
	after, err := s.Delivery(ctx, job.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("late result revived canceled job")
	}
	attempts, err := s.ListDeliveryAttempts(ctx, job.ID)
	current, incErr := s.Incident(ctx, i.ID)
	if err != nil || incErr != nil || len(attempts) != 1 || attempts[0].Outcome != "delivered" || current.LastEnqueuedAt != nil {
		t.Fatalf("late actual result: %+v %+v %v %v", attempts, current, err, incErr)
	}
}

func TestAdministrativeClosureEndsSilence(t *testing.T) {
	s, rule, _, actor, at, i := silenceFixture(t)
	ctx := context.Background()
	w, err := s.CreateIncidentSilence(ctx, i.ID, "create", actor.ID, CreateIncidentSilenceParams{DurationSeconds: 60}, at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE alert_rules SET enabled=0 WHERE id=?`, rule.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileIncidents(ctx, at.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	ended := readSilence(t, s, w.SilenceID)
	if ended.ClosedAt == nil || ended.ClosureReason != "rule_disabled" || ended.Revision != 2 || rowCount(t, s, "notification_deliveries") != 1 {
		t.Fatalf("closure: %+v", ended)
	}
}

func TestSilenceCancellationFailureRollsBackAndActorCannotReuseIdentity(t *testing.T) {
	s, _, _, actor, at, i := silenceFixture(t)
	ctx := context.Background()
	p := CreateIncidentSilenceParams{DurationSeconds: 60}
	w, err := s.CreateIncidentSilence(ctx, i.ID, "create", actor.ID, p, at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Incident(ctx, i.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"incident_silence_requests", "audit_log"} {
		if _, err := s.db.Exec(`CREATE TRIGGER fail_cancel BEFORE INSERT ON ` + table + ` BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
			t.Fatal(err)
		}
		if _, err := s.CancelIncidentSilence(ctx, i.ID, w.SilenceID, "cancel", actor.ID, 1, at.Add(2*time.Second)); err == nil {
			t.Fatal("cancellation failure ignored")
		}
		after, err := s.Incident(ctx, i.ID)
		interval := readSilence(t, s, w.SilenceID)
		if err != nil || !reflect.DeepEqual(before, after) || interval.Revision != 1 || interval.CanceledAt != nil || rowCount(t, s, "incident_silence_requests") != 1 || rowCount(t, s, "audit_log") != 1 {
			t.Fatal("partial cancellation")
		}
		if _, err := s.db.Exec(`DROP TRIGGER fail_cancel`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.CancelIncidentSilence(ctx, i.ID, w.SilenceID, "cancel", actor.ID, 1, at.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateUser(ctx, "other identity fixture", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateIncidentSilence(ctx, i.ID, "create", other.ID, p, at.Add(time.Hour)); !errors.Is(err, ErrSilenceRequestConflict) {
		t.Fatalf("actor replayed create: %v", err)
	}
	if _, err := s.CancelIncidentSilence(ctx, i.ID, w.SilenceID, "cancel", other.ID, 1, at.Add(time.Hour)); !errors.Is(err, ErrSilenceRequestConflict) {
		t.Fatalf("actor replayed cancel: %v", err)
	}
}
