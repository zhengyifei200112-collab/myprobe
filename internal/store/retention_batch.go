package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"time"
)

const historyPruneRows = 100
const historyMinuteGroups = 8

// HistoryRetentionBatch describes committed work, not completion of all history.
// Each pass processes at most one raw minute across nodes and eight complete
// node/five-minute groups, and prunes at most 100 expired rows per rollup table.
type HistoryRetentionBatch struct {
	RawBuckets, MinuteBuckets, RollupsDeleted int64
	More                                      bool
}

// RetainHistory releases the writer between closed time windows. Rollup, prune,
// and counter-anchor publication remain in the same transaction, so readers
// never observe a window with its raw rows removed before its rollup exists.
// A time window bounds normal probe workloads; it is not a wall-clock deadline
// or an arbitrary-input row cap. Cancellation rolls the entire pass back.
func (s *Store) RetainHistory(ctx context.Context, now time.Time, policy RetentionPolicy) (batch HistoryRetentionBatch, resultErr error) {
	complete := s.retentionJob.Begin()
	defer func() {
		if resultErr != nil {
			batch = HistoryRetentionBatch{}
		}
		complete(resultErr)
	}()
	if err := policy.Validate(); err != nil {
		return batch, err
	}
	rawCutoff := floorTime(now.UTC().Add(-policy.Raw), time.Minute)
	minuteCutoff := floorTime(now.UTC().Add(-policy.OneMinute), 5*time.Minute)
	fiveCutoff := floorTime(now.UTC().Add(-policy.FiveMinute), 5*time.Minute)
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return batch, err
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return batch, err
	}
	defer tx.Rollback()
	// Reserve the writer before selecting a window. A concurrent report cannot
	// arrive between the window selection, aggregation and pruning snapshots.
	if _, err = tx.ExecContext(ctx, `UPDATE history_retention_anchors SET sample_id=sample_id WHERE 0`); err != nil {
		return batch, err
	}
	next, err := nextRawRetention(ctx, tx, rawCutoff)
	if err != nil {
		return batch, err
	}
	if !next.IsZero() {
		end := minTime(floorTime(next, time.Minute).Add(time.Minute), rawCutoff)
		for _, query := range []string{rollupRawMetricsQuery, rollupRawLatencyQuery, rollupRawTrafficQuery} {
			if _, err = tx.ExecContext(ctx, query, formatCapturedTime(end)); err != nil {
				return batch, err
			}
		}
		// All earlier rows are previous anchors plus this one closed minute.
		// Publish precisely the same last-sample-per-node rule as full retention.
		if _, err = tx.ExecContext(ctx, `INSERT INTO history_retention_anchors(node_id,sample_id)
			SELECT node_id,id FROM (
				SELECT node_id,id,ROW_NUMBER() OVER (PARTITION BY node_id ORDER BY captured_sort_ns DESC,id DESC) AS position
				FROM metric_samples INDEXED BY idx_metric_samples_retention_time WHERE captured_sort_ns<?
			) WHERE position=1
			ON CONFLICT(node_id) DO UPDATE SET sample_id=excluded.sample_id`, formatCapturedTime(end)); err != nil {
			return batch, err
		}
		if _, err = tx.ExecContext(ctx, pruneRawMetricsQuery, formatCapturedTime(end), formatCapturedTime(end)); err != nil {
			return batch, err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM latency_samples WHERE captured_sort_ns<?`, formatCapturedTime(end)); err != nil {
			return batch, err
		}
		batch.RawBuckets = 1
		next, err = nextRawRetention(ctx, tx, rawCutoff)
		if err != nil {
			return batch, err
		}
		batch.More = !next.IsZero()
	}
	// Never promote a partially materialized five-minute group. Even a short
	// custom policy or a years-old raw backlog must finish that group's raw
	// minutes first. Anchors are excluded from pending work, not from LAG input.
	minuteCutoff = minTime(minuteCutoff, floorTime(rawCutoff, 5*time.Minute))
	if !next.IsZero() {
		minuteCutoff = minTime(minuteCutoff, floorTime(next, 5*time.Minute))
	}
	for range historyMinuteGroups {
		node, minute, selectErr := nextMinuteRetention(ctx, tx, minuteCutoff)
		if selectErr != nil {
			return batch, selectErr
		}
		if minute.IsZero() {
			break
		}
		end := minTime(floorTime(minute, 5*time.Minute).Add(5*time.Minute), minuteCutoff)
		for _, item := range nodeMinuteRetentionQueries {
			if _, err = tx.ExecContext(ctx, item.query, node, formatTime(end)); err != nil {
				return batch, err
			}
			if _, err = tx.ExecContext(ctx, `DELETE FROM `+item.table+` WHERE node_id=? AND bucket_seconds=60 AND bucket_at<?`, node, formatTime(end)); err != nil {
				return batch, err
			}
		}
		batch.MinuteBuckets++
	}
	_, pendingMinute, err := nextMinuteRetention(ctx, tx, minuteCutoff)
	if err != nil {
		return batch, err
	}
	batch.More = batch.More || !pendingMinute.IsZero()
	for _, table := range historyRollupTables {
		r, deleteErr := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE rowid IN (
			SELECT rowid FROM `+table+` WHERE bucket_seconds=300 AND bucket_at<? ORDER BY bucket_at LIMIT ?)`, formatTime(fiveCutoff), historyPruneRows)
		if deleteErr != nil {
			return batch, deleteErr
		}
		n, countErr := r.RowsAffected()
		if countErr != nil {
			return batch, countErr
		}
		batch.RollupsDeleted += n
		batch.More = batch.More || n == historyPruneRows
	}
	if err = tx.Commit(); err != nil {
		// database/sql marks Tx done even when SQLite rejects COMMIT (for
		// example, a deferred constraint). Tx.Rollback then cannot clean up
		// the still-open native transaction. Keep its connection reserved and
		// explicitly roll it back before allowing the pool to reuse it.
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, rollbackErr := conn.ExecContext(cleanup, `ROLLBACK`); rollbackErr != nil {
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
		return batch, err
	}
	return batch, nil
}

