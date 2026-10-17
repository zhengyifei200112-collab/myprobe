package store

import (
	"context"
	"database/sql/driver"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"modernc.org/sqlite"
)

type sqliteCancelPlan struct {
	cancel context.CancelFunc
	done   chan struct{}
	delay  time.Duration
	called atomic.Bool
}

var sqliteCancelRegistration sync.Once
var sqliteCancellation atomic.Pointer[sqliteCancelPlan]

// Cancellation can arrive after SQLite produced a row but before QueryContext
// returns it. The driver must close that unreturned Rows/statement, otherwise an
// idle pooled connection retains a read transaction and starves WAL checkpoints.
func TestCanceledQueryReleasesWALReader(t *testing.T) {
	sqliteCancelRegistration.Do(func() {
		sqlite.MustRegisterScalarFunction("myprobe_test_cancel_row", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			p := sqliteCancellation.Load()
			if p != nil && p.called.CompareAndSwap(false, true) {
				// Include cancellation before step returns so fast machines
				// cannot miss the required pre-return cancellation entirely.
				if p.delay == 0 {
					p.cancel()
					close(p.done)
					return args[0], nil
				}
				go func() {
					started := time.Now()
					for time.Since(started) < p.delay {
						runtime.Gosched()
					}
					p.cancel()
					close(p.done)
				}()
			}
			return args[0], nil
		})
	})
	defer sqliteCancellation.Store(nil)
	previous := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(previous)
	ctx, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()

	// Both handles use the production Open/DSN against a new test-owned database.
	// Keep reader and writer connections distinct and never repair a pinned reader
	// by closing/reopening it or issuing a blocking/restarting checkpoint.
	path := filepath.Join(t.TempDir(), "cancel.db")
	writer, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	writer.db.SetMaxOpenConns(1)
	if _, err := writer.db.ExecContext(ctx, "CREATE TABLE cancellation_fixture(v INTEGER); INSERT INTO cancellation_fixture VALUES(1)"); err != nil {
		t.Fatal(err)
	}
	reader, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	reader.db.SetMaxOpenConns(1)

	// Many result columns widen first-row metadata construction without long SQL,
	// large fixtures or arbitrary sleeps inside the database/driver.
	query := "SELECT myprobe_test_cancel_row(v)" + strings.Repeat(",v", 1499) + " FROM cancellation_fixture LIMIT 1"
	delays := []time.Duration{0, 10 * time.Microsecond, 30 * time.Microsecond, 100 * time.Microsecond}
	canceledErrors, returnedRows := 0, 0
	started := time.Now()
	for i := 0; i < 400; i++ {
		if _, err := writer.db.ExecContext(ctx, "UPDATE cancellation_fixture SET v=v+1"); err != nil {
			t.Fatal(err)
		}
		queryCtx, cancel := context.WithCancel(ctx)
		p := &sqliteCancelPlan{cancel: cancel, done: make(chan struct{}), delay: delays[i%len(delays)]}
		sqliteCancellation.Store(p)
		rows, queryErr := reader.db.QueryContext(queryCtx, query)
		if rows != nil {
			returnedRows++
			if err := rows.Close(); err != nil && !errors.Is(err, context.Canceled) {
				cancel()
				t.Fatalf("iteration %d: close returned rows: %v", i, err)
			}
		}
		// A prepare/step failure need not invoke the scalar function. Diagnose
		// it immediately instead of waiting for a callback that cannot run.
		if queryErr != nil && !errors.Is(queryErr, context.Canceled) {
			cancel()
			t.Fatalf("iteration %d after %s: query error=%v callback=%v parent=%v", i, time.Since(started), queryErr, p.called.Load(), ctx.Err())
		}
		if !p.called.Load() {
			cancel()
			t.Fatalf("iteration %d after %s: query never produced fixture row: error=%v parent=%v", i, time.Since(started), queryErr, ctx.Err())
		}
		select {
		case <-p.done:
		case <-ctx.Done():
			cancel()
			t.Fatalf("iteration %d: first-row cancellation did not finish: %v", i, ctx.Err())
		}
		cancel()
		if queryErr != nil {
			canceledErrors++
		}
		if inUse := reader.db.Stats().InUse; inUse != 0 {
			t.Fatalf("iteration %d: connection still in use: %d", i, inUse)
		}
		// Reuse immediately: a late interrupt must not reach the next user of
		// this pooled connection. Do not hide it behind a sleep or retry.
		var value int
		if err := reader.db.QueryRowContext(ctx, "SELECT v FROM cancellation_fixture").Scan(&value); err != nil || value != 2*i+2 {
			t.Fatalf("iteration %d: next query value=%d error=%v", i, value, err)
		}
		if _, err := writer.db.ExecContext(ctx, "UPDATE cancellation_fixture SET v=v+1"); err != nil {
			t.Fatal(err)
		}
		var busy, frames, copied int
		if err := writer.db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &frames, &copied); err != nil {
			t.Fatal(err)
		}
		if busy != 0 || frames < 0 || frames != copied {
			t.Fatalf("iteration %d: canceled query pinned WAL after caller cleanup: busy=%d frames=%d backfilled=%d canceled_errors=%d returned_rows=%d", i, busy, frames, copied, canceledErrors, returnedRows)
		}
		if (i+1)%100 == 0 {
			t.Logf("completed %d cancellation/checkpoint/reuse cycles in %s", i+1, time.Since(started))
		}
	}
	if canceledErrors == 0 {
		t.Fatal("fixture never exercised cancellation before QueryContext returned")
	}
	t.Logf("400 first-row cancellations, %d canceled query errors, %d explicitly closed returned Rows; every passive checkpoint complete and next query usable", canceledErrors, returnedRows)
}
