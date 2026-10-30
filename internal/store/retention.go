package store

import (
	"context"
	"errors"
	"time"
)

type RetentionPolicy struct {
	Raw        time.Duration
	OneMinute  time.Duration
	FiveMinute time.Duration
}

func DefaultRetentionPolicy() RetentionPolicy {
	return RetentionPolicy{
		Raw:        7 * 24 * time.Hour,
		OneMinute:  30 * 24 * time.Hour,
		FiveMinute: 365 * 24 * time.Hour,
	}
}

func (policy RetentionPolicy) Validate() error {
	if policy.Raw <= 0 || policy.OneMinute < policy.Raw || policy.FiveMinute < policy.OneMinute {
		return errors.New("retention durations must be positive and ordered raw <= one-minute <= five-minute")
	}
	return nil
}

// ApplyRetention builds both rollup levels and removes expired detail in one transaction.
// One raw metric sample per node is retained as a traffic-counter anchor at the raw boundary.
func (s *Store) ApplyRetention(ctx context.Context, now time.Time, policy RetentionPolicy) error {
	return s.applyRetentionStatements(ctx, now, policy, standardRetentionStatements())
}

// Statements stay private and fixed in production. The capacity fixture can
// compare the same transaction with legacy index selection, without duplicating
// retention, traffic anchoring, pruning, or commit/error behavior.
type retentionStatements struct {
	rawMetrics, rawLatency, rawTraffic          string
	minuteMetrics, minuteLatency, minuteTraffic string
	pruneMetrics                                string
	legacyTime                                  bool
}

func standardRetentionStatements() retentionStatements {
	return retentionStatements{rawMetrics: rollupRawMetricsQuery, rawLatency: rollupRawLatencyQuery, rawTraffic: rollupRawTrafficQuery, minuteMetrics: rollupMinuteMetricsQuery, minuteLatency: rollupMinuteLatencyQuery, minuteTraffic: rollupMinuteTrafficQuery, pruneMetrics: pruneRawMetricsQuery}
}

func (s *Store) applyRetentionStatements(ctx context.Context, now time.Time, policy RetentionPolicy, statements retentionStatements) (result error) {
	complete := s.retentionJob.Begin()
	defer func() { complete(result) }()
	if err := policy.Validate(); err != nil {
		return err
	}
	now = now.UTC()
	rawCutoff := floorTime(now.Add(-policy.Raw), time.Minute)
	minuteCutoff := floorTime(now.Add(-policy.OneMinute), 5*time.Minute)
	fiveMinuteCutoff := floorTime(now.Add(-policy.FiveMinute), 5*time.Minute)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, item := range []struct {
		query  string
		cutoff time.Time
		raw    bool
	}{
		{statements.rawMetrics, rawCutoff, true}, {statements.rawLatency, rawCutoff, true}, {statements.rawTraffic, rawCutoff, true},
		{statements.minuteMetrics, minuteCutoff, false}, {statements.minuteLatency, minuteCutoff, false}, {statements.minuteTraffic, minuteCutoff, false},
	} {
		cutoff := formatTime(item.cutoff)
		if item.raw && !statements.legacyTime {
			cutoff = formatCapturedTime(item.cutoff)
		}
		if _, err := tx.ExecContext(ctx, item.query, cutoff); err != nil {
			return err
		}
	}

	rawText := formatCapturedTime(rawCutoff)
	latencyPrune := `DELETE FROM latency_samples WHERE captured_sort_ns < ?`
	if statements.legacyTime {
		rawText = formatTime(rawCutoff)
		latencyPrune = `DELETE FROM latency_samples WHERE captured_at < ?`
	}
	if _, err := tx.ExecContext(ctx, statements.pruneMetrics, rawText, rawText); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, latencyPrune, rawText); err != nil {
		return err
	}
	if err := updateHistoryAnchors(ctx, tx, rawCutoff); err != nil {
		return err
	}
	for _, table := range []string{"metric_rollups", "latency_rollups", "traffic_rollups"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE bucket_seconds = 60 AND bucket_at < ?", formatTime(minuteCutoff)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE bucket_seconds = 300 AND bucket_at < ?", formatTime(fiveMinuteCutoff)); err != nil {
			return err
		}
	}
	if _, err := pruneHTTPHistory(ctx, tx, now); err != nil {
		return err
	}
	return tx.Commit()
}

