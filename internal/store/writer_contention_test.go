package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

type reservationCode int

func (e reservationCode) Error() string { return "synthetic sqlite code" }
func (e reservationCode) Code() int     { return int(e) }

func TestWriterReservationBounds(t *testing.T) {
	for _, code := range []int{5, 517, 6, 19} {
		calls := 0
		err := reserveWriter(context.Background(), func() error { calls++; return reservationCode(code) })
		want := 1
		if code == 5 {
			want = 3
		}
		if err != reservationCode(code) || calls != want {
			t.Fatalf("code=%d calls=%d err=%v", code, calls, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := reserveWriter(ctx, func() error { calls++; cancel(); return reservationCode(5) })
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("cancel: calls=%d err=%v", calls, err)
	}
	calls = 0
	if err := reserveWriter(context.Background(), func() error {
		calls++
		if calls == 1 {
			return reservationCode(5)
		}
		return nil
	}); err != nil || calls != 2 {
		t.Fatalf("recovery: calls=%d err=%v", calls, err)
	}
}

func TestRetentionSurvivesWriterBusyTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "retention.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	lock, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback()
	if _, err = lock.ExecContext(ctx, "UPDATE history_retention_anchors SET sample_id=sample_id WHERE 0"); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { _, err := db.RetainHistory(ctx, time.Now().UTC(), DefaultRetentionPolicy()); finished <- err }()
	timer := time.NewTimer(6 * time.Second)
	defer timer.Stop()
	select {
	case err := <-finished:
		t.Fatalf("returned before writer released: %v", err)
	case <-timer.C:
	}
	if err = lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err = <-finished; err != nil {
		t.Fatal(err)
	}
}

func TestReportSurvivesWriterBusyTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "writer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	node, _, err := db.CreateNode(ctx, CreateNodeParams{Name: "writer"})
	if err != nil {
		t.Fatal(err)
	}
	lock, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback()
	if _, err = lock.ExecContext(ctx, "UPDATE nodes SET name=name WHERE id=?", node.ID); err != nil {
		t.Fatal(err)
	}
	report := protocol.Report{CapturedAt: time.Now().UTC()}
	finished := make(chan error, 1)
	go func() { finished <- db.SaveReportWithIngress(ctx, node.ID, report, "websocket", 2) }()
	timer := time.NewTimer(6 * time.Second)
	defer timer.Stop()
	select {
	case err := <-finished:
		t.Fatalf("returned before writer released: %v", err)
	case <-timer.C:
	}
	if err = lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err = <-finished; err != nil {
		t.Fatal(err)
	}
	if err = db.SaveReportWithIngress(ctx, node.ID, report, "websocket", 2); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM metric_samples WHERE node_id=?", node.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	cancelled, stop := context.WithCancel(ctx)
	stop()
	if err = db.SaveReport(cancelled, node.ID, protocol.Report{CapturedAt: report.CapturedAt.Add(time.Second)}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}
