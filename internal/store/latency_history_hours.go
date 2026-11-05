package store

import (
	"context"
	"database/sql"
	"time"
)

const latencyHourBatch = 128

const alignedLatencyRollup = `(strftime('%Y-%m-%dT%H:%M:%SZ',bucket_at)=bucket_at
	AND unixepoch(bucket_at)>=0 AND unixepoch(bucket_at)%bucket_seconds=0)`

// Refresh only the selected pending hours while this transaction owns the
// writer. Readers continue using each pending hour's exact source buckets.
// A negative limit is the existing all-at-once retention operation.
func refreshLatencyHistoryHours(ctx context.Context, tx *sql.Tx, limit int) (bool, error) {
	const todo = `SELECT node_id,target_id,kind,hour_unix FROM latency_history_hours_dirty
		ORDER BY node_id,target_id,kind,hour_unix LIMIT ?`
	const rebuild = `WITH todo AS MATERIALIZED (` + todo + `)
		INSERT OR REPLACE INTO latency_history_hours
		SELECT d.node_id,d.target_id,d.kind,d.hour_unix,
		SUM(sample_count),SUM(success_count),SUM(latency_count),SUM(latency_sum)
		FROM todo d CROSS JOIN latency_rollups l INDEXED BY idx_latency_rollups_history_cover
		WHERE l.node_id=d.node_id AND l.target_id=d.target_id AND l.kind=d.kind
		AND bucket_seconds IN (60,300)
		AND bucket_at>=strftime('%Y-%m-%dT%H:%M:%SZ',d.hour_unix,'unixepoch')
		AND bucket_at<strftime('%Y-%m-%dT%H:%M:%SZ',d.hour_unix+3600,'unixepoch')
		AND EXISTS(SELECT 1 FROM nodes WHERE id=d.node_id)
		AND EXISTS(SELECT 1 FROM targets WHERE id=d.target_id)
		AND ` + alignedLatencyRollup + `
		GROUP BY d.node_id,d.target_id,d.kind,d.hour_unix`
	if _, err := tx.ExecContext(ctx, rebuild, limit); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM latency_history_hours_dirty
		WHERE (node_id,target_id,kind,hour_unix) IN (`+todo+`)`, limit); err != nil {
		return false, err
	}
	var pending bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM latency_history_hours_dirty)`).Scan(&pending)
	return pending, err
}