const pruneRawMetricsQuery = `DELETE FROM metric_samples
        WHERE captured_sort_ns < ? AND id NOT IN (
            SELECT id FROM (
                SELECT id, ROW_NUMBER() OVER (PARTITION BY node_id ORDER BY captured_sort_ns DESC, id DESC) AS position
                FROM metric_samples INDEXED BY idx_metric_samples_retention_time WHERE captured_sort_ns < ?
            ) WHERE position = 1
        )`

func floorTime(value time.Time, size time.Duration) time.Time {
	return value.Truncate(size).UTC()
}

const rollupRawMetricsQuery = `INSERT OR REPLACE INTO metric_rollups(
		node_id,bucket_seconds,bucket_at,sample_count,cpu_sum,memory_percent_sum,disk_percent_sum,net_rx_rate_sum,net_tx_rate_sum)
		SELECT node_id,60,strftime('%Y-%m-%dT%H:%M:%SZ',(unixepoch(substr(captured_at,1,19)||'Z')/60)*60,'unixepoch'),COUNT(*),
		SUM(cpu_usage),SUM(CASE WHEN memory_total>0 THEN 100.0*memory_used/memory_total ELSE 0 END),
		SUM(CASE WHEN disk_total>0 THEN 100.0*disk_used/disk_total ELSE 0 END),SUM(net_rx_rate),SUM(net_tx_rate)
		FROM metric_samples m INDEXED BY idx_metric_samples_retention_time WHERE captured_sort_ns < ? AND NOT EXISTS (
			SELECT 1 FROM metric_rollups r WHERE r.node_id=m.node_id AND r.bucket_seconds=60
			AND unixepoch(r.bucket_at)=(unixepoch(substr(m.captured_at,1,19)||'Z')/60)*60)
		GROUP BY node_id,(unixepoch(substr(captured_at,1,19)||'Z')/60)`

const rollupRawLatencyQuery = `INSERT OR REPLACE INTO latency_rollups(
		node_id,target_id,kind,bucket_seconds,bucket_at,sample_count,success_count,latency_count,latency_sum)
		SELECT node_id,target_id,kind,60,strftime('%Y-%m-%dT%H:%M:%SZ',(unixepoch(substr(captured_at,1,19)||'Z')/60)*60,'unixepoch'),
		COUNT(*),SUM(success),SUM(CASE WHEN success=1 AND latency_ms IS NOT NULL THEN 1 ELSE 0 END),
		COALESCE(SUM(CASE WHEN success=1 THEN latency_ms ELSE 0 END),0)
		FROM latency_samples l INDEXED BY idx_latency_samples_retention_time WHERE captured_sort_ns < ? AND NOT EXISTS (
			SELECT 1 FROM latency_rollups r WHERE r.node_id=l.node_id AND r.target_id=l.target_id AND r.kind=l.kind
			AND r.bucket_seconds=60 AND unixepoch(r.bucket_at)=(unixepoch(substr(l.captured_at,1,19)||'Z')/60)*60)
		GROUP BY node_id,target_id,kind,(unixepoch(substr(captured_at,1,19)||'Z')/60)`