var historyRollupTables = []string{"metric_rollups", "latency_rollups", "traffic_rollups"}

// Preserve the full transaction's columns, epoch identity and aggregate rules;
// add only a bound node parameter to its source. Node/time indexes can seek the
// selected group without scanning other nodes or the unexpired retained range.
var nodeMinuteRetentionQueries = []struct{ table, query string }{
	{"metric_rollups", strings.Replace(rollupMinuteMetricsQuery, "FROM metric_rollups m WHERE", "FROM metric_rollups m INDEXED BY idx_metric_rollups_retention_time WHERE m.node_id=? AND", 1)},
	{"latency_rollups", strings.Replace(rollupMinuteLatencyQuery, "FROM latency_rollups l WHERE", "FROM latency_rollups l INDEXED BY idx_latency_rollups_retention_time WHERE l.node_id=? AND", 1)},
	{"traffic_rollups", strings.Replace(rollupMinuteTrafficQuery, "FROM traffic_rollups t WHERE", "FROM traffic_rollups t INDEXED BY idx_traffic_rollups_retention_time WHERE t.node_id=? AND", 1)},
}

func nextRawRetention(ctx context.Context, tx *sql.Tx, cutoff time.Time) (time.Time, error) {
	var first time.Time
	for _, query := range []string{
		`SELECT captured_at FROM metric_samples m INDEXED BY idx_metric_samples_retention_time
		 WHERE captured_sort_ns<? AND NOT EXISTS(SELECT 1 FROM history_retention_anchors a WHERE a.sample_id=m.id)
		 ORDER BY captured_sort_ns LIMIT 1`,
		`SELECT captured_at FROM latency_samples INDEXED BY idx_latency_samples_retention_time
		 WHERE captured_sort_ns<? ORDER BY captured_sort_ns LIMIT 1`,
	} {
		var text string
		err := tx.QueryRowContext(ctx, query, formatCapturedTime(cutoff)).Scan(&text)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return time.Time{}, err
		}
		at, err := parseTime(text)
		if err != nil {
			return time.Time{}, err
		}
		if first.IsZero() || at.Before(first) {
			first = at
		}
	}
	return first, nil
}

func nextMinuteRetention(ctx context.Context, tx *sql.Tx, cutoff time.Time) (string, time.Time, error) {
	var first time.Time
	var node string
	for _, table := range historyRollupTables {
		var text, id string
		err := tx.QueryRowContext(ctx, `SELECT node_id,bucket_at FROM `+table+` WHERE bucket_seconds=60 AND bucket_at<? ORDER BY bucket_at LIMIT 1`, formatTime(cutoff)).Scan(&id, &text)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return "", time.Time{}, err
		}
		at, err := parseTime(text)
		if err != nil {
			return "", time.Time{}, err
		}
		if first.IsZero() || at.Before(first) {
			first = at
			node = id
		}
	}
	return node, first, nil
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
