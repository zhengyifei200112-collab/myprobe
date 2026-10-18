package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func lockNotificationFixtureWriter(t *testing.T, s *Store) func() {
	t.Helper()
	ctx := context.Background()
	// Warm the pool so this test isolates idle-claim write reservations rather
	// than opening a new connection while an unrelated transaction owns WAL.
	connections := []*sql.Conn{}
	for range 4 {
		conn, err := s.db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, conn)
	}
	for _, conn := range connections {
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE nodes SET updated_at=updated_at WHERE 0`); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	var once sync.Once
	release := func() { once.Do(func() { _ = tx.Rollback() }) }
	t.Cleanup(release)
	return release
}

func TestIdleNotificationPollingKeepsHealthAvailable(t *testing.T) {
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "health.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	release := lockNotificationFixtureWriter(t, s)
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	start := make(chan struct{})
	errors := make(chan error, 4)
	for i := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if i%2 == 0 {
				job, err := s.ClaimAggregate(ctx, time.Now().UTC(), time.Minute)
				if err == nil && job != nil {
					err = fmt.Errorf("empty aggregate queue produced a job")
				}
				errors <- err
			} else {
				job, err := s.ClaimDelivery(ctx, time.Now().UTC(), time.Minute)
				if err == nil && job != nil {
					err = fmt.Errorf("empty single queue produced a job")
				}
				errors <- err
			}
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	close(start)
	deadline := time.Now().Add(time.Second)
	for s.db.Stats().InUse < 4 && time.Now().Before(deadline) {
		select {
		case <-done:
			deadline = time.Time{}
		default:
			time.Sleep(time.Millisecond)
		}
	}
	before := s.db.Stats()
	healthCtx, stop := context.WithTimeout(context.Background(), 250*time.Millisecond)
	healthErr := s.Health(healthCtx)
	stop()
	release()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("idle claim consumers did not stop after releasing the fixture writer")
	}
	if healthErr != nil {
		t.Fatalf("idle claimers starved health: %v; pool before=%+v", healthErr, before)
	}
	for range 4 {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
}

func TestIdleNotificationFutureAndUnexpiredJobsDoNotTakeWriter(t *testing.T) {
	t.Run("single", func(t *testing.T) {
		s, rule, node := incidentFixture(t, `{"threshold_percent":90}`)
		ctx := context.Background()
		at := time.Now().UTC().Truncate(time.Millisecond)
		observe(t, s, rule, node, at, true, true)
		due := at.Add(time.Minute)
		if _, err := s.db.Exec(`UPDATE notification_deliveries SET available_at_ms=?`, due.UnixMilli()); err != nil {
			t.Fatal(err)
		}
		release := lockNotificationFixtureWriter(t, s)
		limited, cancel := context.WithTimeout(ctx, time.Second)
		job, err := s.ClaimDelivery(limited, at, time.Minute)
		cancel()
		release()
		if err != nil || job != nil {
			t.Fatal("future single job requested writer", job, err)
		}
		job, err = s.ClaimDelivery(ctx, due, time.Minute)
		if err != nil || job == nil {
			t.Fatal("due single job was skipped", err)
		}
		release = lockNotificationFixtureWriter(t, s)
		limited, cancel = context.WithTimeout(ctx, time.Second)
		early, err := s.ClaimDelivery(limited, job.LeaseUntil.Add(-time.Millisecond), time.Minute)
		cancel()
		release()
		if err != nil || early != nil {
			t.Fatal("unexpired single lease requested writer", early, err)
		}
		next, err := s.ClaimDelivery(ctx, *job.LeaseUntil, time.Minute)
		if err != nil || next == nil || next.ID != job.ID || next.LeaseToken == job.LeaseToken {
			t.Fatal("expired single lease was skipped", next, err)
		}
	})
	t.Run("aggregate", func(t *testing.T) {
		s, _, _, at := aggregateFixture(t, 1)
		ctx := context.Background()
		release := lockNotificationFixtureWriter(t, s)
		limited, cancel := context.WithTimeout(ctx, time.Second)
		job, err := s.ClaimAggregate(limited, at, time.Minute)
		cancel()
		release()
		if err != nil || job != nil {
			t.Fatal("future aggregate requested writer", job, err)
		}
		job, err = s.ClaimAggregate(ctx, at.Add(30*time.Second), time.Minute)
		if err != nil || job == nil {
			t.Fatal("due aggregate was skipped", err)
		}
		release = lockNotificationFixtureWriter(t, s)
		limited, cancel = context.WithTimeout(ctx, time.Second)
		early, err := s.ClaimAggregate(limited, job.LeaseUntil.Add(-time.Millisecond), time.Minute)
		cancel()
		release()
		if err != nil || early != nil {
			t.Fatal("unexpired aggregate requested writer", early, err)
		}
		next, err := s.ClaimAggregate(ctx, *job.LeaseUntil, time.Minute)
		if err != nil || next == nil || next.ID != job.ID || next.LeaseToken == job.LeaseToken {
			t.Fatal("expired aggregate lease was skipped", next, err)
		}
	})
	t.Run("system unexpired", func(t *testing.T) {
		s, _, at := systemNotificationFixture(t, "webhook", 1)
		ctx := context.Background()
		job, err := s.ClaimSystemNotification(ctx, at)
		if err != nil || job == nil {
			t.Fatal("system job was skipped", err)
		}
		release := lockNotificationFixtureWriter(t, s)
		limited, cancel := context.WithTimeout(ctx, time.Second)
		early, err := s.ClaimSystemNotification(limited, job.LeaseUntil.Add(-time.Millisecond))
		cancel()
		release()
		if err != nil || early != nil {
			t.Fatal("unexpired system lease requested writer", early, err)
		}
		next, err := s.ClaimSystemNotification(ctx, job.LeaseUntil)
		if err != nil || next == nil || next.ID != job.ID || next.LeaseToken == job.LeaseToken || next.AttemptCount != 1 {
			t.Fatal("unsent expired system reservation was skipped or consumed its budget", next, err)
		}
	})
}

func TestIdleSystemFuturePendingStillCancelsStaleChannel(t *testing.T) {
	s, channel, at := systemNotificationFixture(t, "webhook", 1)
	// Model persisted future work with a stale destination, including imports
	// from older versions. ClaimSystemNotification cancels this even before due.
	if _, err := s.db.Exec(`UPDATE system_notifications SET available_at_ms=?`, at.Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE notification_channels SET delivery_revision=delivery_revision+1 WHERE id=?`, channel); err != nil {
		t.Fatal(err)
	}
	job, err := s.ClaimSystemNotification(context.Background(), at)
	if err != nil || job != nil {
		t.Fatal("stale future system destination was claimed", job, err)
	}
	rows, err := s.SystemNotifications(context.Background())
	if err != nil || len(rows) != 1 || rows[0].Status != "canceled" || rows[0].ErrorClass != "channel_configuration_changed" {
		t.Fatal("negative preflight skipped future destination cancellation", rows, err)
	}
}

func TestIdleNotificationClaimsDoNotWaitForUnrelatedWriter(t *testing.T) {
	for _, kind := range []string{"single", "aggregate", "system"} {
		t.Run(kind, func(t *testing.T) {
			s, err := Open(context.Background(), filepath.Join(t.TempDir(), "idle.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { s.Close() })
			release := lockNotificationFixtureWriter(t, s)
			defer release()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			now := time.Now().UTC()
			if kind == "single" {
				if job, err := s.ClaimDelivery(ctx, now, time.Minute); err != nil || job != nil {
					t.Fatalf("empty queue waited for the writer: %+v %v", job, err)
				}
			} else if kind == "aggregate" {
				if job, err := s.ClaimAggregate(ctx, now, time.Minute); err != nil || job != nil {
					t.Fatalf("empty aggregate queue waited for the writer: %+v %v", job, err)
				}
			} else if job, err := s.ClaimSystemNotification(ctx, now); err != nil || job != nil {
				t.Fatalf("empty system notification queue waited for the writer: %+v %v", job, err)
			}
		})
	}
}
