package store

import (
	"context"
	"database/sql"
	"time"
)

// An old full-maintenance database can have an empty marker table. Inspect only
// the first eligible sample of each unmarked node, then check its retained
// bucket; never scan its raw backlog looking for a covered row. Initializing
// these known anchors keeps reads correct from the first committed startup pass.
func initializeHistoryAnchors(ctx context.Context, tx *sql.Tx, cutoff time.Time) error {
	_, err := tx.ExecContext(ctx, `WITH candidates AS MATERIALIZED (
		SELECT n.id AS node_id,(SELECT m.id FROM metric_samples m INDEXED BY idx_metric_samples_node_capture_ns
			WHERE m.node_id=n.id AND m.captured_sort_ns<? ORDER BY m.captured_sort_ns,m.id LIMIT 1) AS sample_id
		FROM nodes n WHERE NOT EXISTS(SELECT 1 FROM history_retention_anchors a WHERE a.node_id=n.id AND a.sample_id IS NOT NULL)
	)
	INSERT INTO history_retention_anchors(node_id,sample_id)
	SELECT c.node_id,c.sample_id FROM candidates c JOIN metric_samples m ON m.id=c.sample_id
	WHERE EXISTS(SELECT 1 FROM metric_rollups r WHERE r.node_id=c.node_id AND r.bucket_seconds=60
		AND unixepoch(r.bucket_at)=(unixepoch(substr(m.captured_at,1,19)||'Z')/60)*60)
	OR EXISTS(SELECT 1 FROM metric_rollups r WHERE r.node_id=c.node_id AND r.bucket_seconds=300
		AND unixepoch(r.bucket_at)=(unixepoch(substr(m.captured_at,1,19)||'Z')/300)*300)
	ON CONFLICT(node_id) DO UPDATE SET sample_id=excluded.sample_id`, formatCapturedTime(cutoff))
	return err
}

