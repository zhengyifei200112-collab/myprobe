package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var historyOpenEnd = time.Date(9999, 12, 31, 23, 59, 58, 0, time.UTC)

func (s *Store) NodeExists(ctx context.Context, nodeID string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM nodes WHERE id=?)`, nodeID).Scan(&exists)
	return exists, err
}

// Stored sample timestamps are UTC RFC3339Nano, whose optional fractions do not
// sort chronologically as text within one second. Reuse migration 041's exact
// indexed UTC key, keeping both bounds as direct index predicates.
const historyRawBounds = `captured_sort_ns>=:start AND captured_sort_ns<:end`

// Retention buckets start on whole seconds. Only include complete buckets: a
// partially overlapping aggregate cannot be separated into its original samples.
const historyRollupBounds = `bucket_at>=:rollup_start AND bucket_at<:scan_end
	AND unixepoch(bucket_at)+bucket_seconds<=:end_second`

func historyQueryArgs(nodeID string, start, end time.Time, bucketSeconds int, includeHidden bool) ([]any, error) {
	start, end = start.UTC(), end.UTC()
	if bucketSeconds < 1 || bucketSeconds > 86400 || !start.Before(end) || start.Year() < 1970 || end.Year() > 9999 {
		return nil, errors.New("invalid history window or bucket")
	}
	rollupStart := start.Truncate(time.Second)
	if start.Nanosecond() != 0 {
		rollupStart = rollupStart.Add(time.Second)
	}
	const fixed = "2006-01-02T15:04:05.000000000Z"
	const seconds = "2006-01-02T15:04:05"
	return []any{
		sql.Named("node", nodeID),
		sql.Named("include_hidden", includeHidden),
		sql.Named("bucket", bucketSeconds),
		sql.Named("scan_end", end.Truncate(time.Second).Add(time.Second).Format(seconds)),
		sql.Named("start", start.Format(fixed)),
		sql.Named("end", end.Format(fixed)),
		sql.Named("rollup_start", formatTime(rollupStart)),
		sql.Named("end_second", end.Unix()),
	}, nil
}