const rollupRawTrafficQuery = `INSERT OR REPLACE INTO traffic_rollups(node_id,bucket_seconds,bucket_at,rx_bytes,tx_bytes)
		WITH ordered AS (
			SELECT node_id,captured_at,net_rx_total,net_tx_total,
			LAG(net_rx_total) OVER (PARTITION BY node_id ORDER BY captured_sort_ns,id) AS previous_rx,
			LAG(net_tx_total) OVER (PARTITION BY node_id ORDER BY captured_sort_ns,id) AS previous_tx
			FROM metric_samples m INDEXED BY idx_metric_samples_retention_time WHERE captured_sort_ns < ?
		), deltas AS (
			SELECT node_id,captured_at,
			CASE WHEN previous_rx IS NULL THEN 0 WHEN net_rx_total>=previous_rx THEN net_rx_total-previous_rx ELSE net_rx_total END AS rx_delta,
			CASE WHEN previous_tx IS NULL THEN 0 WHEN net_tx_total>=previous_tx THEN net_tx_total-previous_tx ELSE net_tx_total END AS tx_delta
			FROM ordered
		)
		SELECT node_id,60,strftime('%Y-%m-%dT%H:%M:%SZ',(unixepoch(substr(captured_at,1,19)||'Z')/60)*60,'unixepoch'),SUM(rx_delta),SUM(tx_delta)
		FROM deltas d WHERE NOT EXISTS (
			SELECT 1 FROM traffic_rollups r WHERE r.node_id=d.node_id AND r.bucket_seconds=60
			AND unixepoch(r.bucket_at)=(unixepoch(substr(d.captured_at,1,19)||'Z')/60)*60)
		GROUP BY node_id,(unixepoch(substr(captured_at,1,19)||'Z')/60)`

const rollupMinuteMetricsQuery = `INSERT OR REPLACE INTO metric_rollups(
		node_id,bucket_seconds,bucket_at,sample_count,cpu_sum,memory_percent_sum,disk_percent_sum,net_rx_rate_sum,net_tx_rate_sum)
		SELECT node_id,300,strftime('%Y-%m-%dT%H:%M:%SZ',(unixepoch(bucket_at)/300)*300,'unixepoch'),
		SUM(sample_count),SUM(cpu_sum),SUM(memory_percent_sum),SUM(disk_percent_sum),SUM(net_rx_rate_sum),SUM(net_tx_rate_sum)
		FROM metric_rollups m WHERE bucket_seconds=60 AND bucket_at < ? AND NOT EXISTS (
			SELECT 1 FROM metric_rollups r WHERE r.node_id=m.node_id AND r.bucket_seconds=300
			AND unixepoch(r.bucket_at)=(unixepoch(m.bucket_at)/300)*300)
		GROUP BY node_id,(unixepoch(bucket_at)/300)`

const rollupMinuteLatencyQuery = `INSERT OR REPLACE INTO latency_rollups(
		node_id,target_id,kind,bucket_seconds,bucket_at,sample_count,success_count,latency_count,latency_sum)
		SELECT node_id,target_id,kind,300,strftime('%Y-%m-%dT%H:%M:%SZ',(unixepoch(bucket_at)/300)*300,'unixepoch'),
		SUM(sample_count),SUM(success_count),SUM(latency_count),SUM(latency_sum)
		FROM latency_rollups l WHERE bucket_seconds=60 AND bucket_at < ? AND NOT EXISTS (
			SELECT 1 FROM latency_rollups r WHERE r.node_id=l.node_id AND r.target_id=l.target_id AND r.kind=l.kind
			AND r.bucket_seconds=300 AND unixepoch(r.bucket_at)=(unixepoch(l.bucket_at)/300)*300)
		GROUP BY node_id,target_id,kind,(unixepoch(bucket_at)/300)`

const rollupMinuteTrafficQuery = `INSERT OR REPLACE INTO traffic_rollups(node_id,bucket_seconds,bucket_at,rx_bytes,tx_bytes)
		SELECT node_id,300,strftime('%Y-%m-%dT%H:%M:%SZ',(unixepoch(bucket_at)/300)*300,'unixepoch'),SUM(rx_bytes),SUM(tx_bytes)
		FROM traffic_rollups t WHERE bucket_seconds=60 AND bucket_at < ? AND NOT EXISTS (
			SELECT 1 FROM traffic_rollups r WHERE r.node_id=t.node_id AND r.bucket_seconds=300
			AND unixepoch(r.bucket_at)=(unixepoch(t.bucket_at)/300)*300)
		GROUP BY node_id,(unixepoch(bucket_at)/300)`
