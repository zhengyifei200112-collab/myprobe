package store

import (
	"context"
	"database/sql"
	"time"
)

const HTTPHistoryRetention = 31 * 24 * time.Hour

func pruneHTTPHistory(ctx context.Context, tx *sql.Tx, now time.Time) error {
	cutoff := now.UTC().Add(-HTTPHistoryRetention).Truncate(time.Second)
	// Advance only: moving the clock backwards cannot restore deleted samples.
	if _, err := tx.ExecContext(ctx, `UPDATE http_history_state SET retained_from_ns=MAX(retained_from_ns,?) WHERE id=1`, cutoff.UnixNano()); err != nil {
		return err
	}
	// Fixed zero fractions sort before any instant within the cutoff second,
	// including whole-second RFC3339 strings which omit the decimal entirely.
	bound := cutoff.Format("2006-01-02T15:04:05.000000000Z")
	if _, err := tx.ExecContext(ctx, `DELETE FROM http_tasks WHERE id IN (SELECT id FROM http_tasks WHERE scheduled_at<? ORDER BY scheduled_at LIMIT 10000)`, bound); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM http_schedule_epochs WHERE rowid IN (SELECT rowid FROM http_schedule_epochs WHERE end_ns IS NOT NULL AND end_ns<=? LIMIT 10000)`, cutoff.UnixNano())
	return err
}
