package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func idleDeliveryFixture(t *testing.T) *Store {
	t.Helper()
	dir := os.Getenv("MYPROBE_IDLE_CLAIM_TEST_DIR")
	if dir == "" {
		dir = t.TempDir()
	} else if !filepath.IsAbs(dir) {
		t.Fatal("retained test directory must be absolute")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "idle.db")); !os.IsNotExist(err) {
		t.Fatal("idle claim fixture must start with an absent database")
	}
	s, err := Open(context.Background(), filepath.Join(dir, "idle.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func lockIdleDeliveryWriter(t *testing.T, s *Store) func() {
	t.Helper()
	var connections []*sql.Conn
	for range 4 {
		conn, err := s.db.Conn(context.Background())
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
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE nodes SET updated_at=updated_at WHERE 0`); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	var once sync.Once
	release := func() { once.Do(func() { _ = tx.Rollback() }) }
	t.Cleanup(release)
	return release
}

func TestEmptyDeliveryPollDoesNotWaitForWriter(t *testing.T) {
	s := idleDeliveryFixture(t)
	release := lockIdleDeliveryWriter(t, s)
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	job, err := s.ClaimDelivery(ctx, time.Now().UTC(), time.Minute)
	if err != nil || job != nil {
		t.Fatalf("empty queue waited for the unrelated writer: job=%v error=%v", job, err)
	}
}

func TestFutureDeliveryAndLeaseStillBecomeClaimable(t *testing.T) {
	s, rule, node := incidentFixture(t, `{"threshold_percent":90}`)
	at := time.Now().UTC().Truncate(time.Millisecond)
	observe(t, s, rule, node, at, true, true)
	due := at.Add(time.Minute)
	if _, err := s.db.Exec(`UPDATE notification_deliveries SET available_at_ms=?`, due.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	var previous *NotificationDelivery
	for _, step := range []string{"future", "unexpired"} {
		t.Run(step, func(t *testing.T) {
			checkAt := at
			if step == "unexpired" {
				checkAt = due.Add(-time.Millisecond)
			}
			release := lockIdleDeliveryWriter(t, s)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			job, err := s.ClaimDelivery(ctx, checkAt, time.Minute)
			cancel()
			release()
			if err != nil || job != nil {
				t.Fatalf("not-yet-due work requested writer: job=%v error=%v", job, err)
			}
		})
		job, err := s.ClaimDelivery(context.Background(), due, time.Minute)
		if err != nil || job == nil {
			t.Fatalf("due work was skipped: job=%v error=%v", job, err)
		}
		if previous != nil && (job.ID != previous.ID || job.LeaseToken == previous.LeaseToken) {
			t.Fatal("expired lease did not recover the original job with a new token")
		}
		previous = job
		due = *job.LeaseUntil
	}
}
