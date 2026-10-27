package store

import (
	"context"
	"database/sql"
	"time"
)

const HTTPHistoryRetention = 31 * 24 * time.Hour

// Bound writer occupancy by draining more often, rather than raising the
// per-transaction row budget when a backlog develops.
const httpRetentionBatchSize = 1000

// HTTPRetentionBatch describes committed work, not total expired backlog.
// More is conservative: a full batch requires another bounded pass.
type HTTPRetentionBatch struct {
	TasksDeleted  int64
	EpochsDeleted int64
	RetainedFrom  time.Time
	More          bool
}

// PruneHTTPHistory commits one bounded batch independently of host rollups.
// Callers must yield between passes so reports and management writes can proceed.
func (s *Store) PruneHTTPHistory(ctx context.Context, now time.Time) (result HTTPRetentionBatch, resultErr error) {
	complete := s.httpRetentionJob.Begin()
	defer func() { complete(resultErr) }()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return HTTPRetentionBatch{}, err
	}
	defer tx.Rollback()
	batch, err := pruneHTTPHistory(ctx, tx, now)
	if err != nil {
		return HTTPRetentionBatch{}, err
	}
	if err := tx.Commit(); err != nil {
		return HTTPRetentionBatch{}, err
	}
	return batch, nil
}

func pruneHTTPHistory(ctx context.Context, tx *sql.Tx, now time.Time) (HTTPRetentionBatch, error) {
	batch := HTTPRetentionBatch{}
	cutoff := now.UTC().Add(-HTTPHistoryRetention).Truncate(time.Second)
	// Advance only: moving the clock backwards cannot restore deleted samples.
	if _, err := tx.ExecContext(ctx, `UPDATE http_history_state SET retained_from_ns=MAX(retained_from_ns,?) WHERE id=1`, cutoff.UnixNano()); err != nil {
		return batch, err
	}
	var floor int64
	if err := tx.QueryRowContext(ctx, `SELECT retained_from_ns FROM http_history_state WHERE id=1`).Scan(&floor); err != nil {
		return batch, err
	}
	// Continue draining the already-published floor even after clock rollback.
	cutoff = time.Unix(0, floor).UTC()
	batch.RetainedFrom = cutoff
	// Fixed zero fractions sort before any instant within the cutoff second,
	// including whole-second RFC3339 strings which omit the decimal entirely.
	bound := cutoff.Format("2006-01-02T15:04:05.000000000Z")
	deleted, err := tx.ExecContext(ctx, `DELETE FROM http_tasks WHERE id IN (SELECT id FROM http_tasks WHERE scheduled_at<? ORDER BY scheduled_at LIMIT ?)`, bound, httpRetentionBatchSize)
	if err != nil {
		return batch, err
	}
	if batch.TasksDeleted, err = deleted.RowsAffected(); err != nil {
		return batch, err
	}
	deleted, err = tx.ExecContext(ctx, `DELETE FROM http_schedule_epochs WHERE rowid IN (SELECT rowid FROM http_schedule_epochs WHERE end_ns IS NOT NULL AND end_ns<=? ORDER BY end_ns LIMIT ?)`, floor, httpRetentionBatchSize)
	if err != nil {
		return batch, err
	}
	if batch.EpochsDeleted, err = deleted.RowsAffected(); err != nil {
		return batch, err
	}
	batch.More = batch.TasksDeleted == httpRetentionBatchSize || batch.EpochsDeleted == httpRetentionBatchSize
	return batch, nil
}
