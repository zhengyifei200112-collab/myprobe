package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestDiagnosticsReadWhileSQLiteWriterIsReserved(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(httpFixtureDir(t), "diagnostics-reader.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	writer, err := s.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := writer.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	defer writer.ExecContext(ctx, "ROLLBACK")
	readCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	queue := s.NotificationQueueDiagnostics(readCtx, time.Now())
	if queue.Status != "available" || queue.Deliveries == nil || queue.Deliveries.Pending != 0 || queue.Aggregates == nil || queue.Aggregates.Pending != 0 {
		t.Fatalf("health polling waited for the SQLite writer: %+v", queue)
	}
	if got := s.DatabaseDiagnostics(readCtx); got.Status != "ok" {
		t.Fatalf("metadata read unavailable while writer reserved: %+v", got)
	}
}
