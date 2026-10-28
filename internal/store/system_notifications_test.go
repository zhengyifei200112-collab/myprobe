package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func systemNotificationFixture(t *testing.T, kind string, count int) (*Store, string, time.Time) {
	t.Helper()
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	channel, err := db.CreateNotificationChannel(ctx, "synthetic system channel", kind, "synthetic encrypted configuration")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 14, 1, 0, 0, 0, time.UTC)
	for i := 0; i < count; i++ {
		run, err := db.BeginBackupRun(ctx, "", []string{channel.ID}, at)
		if err != nil {
			t.Fatal(err)
		}
		if err = db.FinishBackupRun(ctx, run.ID, "failed", "backup_insufficient_space", nil, at); err != nil {
			t.Fatal(err)
		}
	}
	return db, channel.ID, at
}
func authorizeSystemJob(t *testing.T, db *Store, n *SystemNotification, at time.Time) {
	t.Helper()
	ok, err := db.AuthorizeSystemNotification(context.Background(), n.ID, n.LeaseToken, n.ChannelRevision, at)
	if err != nil || !ok {
		t.Fatalf("authorization: %v %v", ok, err)
	}
}
func TestSystemNotificationSharedPauseAndConfigurationFence(t *testing.T) {
	db, channel, at := systemNotificationFixture(t, "smtp", 2)
	ctx := context.Background()
	first, err := db.ClaimSystemNotification(ctx, at)
	if err != nil || first == nil || first.Provider != "smtp" {
		t.Fatalf("provider claim: %+v %v", first, err)
	}
	authorizeSystemJob(t, db, first, at)
	outcome := DeliveryOutcome{ErrorClass: "http_rate_limited", RetryAt: at.Add(2 * time.Minute), ChannelRetryAt: at.Add(time.Minute), ChannelRevision: first.ChannelRevision}
	if err = db.CompleteSystemNotification(ctx, first.ID, first.LeaseToken, at, outcome); err != nil {
		t.Fatal(err)
	}
	if err = db.CheckNotificationChannelSend(ctx, channel, first.ChannelRevision, at.Add(time.Second)); !errors.Is(err, ErrChannelRateLimited) {
		t.Fatal("system wait not shared with ordinary sends")
	}
	if next, err := db.ClaimSystemNotification(ctx, at.Add(time.Second)); err != nil || next != nil {
		t.Fatalf("shared wait bypass: %+v %v", next, err)
	}
	second, err := db.ClaimSystemNotification(ctx, at.Add(time.Minute))
	if err != nil || second == nil || second.ID == first.ID {
		t.Fatal("unrelated due reminder not claimed after shared wait")
	}
	replacement := "replacement encrypted configuration"
	if _, err = db.UpdateNotificationChannel(ctx, channel, "updated", "smtp", &replacement, true); err != nil {
		t.Fatal(err)
	}
	if allowed, err := db.AuthorizeSystemNotification(ctx, second.ID, second.LeaseToken, second.ChannelRevision, at.Add(time.Minute)); err != nil || allowed {
		t.Fatal("replaced channel was authorized")
	}
	rows, err := db.SystemNotifications(ctx)
	if err != nil || rows[0].Status != "canceled" {
		t.Fatalf("configuration history: %+v %v", rows, err)
	}
}
func TestSystemNotificationExpiredAuthorizationIsNeverRetried(t *testing.T) {
	db, _, at := systemNotificationFixture(t, "webhook", 1)
	ctx := context.Background()
	unsent, err := db.ClaimSystemNotification(ctx, at)
	if err != nil || unsent == nil {
		t.Fatal(err)
	}
	retried, err := db.ClaimSystemNotification(ctx, at.Add(time.Minute))
	if err != nil || retried == nil || retried.ID != unsent.ID || retried.LeaseToken == unsent.LeaseToken || retried.AttemptCount != 1 {
		t.Fatalf("unsent recovery: %+v %v", retried, err)
	}
	authorizeSystemJob(t, db, retried, at.Add(time.Minute))
	before := db.NotificationQueueDiagnostics(ctx, at.Add(2*time.Minute))
	if before.Status != "available" || before.System == nil || before.System.Inflight != 1 || before.System.ExpiredLeases != 1 {
		t.Fatalf("system diagnostic: %+v", before)
	}
	// A diagnostic read cannot expire a lease or disclose payload/identity.
	rowsBefore, err := db.SystemNotifications(ctx)
	if err != nil || rowsBefore[0].Status != "inflight" {
		t.Fatal("system diagnostic mutated lease")
	}
	if next, err := db.ClaimSystemNotification(ctx, at.Add(2*time.Minute)); err != nil || next != nil {
		t.Fatal("authorized unknown send retried")
	}
	rows, err := db.SystemNotifications(ctx)
	if err != nil || rows[0].Status != "unknown" || rows[0].ErrorClass != "send_result_unknown" {
		t.Fatalf("unknown evidence: %+v %v", rows, err)
	}
	if err = db.CompleteSystemNotification(ctx, retried.ID, retried.LeaseToken, at.Add(2*time.Minute), DeliveryOutcome{Delivered: true, ChannelRevision: retried.ChannelRevision}); !errors.Is(err, ErrDeliveryLeaseLost) {
		t.Fatalf("stale completion accepted: %v", err)
	}
}
func TestSystemNotificationRetryBudgetAndAtomicFailureEnqueue(t *testing.T) {
	db, channel, at := systemNotificationFixture(t, "webhook", 1)
	ctx := context.Background()
	for i := 1; i <= 5; i++ {
		n, err := db.ClaimSystemNotification(ctx, at)
		if err != nil || n == nil || n.AttemptCount != i {
			t.Fatalf("attempt %d: %+v %v", i, n, err)
		}
		authorizeSystemJob(t, db, n, at)
		if err = db.CompleteSystemNotification(ctx, n.ID, n.LeaseToken, at, DeliveryOutcome{ErrorClass: "http_server_error", RetryAt: at.Add(time.Second), ChannelRevision: n.ChannelRevision}); err != nil {
			t.Fatal(err)
		}
		at = at.Add(time.Second)
	}
	rows, err := db.SystemNotifications(ctx)
	if err != nil || rows[0].Status != "failed" || rows[0].AttemptCount != 5 {
		t.Fatal("retry budget not enforced")
	}
	run, err := db.BeginBackupRun(ctx, "", []string{channel}, at)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.db.ExecContext(ctx, `CREATE TRIGGER synthetic_fail_system_enqueue BEFORE INSERT ON system_notifications BEGIN SELECT RAISE(ABORT,'synthetic enqueue failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err = db.FinishBackupRun(ctx, run.ID, "failed", "backup_snapshot_failed", nil, at); err == nil {
		t.Fatal("queue insertion failure succeeded")
	}
	runs, err := db.BackupRuns(ctx)
	if err != nil || runs[0].Status != "running" {
		t.Fatal("failed completion partially committed")
	}
	if _, err = db.db.ExecContext(ctx, `DROP TRIGGER synthetic_fail_system_enqueue`); err != nil {
		t.Fatal(err)
	}
	if err = db.FinishBackupRun(ctx, run.ID, "failed", "backup_snapshot_failed", nil, at); err != nil {
		t.Fatal(err)
	}
	rows, err = db.SystemNotifications(ctx)
	if err != nil || len(rows) != 2 {
		t.Fatal("transaction retry lost or duplicated reminder")
	}
}
