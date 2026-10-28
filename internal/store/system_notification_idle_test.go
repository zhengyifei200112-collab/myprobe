package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestSystemNotificationEmptyQueueDoesNotReserveWriter(t *testing.T) {
	db, err := Open(context.Background(), filepath.Join(httpFixtureDir(t), "idle-system.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Warm separate connections before holding an unrelated WAL writer.
	var connections []*sql.Conn
	for range 4 {
		conn, err := db.db.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, conn)
	}
	for _, conn := range connections {
		conn.Close()
	}
	tx, err := db.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE nodes SET updated_at=updated_at WHERE 0"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	job, err := db.ClaimSystemNotification(ctx, time.Now().UTC())
	if err != nil || job != nil {
		t.Fatal("empty system queue waited for an unrelated writer", err)
	}
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
