package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func pauseFixtureChannel(t *testing.T, s *Store, id string, at, until time.Time) NotificationChannel {
	t.Helper()
	c, err := s.NotificationChannel(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteNotificationChannelTest(context.Background(), id, DeliveryOutcome{ChannelRevision: c.DeliveryRevision, ChannelRetryAt: until, ErrorClass: "http_rate_limited"}, at); err != nil {
		t.Fatal(err)
	}
	c, err = s.NotificationChannel(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func addSinglePauseFixture(t *testing.T, s *Store, channel string, at time.Time) NotificationDelivery {
	t.Helper()
	ctx := context.Background()
	n, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "single pause fixture"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.CreateAlertRule(ctx, n.ID, channel, "cpu", []byte(`{"threshold_percent":90}`), 300)
	if err != nil {
		t.Fatal(err)
	}
	i := observe(t, s, r, n, at, true, true)
	jobs, err := s.ListIncidentDeliveries(ctx, i.ID, 0, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs: %v %v", jobs, err)
	}
	return jobs[0]
}

func TestChannelPreSendDeferralsPreserveBudgetAndHistory(t *testing.T) {
	for _, aggregate := range []bool{false, true} {
		t.Run(fmt.Sprint("aggregate=", aggregate), func(t *testing.T) {
			s, members, p, base := aggregateFixture(t, 1)
			ctx := context.Background()
			at := base.Add(30 * time.Second)
			var job NotificationDelivery
			if aggregate {
				jobs, _ := s.ListIncidentDeliveries(ctx, members[0].incident.ID, 0, 10)
				job = jobs[0]
			} else {
				job = addSinglePauseFixture(t, s, p.ChannelID, at)
			}
			for n := 0; n < 27; n++ {
				var g *NotificationAggregate
				var d *NotificationDelivery
				var err error
				if aggregate {
					g = claimAggregate(t, s, at)
				} else {
					d, err = s.ClaimDelivery(ctx, at, time.Minute)
					if err != nil || d == nil {
						t.Fatalf("claim: %v %v", d, err)
					}
				}
				c := pauseFixtureChannel(t, s, p.ChannelID, at, at.Add(time.Second+time.Nanosecond))
				if aggregate {
					_, err = s.PrepareAggregateSend(ctx, g.ID, g.LeaseToken, at)
				} else {
					err = s.CheckDeliveryLease(ctx, d.ID, d.LeaseToken, at, c.DeliveryRevision)
				}
				if !errors.Is(err, ErrDeliveryDeferred) {
					t.Fatal(err)
				}
				saved, err := s.Delivery(ctx, job.ID)
				if err != nil || saved.Status != "pending" || saved.AttemptCount != 0 || saved.AttemptSequence != n+1 || saved.SendAuthorized {
					t.Fatalf("refund: %+v %v", saved, err)
				}
				if aggregate {
					page, err := s.AggregateContext(ctx, g.ID, 0, 25)
					if err != nil || page.Attempts[0].Content != nil || page.Attempts[0].Outcome != "canceled" || page.Attempts[0].Members[0].Included {
						t.Fatal("deferred aggregate acquired send authority")
					}
					if err := s.CompleteAggregate(ctx, g.ID, g.LeaseToken, at, DeliveryOutcome{Delivered: true}); !errors.Is(err, ErrDeliveryLeaseLost) {
						t.Fatal("deferred aggregate accepted fake receipt")
					}
				} else if err := s.CompleteDelivery(ctx, d.ID, d.LeaseToken, at, DeliveryOutcome{Delivered: true}); !errors.Is(err, ErrDeliveryLeaseLost) {
					t.Fatal("deferred single accepted fake receipt")
				}
				if early, err := s.ClaimDelivery(ctx, c.DeliveryPausedUntil.Add(-time.Nanosecond), time.Minute); err != nil || early != nil {
					t.Fatal("single resumed early")
				}
				if early, err := s.ClaimAggregate(ctx, c.DeliveryPausedUntil.Add(-time.Nanosecond), time.Minute); err != nil || early != nil {
					t.Fatal("aggregate resumed early")
				}
				at = at.Add(2 * time.Second)
			}
			// All five actual attempts remain available, even after 27 canceled reservations.
			for attempt := 1; attempt <= 5; attempt++ {
				outcome := DeliveryOutcome{ErrorClass: "delivery_failed", RetryAt: at.Add(time.Second)}
				if aggregate {
					g := claimAggregate(t, s, at)
					send := prepareAggregate(t, s, g, at)
					if send.MaxMemberAttempt != attempt {
						t.Fatal("budget consumed by deferral")
					}
					if _, err := s.PrepareAggregateSend(ctx, g.ID, g.LeaseToken, at); !errors.Is(err, ErrDeliveryLeaseLost) {
						t.Fatal("authorization replay accepted")
					}
					if err := s.CompleteAggregate(ctx, g.ID, g.LeaseToken, at, outcome); err != nil {
						t.Fatal(err)
					}
				} else {
					d, err := s.ClaimDelivery(ctx, at, time.Minute)
					if err != nil || d == nil || d.AttemptCount != attempt {
						t.Fatalf("budget: %v %v", d, err)
					}
					if err := s.CheckDeliveryLease(ctx, d.ID, d.LeaseToken, at); err != nil {
						t.Fatal(err)
					}
					if err := s.CheckDeliveryLease(ctx, d.ID, d.LeaseToken, at); !errors.Is(err, ErrDeliveryLeaseLost) {
						t.Fatal("authorization replay accepted")
					}
					if err := s.CompleteDelivery(ctx, d.ID, d.LeaseToken, at, outcome); err != nil {
						t.Fatal(err)
					}
				}
				at = at.Add(time.Second)
			}
			saved, err := s.Delivery(ctx, job.ID)
			if err != nil || saved.Status != "failed" || saved.AttemptCount != 5 || saved.AttemptSequence != 32 {
				t.Fatalf("final budget: %+v %v", saved, err)
			}
			before := int64(0)
			seen := map[int]bool{}
			for {
				page, err := s.ListDeliveryAttemptPage(ctx, job.ID, before, 5)
				if err != nil {
					t.Fatal(err)
				}
				if len(page.Attempts) > 5 {
					t.Fatal("unbounded history")
				}
				for i, a := range page.Attempts {
					if seen[a.Number] || (i > 0 && page.Attempts[i-1].Number >= a.Number) {
						t.Fatal("duplicate or unordered page")
					}
					seen[a.Number] = true
					if a.Number <= 27 && (a.Outcome != "canceled" || a.ErrorClass != "channel_rate_limited") {
						t.Fatal("unsent history rewritten")
					}
				}
				if page.NextBefore == 0 {
					break
				}
				before = page.NextBefore
			}
			if len(seen) != 32 {
				t.Fatalf("history lost: %d", len(seen))
			}
		})
	}
}

func TestChannelPauseCompletionAndDeferralRollBackAtomically(t *testing.T) {
	for _, aggregate := range []bool{false, true} {
		t.Run(fmt.Sprint(aggregate), func(t *testing.T) {
			s, _, p, base := aggregateFixture(t, 2)
			ctx := context.Background()
			at := base.Add(30 * time.Second)
			c, _ := s.NotificationChannel(ctx, p.ChannelID)
			var finish func(DeliveryOutcome) error
			if aggregate {
				g := claimAggregate(t, s, at)
				prepareAggregate(t, s, g, at)
				finish = func(o DeliveryOutcome) error { return s.CompleteAggregate(ctx, g.ID, g.LeaseToken, at, o) }
			} else {
				addSinglePauseFixture(t, s, p.ChannelID, at)
				d, err := s.ClaimDelivery(ctx, at, time.Minute)
				if err != nil || d == nil {
					t.Fatal(err)
				}
				finish = func(o DeliveryOutcome) error { return s.CompleteDelivery(ctx, d.ID, d.LeaseToken, at, o) }
			}
			if _, err := s.db.Exec(`CREATE TRIGGER fail_pause BEFORE UPDATE OF delivery_paused_until_ms ON notification_channels BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
				t.Fatal(err)
			}
			o := DeliveryOutcome{ErrorClass: "http_rate_limited", RetryAt: at.Add(time.Minute), ChannelRetryAt: at.Add(time.Minute + time.Nanosecond), ChannelRevision: c.DeliveryRevision}
			if err := finish(o); err == nil {
				t.Fatal("partial completion committed")
			}
			var done int
			if err := s.db.QueryRow(`SELECT count(*) FROM delivery_attempts WHERE outcome!='started'`).Scan(&done); err != nil || done != 0 {
				t.Fatal("attempt completion escaped rollback")
			}
			if _, err := s.db.Exec(`DROP TRIGGER fail_pause`); err != nil {
				t.Fatal(err)
			}
			if err := finish(o); err != nil {
				t.Fatal(err)
			}
			c, err := s.NotificationChannel(ctx, p.ChannelID)
			if err != nil || c.DeliveryPausedUntil == nil || !c.DeliveryPausedUntil.Equal(ceilMilliseconds(o.ChannelRetryAt)) {
				t.Fatal("completion did not persist pause")
			}
		})
	}
	t.Run("prepared-member-refund", func(t *testing.T) {
		s, _, p, base := aggregateFixture(t, 2)
		ctx := context.Background()
		at := base.Add(30 * time.Second)
		g := claimAggregate(t, s, at)
		pauseFixtureChannel(t, s, p.ChannelID, at, at.Add(time.Minute))
		if _, err := s.db.Exec(`CREATE TRIGGER fail_second_refund BEFORE UPDATE OF attempt_count ON notification_deliveries WHEN NEW.attempt_count<OLD.attempt_count AND (SELECT count(*) FROM notification_deliveries WHERE status='pending')>0 BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
			t.Fatal(err)
		}
		if _, err := s.PrepareAggregateSend(ctx, g.ID, g.LeaseToken, at); err == nil || errors.Is(err, ErrDeliveryDeferred) {
			t.Fatal("partial refund committed")
		}
		page, err := s.AggregateContext(ctx, g.ID, 0, 25)
		if err != nil || page.Totals.Inflight != 2 || page.Attempts[0].Outcome != "prepared" {
			t.Fatal("refund rollback lost owner")
		}
		if _, err := s.db.Exec(`DROP TRIGGER fail_second_refund`); err != nil {
			t.Fatal(err)
		}
		if _, err := s.PrepareAggregateSend(ctx, g.ID, g.LeaseToken, at); !errors.Is(err, ErrDeliveryDeferred) {
			t.Fatal(err)
		}
	})
}

func TestChannelPauseSurvivesRestartAndFullRestore(t *testing.T) {
	s, _, p, base := aggregateFixture(t, 11)
	ctx := context.Background()
	at := base.Add(30 * time.Second)
	single := addSinglePauseFixture(t, s, p.ChannelID, at)
	g := claimAggregate(t, s, at)
	first := prepareAggregate(t, s, g, at)
	deadline := at.Add(120*time.Second + time.Nanosecond)
	if err := s.CompleteAggregate(ctx, g.ID, g.LeaseToken, at, DeliveryOutcome{ErrorClass: "http_rate_limited", RetryAt: deadline, ChannelRetryAt: deadline, ChannelRevision: first.Channel.DeliveryRevision}); err != nil {
		t.Fatal(err)
	}
	addSinglePauseFixture(t, s, p.ChannelID, at.Add(time.Second))
	backup := filepath.Join(t.TempDir(), "paused.db")
	if err := s.ConsistentBackup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	path := s.DatabasePath()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if g, err := reopened.ClaimAggregate(ctx, deadline.Add(-time.Nanosecond), time.Minute); err != nil || g != nil {
		t.Fatal("restart lost group wait")
	}
	if d, err := reopened.ClaimDelivery(ctx, deadline.Add(-time.Nanosecond), time.Minute); err != nil || d != nil {
		t.Fatal("restart lost single wait")
	}
	replacement := "replacement fixture"
	if _, err := reopened.UpdateNotificationChannel(ctx, p.ChannelID, "new", "webhook", &replacement, true); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := StageDatabaseRestore(ctx, path, backup); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyPendingRestore(ctx, path, at); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	for _, when := range []time.Time{at.Add(time.Second), ceilMilliseconds(deadline).Add(-time.Nanosecond)} {
		if g, err := restored.ClaimAggregate(ctx, when, time.Minute); err != nil || g != nil {
			t.Fatal("restore lost group wait")
		}
		if d, err := restored.ClaimDelivery(ctx, when, time.Minute); err != nil || d != nil {
			t.Fatal("restore lost single wait")
		}
	}
	due := ceilMilliseconds(deadline)
	for n := 0; n < 2; n++ {
		next := claimAggregate(t, restored, due)
		send := prepareAggregate(t, restored, next, due)
		if next.ID == g.ID && (string(send.Payload) != string(first.Payload) || send.IdempotencyKey != first.IdempotencyKey) {
			t.Fatal("pause changed retry identity")
		}
		if err := restored.CompleteAggregate(ctx, next.ID, next.LeaseToken, due, DeliveryOutcome{Delivered: true}); err != nil {
			t.Fatal(err)
		}
	}
	d, err := restored.ClaimDelivery(ctx, due, time.Minute)
	if err != nil || d == nil || d.ID != single.ID || d.AttemptCount != 1 {
		t.Fatalf("due single: %v %v", d, err)
	}
}

func TestChannelPauseVersionAndLateResultFences(t *testing.T) {
	s, _, p, base := aggregateFixture(t, 1)
	ctx := context.Background()
	at := base.Add(30 * time.Second)
	g := claimAggregate(t, s, at)
	old := prepareAggregate(t, s, g, at)
	current := pauseFixtureChannel(t, s, p.ChannelID, at, at.Add(2*time.Minute))
	short := pauseFixtureChannel(t, s, p.ChannelID, at.Add(time.Second), at.Add(time.Minute))
	if !short.DeliveryPausedUntil.Equal(*current.DeliveryPausedUntil) {
		t.Fatal("short response shortened wait")
	}
	if err := s.CompleteNotificationChannelTest(ctx, p.ChannelID, DeliveryOutcome{Delivered: true, ChannelRevision: current.DeliveryRevision}, at); err != nil {
		t.Fatal(err)
	}
	renamed, err := s.UpdateNotificationChannel(ctx, p.ChannelID, "renamed", "webhook", nil, false)
	if err != nil || renamed.DeliveryPausedUntil == nil || renamed.DeliveryRevision != current.DeliveryRevision {
		t.Fatal("metadata edit erased wait")
	}
	changed := "new encrypted fixture"
	replacement, err := s.UpdateNotificationChannel(ctx, p.ChannelID, "replacement", "webhook", &changed, true)
	if err != nil || replacement.DeliveryPausedUntil != nil || replacement.DeliveryRevision != current.DeliveryRevision+1 {
		t.Fatal("replacement retained old wait")
	}
	if err := s.CompleteAggregate(ctx, g.ID, g.LeaseToken, at.Add(2*time.Second), DeliveryOutcome{ErrorClass: "http_rate_limited", RetryAt: at.Add(3 * time.Second), ChannelRetryAt: at.Add(time.Hour), ChannelRevision: old.Channel.DeliveryRevision}); err != nil {
		t.Fatal(err)
	}
	c, _ := s.NotificationChannel(ctx, p.ChannelID)
	if c.DeliveryPausedUntil != nil {
		t.Fatal("old configuration paused replacement")
	}
	next := claimAggregate(t, s, at.Add(3*time.Second))
	prepareAggregate(t, s, next, at.Add(3*time.Second))
	successor := claimAggregate(t, s, at.Add(64*time.Second))
	prepareAggregate(t, s, successor, at.Add(64*time.Second))
	if err := s.CompleteAggregate(ctx, next.ID, next.LeaseToken, at.Add(65*time.Second), DeliveryOutcome{ErrorClass: "http_rate_limited", RetryAt: at.Add(time.Hour), ChannelRetryAt: at.Add(time.Hour), ChannelRevision: c.DeliveryRevision}); err != nil {
		t.Fatal(err)
	}
	c, _ = s.NotificationChannel(ctx, p.ChannelID)
	if c.DeliveryPausedUntil != nil {
		t.Fatal("superseded attempt regained channel-clock authority")
	}
}

func TestChannelPauseFiltersBeforeClaimBatchLimit(t *testing.T) {
	s, _, p, at := aggregateFixture(t, 1)
	ctx := context.Background()
	for n := 0; n < 101; n++ {
		addSinglePauseFixture(t, s, p.ChannelID, at)
	}
	other, err := s.CreateNotificationChannel(ctx, "other", "webhook", "other fixture")
	if err != nil {
		t.Fatal(err)
	}
	want := addSinglePauseFixture(t, s, other.ID, at)
	pauseFixtureChannel(t, s, p.ChannelID, at, at.Add(time.Hour))
	d, err := s.ClaimDelivery(ctx, at, time.Minute)
	if err != nil || d == nil || d.ID != want.ID {
		t.Fatalf("blocked rows starved independent channel: %v %v", d, err)
	}
	// Synthetic empty roots exercise the bounded group scan without claiming capacity.
	for n := 0; n < 101; n++ {
		if _, err := s.db.Exec(`INSERT INTO notification_aggregates(id,policy_id,policy_revision,channel_id,provider,notification_type,starts_at_ms,ends_at_ms,available_at_ms,created_at,updated_at) VALUES(?, 'fixture',1,?,'webhook','firing',0,1,0,?,?)`, fmt.Sprintf("held-root-%d", n), p.ChannelID, formatTime(at), formatTime(at)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`INSERT INTO notification_aggregates(id,policy_id,policy_revision,channel_id,provider,notification_type,starts_at_ms,ends_at_ms,available_at_ms,created_at,updated_at) VALUES('ready-root','fixture',1,?,'webhook','firing',0,1,0,?,?)`, other.ID, formatTime(at), formatTime(at)); err != nil {
		t.Fatal(err)
	}
	// The independent empty root is processed while held roots stay untouched.
	if _, err := s.ClaimAggregate(ctx, at.Add(30*time.Second), time.Minute); err != nil {
		t.Fatal(err)
	}
	var finished int
	if err := s.db.QueryRow(`SELECT count(*) FROM notification_aggregates WHERE status='finished'`).Scan(&finished); err != nil || finished != 1 {
		t.Fatal("held roots consumed the group scan")
	}
}