// The summary covers only complete hours. Exact edge buckets, pending hours and
// legacy unaligned rows retain the original time predicates and weighted values.
const latencyHourHistoryQuery = `WITH target_series AS MATERIALIZED (
	SELECT rowid AS series_index,id AS series_target FROM targets
), retained_kinds AS (
	SELECT 'ping' AS series_kind UNION ALL SELECT 'tcping'
), history AS (
	SELECT (unixepoch(substr(captured_at,1,19)||'Z')/:bucket)*:bucket AS bucket,series_index,kind,
	COUNT(*) AS sample_count,SUM(success) AS success_count,
	SUM(CASE WHEN success=1 AND latency_ms IS NOT NULL THEN 1 ELSE 0 END) AS latency_count,
	SUM(CASE WHEN success=1 THEN COALESCE(latency_ms,0) ELSE 0 END) AS latency_sum
	FROM target_series CROSS JOIN latency_samples INDEXED BY idx_latency_samples_history_cover
	WHERE target_id=series_target AND node_id=:node AND ` + historyNodeScope + ` AND ` + historyRawBounds + `
	GROUP BY bucket,series_index,kind
	UNION ALL
	SELECT (h.hour_unix/:bucket)*:bucket,series_index,h.kind,
	h.sample_count,h.success_count,h.latency_count,h.latency_sum
	FROM target_series CROSS JOIN retained_kinds CROSS JOIN latency_history_hours h
	WHERE h.node_id=:node AND h.target_id=series_target AND h.kind=series_kind
	AND h.hour_unix>=:hours_start AND h.hour_unix<:hours_end AND ` + historyNodeScope + `
	UNION ALL
	SELECT (unixepoch(substr(bucket_at,1,19)||'Z')/:bucket)*:bucket AS bucket,series_index,kind,
	SUM(sample_count),SUM(success_count),SUM(latency_count),SUM(latency_sum)
	FROM target_series CROSS JOIN retained_kinds CROSS JOIN latency_rollups INDEXED BY idx_latency_rollups_history_cover
	WHERE node_id=:node AND target_id=series_target AND kind=series_kind AND ` + historyNodeScope + `
	AND ` + historyRollupBounds + ` AND bucket_at<:hours_start_text AND ` + alignedLatencyRollup + `
	GROUP BY bucket,series_index,kind
	UNION ALL
	SELECT (unixepoch(substr(bucket_at,1,19)||'Z')/:bucket)*:bucket AS bucket,series_index,kind,
	SUM(sample_count),SUM(success_count),SUM(latency_count),SUM(latency_sum)
	FROM target_series CROSS JOIN retained_kinds CROSS JOIN latency_rollups INDEXED BY idx_latency_rollups_history_cover
	WHERE node_id=:node AND target_id=series_target AND kind=series_kind AND ` + historyNodeScope + `
	AND ` + historyRollupBounds + ` AND bucket_at>=:hours_end_text AND ` + alignedLatencyRollup + `
	GROUP BY bucket,series_index,kind
	UNION ALL
	SELECT (unixepoch(substr(bucket_at,1,19)||'Z')/:bucket)*:bucket AS bucket,series_index,latency_rollups.kind,
	SUM(sample_count),SUM(success_count),SUM(latency_count),SUM(latency_sum)
	FROM target_series CROSS JOIN retained_kinds CROSS JOIN latency_history_hours_dirty d
	CROSS JOIN latency_rollups INDEXED BY idx_latency_rollups_history_cover
	WHERE d.node_id=:node AND d.target_id=series_target AND d.kind=series_kind
	AND d.hour_unix>=:hours_start AND d.hour_unix<:hours_end
	AND latency_rollups.node_id=d.node_id AND latency_rollups.target_id=d.target_id AND latency_rollups.kind=d.kind
	AND ` + historyNodeScope + ` AND ` + historyRollupBounds + `
	AND bucket_at>=strftime('%Y-%m-%dT%H:%M:%SZ',d.hour_unix,'unixepoch')
	AND bucket_at<strftime('%Y-%m-%dT%H:%M:%SZ',d.hour_unix+3600,'unixepoch')
	AND ` + alignedLatencyRollup + `
	GROUP BY bucket,series_index,latency_rollups.kind
	UNION ALL
	SELECT (unixepoch(substr(bucket_at,1,19)||'Z')/:bucket)*:bucket AS bucket,series_index,kind,
	SUM(sample_count),SUM(success_count),SUM(latency_count),SUM(latency_sum)
	FROM target_series CROSS JOIN retained_kinds CROSS JOIN latency_rollups INDEXED BY idx_latency_rollups_unaligned_history
	WHERE node_id=:node AND target_id=series_target AND kind=series_kind AND ` + historyNodeScope + `
	AND ` + historyRollupBounds + ` AND ` + alignedLatencyRollup + ` IS NOT TRUE
	GROUP BY bucket,series_index,kind
), grouped AS (
	SELECT bucket,series_index,kind,SUM(sample_count) AS sample_count,SUM(success_count) AS success_count,
	SUM(latency_count) AS latency_count,SUM(latency_sum) AS latency_sum
	FROM history GROUP BY bucket,series_index,kind
)
SELECT g.bucket,t.id,t.name,g.kind,
	CASE WHEN g.latency_count>0 THEN g.latency_sum/g.latency_count END,
	100.0*g.success_count/g.sample_count
FROM grouped g JOIN targets t ON t.rowid=g.series_index
ORDER BY g.bucket,t.sort_order,t.name,t.id,g.kind`

func latencyHistoryReadQuery(start, end time.Time, bucketSeconds int, args []any) (string, []any) {
	first := start.Unix() / 3600 * 3600
	if first != start.Unix() || start.Nanosecond() != 0 {
		first += 3600
	}
	last := end.Unix() / 3600 * 3600
	if bucketSeconds%3600 != 0 || first >= last {
		return latencyHistoryQuery, args
	}
	return latencyHourHistoryQuery, append(args,
		sql.Named("hours_start", first), sql.Named("hours_end", last),
		sql.Named("hours_start_text", formatTime(time.Unix(first, 0))),
		sql.Named("hours_end_text", formatTime(time.Unix(last, 0))))
}
