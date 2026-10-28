package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"time"

	quality "github.com/zhengyifei200112-collab/myprobe/internal/networkquality"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

// Fixed private SQL shared by queries and archival; overlarge values are not
// materialized in Go. NULL here is corruption, never an absent observation.
const networkObservationColumns = `id,scheduled_at,state,dispatch_reason,outcome,
 CASE WHEN length(result_json)<=4096 THEN result_json END,result_sha,received_at_ns`

type networkObservation struct {
	id, state, dispatch string
	slot                int64
	outcome, raw, sha   sql.NullString
	received            sql.NullInt64
}

type networkQualityValue struct {
	outcome quality.Outcome
	rtt     float64
	reason  string
}

func (o networkObservation) add(segment *quality.Segment, target string) (networkQualityValue, error) {
	kind, reason, rtt := quality.Unobserved, o.dispatch, 0.0
	if o.state == "completed" {
		if !o.raw.Valid || !o.received.Valid || !o.outcome.Valid || !o.sha.Valid {
			return networkQualityValue{}, ErrNetworkQualityUnavailable
		}
		var r protocol.LatencyResult
		if json.Unmarshal([]byte(o.raw.String), &r) != nil || r.Validate(time.Unix(0, o.received.Int64)) != nil || r.TaskID != o.id || r.TargetID != target {
			return networkQualityValue{}, ErrNetworkQualityUnavailable
		}
		sum := sha256.Sum256([]byte(o.raw.String))
		if hex.EncodeToString(sum[:]) != o.sha.String {
			return networkQualityValue{}, ErrNetworkQualityUnavailable
		}
		kind, reason = quality.Outcome(o.outcome.String), r.ErrorClass
		if r.Success {
			if kind != quality.Success {
				return networkQualityValue{}, ErrNetworkQualityUnavailable
			}
			rtt = r.LatencyMS
		} else if kind == quality.Success {
			return networkQualityValue{}, ErrNetworkQualityUnavailable
		}
	} else if reason == "" {
		reason = "missing"
		if o.state == "sending" {
			reason = "dispatch_unknown"
		}
	}
	if err := segment.Add(o.slot, kind, rtt, reason); err != nil {
		return networkQualityValue{}, ErrNetworkQualityUnavailable
	}
	return networkQualityValue{outcome: kind, rtt: rtt, reason: reason}, nil
}

func readNetworkRawSegment(ctx context.Context, tx *sql.Tx, epoch, target string, anchor, interval, start, end int64) (quality.Segment, error) {
	segment, err := quality.NewSegment(epoch, anchor, interval)
	if err != nil {
		return segment, ErrNetworkQualityUnavailable
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+networkObservationColumns+` FROM network_probe_tasks WHERE epoch_id=? AND source='scheduled' AND scheduled_at>=? AND scheduled_at<? ORDER BY scheduled_at LIMIT 13`, epoch, start, end)
	if err != nil {
		return segment, err
	}
	defer rows.Close()
	for rows.Next() {
		var o networkObservation
		if err := rows.Scan(&o.id, &o.slot, &o.state, &o.dispatch, &o.outcome, &o.raw, &o.sha, &o.received); err != nil {
			return segment, err
		}
		if _, err := o.add(&segment, target); err != nil {
			return segment, err
		}
	}
	if err := rows.Err(); err != nil {
		return segment, err
	}
	// There can be at most 12 fixed-phase slots in a minute at a 5s interval.
	if segment.Totals.Observed > 12 {
		return segment, ErrNetworkQualityUnavailable
	}
	return segment, nil
}