func updateHistoryAnchors(ctx context.Context, tx *sql.Tx, end time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO history_retention_anchors(node_id,sample_id)
		SELECT node_id,id FROM (
			SELECT node_id,id,ROW_NUMBER() OVER (PARTITION BY node_id ORDER BY captured_sort_ns DESC,id DESC) AS position
			FROM metric_samples INDEXED BY idx_metric_samples_retention_time WHERE captured_sort_ns<?
		) WHERE position=1
		ON CONFLICT(node_id) DO UPDATE SET sample_id=excluded.sample_id`, formatCapturedTime(end))
	return err
}

// Retained counter anchors are input to traffic LAG, not new aggregate output.
// The 300-second checks also cover databases first entering batched retention
// after full maintenance: their marker table can still be empty even though
// an old anchor's minute has already been promoted. Recreating those minutes
// would expose overlapping tiers while the bounded promotion backlog drains.
const historyBatchRawMetricsQuery = `INSERT OR REPLACE INTO metric_rollups(
		node_id,bucket_seconds,bucket_at,sample_count,cpu_sum,memory_percent_sum,disk_percent_sum,net_rx_rate_sum,net_tx_rate_sum)
		SELECT node_id,60,strftime('%Y-%m-%dT%H:%M:%SZ',(unixepoch(substr(captured_at,1,19)||'Z')/60)*60,'unixepoch'),COUNT(*),
		SUM(cpu_usage),SUM(CASE WHEN memory_total>0 THEN 100.0*memory_used/memory_total ELSE 0 END),
		SUM(CASE WHEN disk_total>0 THEN 100.0*disk_used/disk_total ELSE 0 END),SUM(net_rx_rate),SUM(net_tx_rate)
		FROM metric_samples m INDEXED BY idx_metric_samples_retention_time WHERE captured_sort_ns < ?
		AND NOT EXISTS (SELECT 1 FROM history_retention_anchors a WHERE a.sample_id=m.id)
		AND NOT EXISTS (SELECT 1 FROM metric_rollups r WHERE r.node_id=m.node_id AND r.bucket_seconds=60
			AND unixepoch(r.bucket_at)=(unixepoch(substr(m.captured_at,1,19)||'Z')/60)*60)
		AND NOT EXISTS (SELECT 1 FROM metric_rollups r WHERE r.node_id=m.node_id AND r.bucket_seconds=300
			AND unixepoch(r.bucket_at)=(unixepoch(substr(m.captured_at,1,19)||'Z')/300)*300)
		GROUP BY node_id,(unixepoch(substr(captured_at,1,19)||'Z')/60)`

const historyBatchRawLatencyQuery = `INSERT OR REPLACE INTO latency_rollups(
		node_id,target_id,kind,bucket_seconds,bucket_at,sample_count,success_count,latency_count,latency_sum)
		SELECT node_id,target_id,kind,60,strftime('%Y-%m-%dT%H:%M:%SZ',(unixepoch(substr(captured_at,1,19)||'Z')/60)*60,'unixepoch'),
		COUNT(*),SUM(success),SUM(CASE WHEN success=1 AND latency_ms IS NOT NULL THEN 1 ELSE 0 END),
		COALESCE(SUM(CASE WHEN success=1 THEN latency_ms ELSE 0 END),0)
		FROM latency_samples l INDEXED BY idx_latency_samples_retention_time WHERE captured_sort_ns < ?
		AND NOT EXISTS (SELECT 1 FROM latency_rollups r WHERE r.node_id=l.node_id AND r.target_id=l.target_id AND r.kind=l.kind
			AND r.bucket_seconds=60 AND unixepoch(r.bucket_at)=(unixepoch(substr(l.captured_at,1,19)||'Z')/60)*60)
		AND NOT EXISTS (SELECT 1 FROM latency_rollups r WHERE r.node_id=l.node_id AND r.target_id=l.target_id AND r.kind=l.kind
			AND r.bucket_seconds=300 AND unixepoch(r.bucket_at)=(unixepoch(substr(l.captured_at,1,19)||'Z')/300)*300)
		GROUP BY node_id,target_id,kind,(unixepoch(substr(captured_at,1,19)||'Z')/60)`

const historyBatchRawTrafficQuery = `INSERT OR REPLACE INTO traffic_rollups(node_id,bucket_seconds,bucket_at,rx_bytes,tx_bytes)
		WITH ordered AS (
			SELECT m.id,node_id,captured_at,net_rx_total,net_tx_total,
			LAG(net_rx_total) OVER (PARTITION BY node_id ORDER BY captured_sort_ns,id) AS previous_rx,
			LAG(net_tx_total) OVER (PARTITION BY node_id ORDER BY captured_sort_ns,id) AS previous_tx
			FROM metric_samples m INDEXED BY idx_metric_samples_retention_time WHERE captured_sort_ns < ?
		), deltas AS (
			SELECT id,node_id,captured_at,
			CASE WHEN previous_rx IS NULL THEN 0 WHEN net_rx_total>=previous_rx THEN net_rx_total-previous_rx ELSE net_rx_total END AS rx_delta,
			CASE WHEN previous_tx IS NULL THEN 0 WHEN net_tx_total>=previous_tx THEN net_tx_total-previous_tx ELSE net_tx_total END AS tx_delta
			FROM ordered
		)
		SELECT node_id,60,strftime('%Y-%m-%dT%H:%M:%SZ',(unixepoch(substr(captured_at,1,19)||'Z')/60)*60,'unixepoch'),SUM(rx_delta),SUM(tx_delta)
		FROM deltas d WHERE NOT EXISTS (SELECT 1 FROM history_retention_anchors a WHERE a.sample_id=d.id)
		AND NOT EXISTS (SELECT 1 FROM traffic_rollups r WHERE r.node_id=d.node_id AND r.bucket_seconds=60
			AND unixepoch(r.bucket_at)=(unixepoch(substr(d.captured_at,1,19)||'Z')/60)*60)
		AND NOT EXISTS (SELECT 1 FROM traffic_rollups r WHERE r.node_id=d.node_id AND r.bucket_seconds=300
			AND unixepoch(r.bucket_at)=(unixepoch(substr(d.captured_at,1,19)||'Z')/300)*300)
		GROUP BY node_id,(unixepoch(substr(captured_at,1,19)||'Z')/60)`
