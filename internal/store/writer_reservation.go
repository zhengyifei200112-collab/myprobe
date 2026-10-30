package store

import (
	"context"
	"errors"
	"time"
)

// reserveWriter retries only the first write of a fresh deferred transaction,
// before reads or business mutations. SQLite leaves that transaction unchanged
// when acquiring the writer returns SQLITE_BUSY. Never use this for COMMIT or
// an arbitrary statement: those require their own rollback/replay semantics.
// Three attempts retain the connection's 5s busy timeout, with a finite wait
// and the caller's cancellation/deadline taking precedence.
func reserveWriter(ctx context.Context, acquire func() error) error {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := acquire()
		if err == nil {
			return nil
		}
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		var coded interface{ Code() int }
		// Exact primary BUSY only. BUSY_SNAPSHOT requires a new transaction and
		// LOCKED may indicate misuse, so neither is safe to retry here.
		if attempt == 2 || !errors.As(err, &coded) || coded.Code() != 5 {
			return err
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
