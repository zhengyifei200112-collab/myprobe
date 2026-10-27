package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"

	quality "github.com/zhengyifei200112-collab/myprobe/internal/networkquality"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

var ErrNetworkQualityUnavailable = errors.New("network quality history unavailable")

type NetworkQualityQuery struct {
	NodeID, TargetID, Kind string
	Start, End, AsOf       time.Time
}

// Store results are private. Routes must separately enforce public/share/admin
// scope and use an explicit projection; never serialize this struct directly.
type NetworkQualityStats struct {
	RequestedStart, RequestedEnd, AvailableFrom, EffectiveStart, EffectiveEnd time.Time
	Availability                                                              string
	GranularitySeconds                                                        int64
	Planned, Pending, Success, Failure, Missing, NotExecuted, ObservedMissing uint64
	Coverage, FailureRate, RTTMean, RTTMin, RTTMax, P50, P95, Jitter          *float64
	P50Bounds, P95Bounds                                                      *quality.Bounds
	JitterPairs                                                               uint64
	QuantileMethod                                                            string
	Totals                                                                    quality.Totals
}

func ceilNetworkSecond(ns int64) int64 {
	value := ns / int64(time.Second)
	if ns%int64(time.Second) != 0 {
		value++
	}
	return value
}

func networkSlotCount(anchor, interval, startNS, endNS int64) uint64 {
	if endNS <= startNS {
		return 0
	}
	lower := max(anchor, ceilNetworkSecond(startNS))
	upper := ceilNetworkSecond(endNS)
	first := anchor + ((lower-anchor+interval-1)/interval)*interval
	if first >= upper {
		return 0
	}
	return uint64((upper-1-first)/interval + 1)
}

func (s *Store) PublicNetworkQuality(ctx context.Context, q NetworkQualityQuery) (NetworkQualityStats, error) {
	return s.readNetworkQuality(ctx, q, false)
}
func (s *Store) AdminNetworkQuality(ctx context.Context, q NetworkQualityQuery) (NetworkQualityStats, error) {
	return s.readNetworkQuality(ctx, q, true)
}

func validNetworkQualityWindow(kind string, start, end, asOf time.Time) bool {
	validTime := func(t time.Time) bool { return t.Unix() >= 0 && t.Unix() <= 9223372035 }
	return (kind == protocol.TaskKindPing || kind == protocol.TaskKindTCPing) && validTime(start) && validTime(end) && validTime(asOf) && end.After(start) && end.Sub(start) <= 365*24*time.Hour
}

func (s *Store) readNetworkQuality(ctx context.Context, q NetworkQualityQuery, includeHidden bool) (NetworkQualityStats, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return NetworkQualityStats{}, err
	}
	defer tx.Rollback()
	result, err := readNetworkQualityTx(ctx, tx, q, includeHidden)
	if err != nil {
		return result, err
	}
	return result, tx.Commit()
}

func readNetworkQualityTx(ctx context.Context, tx *sql.Tx, q NetworkQualityQuery, includeHidden bool) (NetworkQualityStats, error) {
	result := NetworkQualityStats{RequestedStart: q.Start.UTC(), RequestedEnd: q.End.UTC(), QuantileMethod: "unavailable", GranularitySeconds: 1}
	// UnixNano is defined only within this range. Reject wider Go time values
	// before arithmetic, including their otherwise plausible wrapped nanoseconds.
	if q.NodeID == "" || q.TargetID == "" || !validNetworkQualityWindow(q.Kind, q.Start, q.End, q.AsOf) {
		return result, quality.ErrInvalid
	}
	var err error
	var allowed int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes WHERE id=? AND (? OR hidden=0)`, q.NodeID, includeHidden).Scan(&allowed); err != nil {
		return result, err
	}
	if allowed != 1 {
		return result, ErrNotFound
	}
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM network_schedule_epochs WHERE node_id=? AND target_id=? AND kind=?)`, q.NodeID, q.TargetID, q.Kind).Scan(&allowed); err != nil {
		return result, err
	}
	if allowed == 0 {
		return result, ErrNotFound
	}
	var plansKnown, resultsKnown, rawFloor, minuteFloor, retained int64
	if err = tx.QueryRowContext(ctx, `SELECT m.known_since,m.known_results_since,h.raw_floor,h.minute_floor,h.retained_from FROM network_schedule_metadata m CROSS JOIN network_quality_history_state h WHERE m.singleton=1 AND h.singleton=1`).Scan(&plansKnown, &resultsKnown, &rawFloor, &minuteFloor, &retained); err != nil {
		return result, err
	}
	for _, boundary := range []int64{plansKnown, resultsKnown, rawFloor, minuteFloor, retained} {
		if boundary < 0 || boundary > 9223372035 {
			return result, ErrNetworkQualityUnavailable
		}
	}
	result.AvailableFrom = time.Unix(max(plansKnown, resultsKnown, retained), 0).UTC()
	result.EffectiveStart = q.Start.UTC()
	if result.EffectiveStart.Before(result.AvailableFrom) {
		result.EffectiveStart = result.AvailableFrom
	}
	result.EffectiveEnd = q.End.UTC()
	if result.EffectiveEnd.After(q.AsOf) {
		result.EffectiveEnd = q.AsOf.UTC()
	}
	result.Availability = "available"
	if result.EffectiveStart.After(q.Start) {
		result.Availability = "partial_known_history"
	}
	if result.EffectiveStart.Unix() < minuteFloor {
		result.GranularitySeconds = 300
	} else if result.EffectiveStart.Unix() < rawFloor {
		result.GranularitySeconds = 60
	}
	if result.GranularitySeconds > 1 {
		size := result.GranularitySeconds
		// Snap inward only; never include observations outside the request. Return
		// actual bounds so a consumer can explain the loss of boundary precision.
		start := (ceilNetworkSecond(result.EffectiveStart.UnixNano()) + size - 1) / size * size
		end := result.EffectiveEnd.Unix() / size * size
		result.EffectiveStart = time.Unix(start, 0).UTC()
		result.EffectiveEnd = time.Unix(end, 0).UTC()
		if result.EffectiveStart.After(q.Start) || result.EffectiveEnd.Before(q.End) {
			result.Availability = "partial_resolution"
		}
	}
	if !result.EffectiveEnd.After(result.EffectiveStart) {
		result.EffectiveStart = result.EffectiveEnd
		result.Availability = "untracked_history"
		return result, nil
	}
	type epoch struct {
		id                        string
		anchor, interval, timeout int64
		end                       sql.NullInt64
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,starts_at,interval_seconds,timeout_ms,ends_at FROM network_schedule_epochs WHERE node_id=? AND target_id=? AND kind=? AND starts_at<? AND (ends_at IS NULL OR ends_at>?) ORDER BY starts_at,id LIMIT 4097`, q.NodeID, q.TargetID, q.Kind, ceilNetworkSecond(result.EffectiveEnd.UnixNano()), result.EffectiveStart.Unix())
	if err != nil {
		return result, err
	}
	var epochs []epoch
	for rows.Next() {
		var e epoch
		if err = rows.Scan(&e.id, &e.anchor, &e.interval, &e.timeout, &e.end); err != nil {
			break
		}
		if e.anchor < 0 || e.anchor > 9223372035 || e.interval < 5 || e.interval > 86400 || e.timeout < 100 || e.timeout > 60000 || (e.end.Valid && (e.end.Int64 < e.anchor || e.end.Int64 > 9223372035)) {
			err = ErrNetworkQualityUnavailable
			break
		}
		epochs = append(epochs, e)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return result, err
	}
	if len(epochs) > 4096 {
		return result, ErrNetworkQualityUnavailable
	}
	const maxRows = 350000
	remaining := maxRows
	var samples []float64
	approximate := false
	for _, e := range epochs {
		startNS := max(result.EffectiveStart.UnixNano(), e.anchor*int64(time.Second))
		endNS := result.EffectiveEnd.UnixNano()
		if e.end.Valid {
			endNS = min(endNS, e.end.Int64*int64(time.Second))
		}
		matureEndNS := min(endNS, q.AsOf.UnixNano()-e.timeout*int64(time.Millisecond)-15*int64(time.Second))
		matured := networkSlotCount(e.anchor, e.interval, startNS, matureEndNS)
		result.Planned += matured
		result.Pending += networkSlotCount(e.anchor, e.interval, startNS, endNS) - matured
		if matureEndNS <= startNS {
			continue
		}
		segment, err := quality.NewSegment(e.id, e.anchor, e.interval)
		if err != nil {
			return result, ErrNetworkQualityUnavailable
		}
		// An atomic archive guarantees one source per bucket. UNION order restores
		// epoch adjacency across both levels and raw data without a second snapshot.
		observations, err := tx.QueryContext(ctx, `SELECT scheduled_at AS at,0 AS level,`+networkObservationColumns+`,NULL AS summary FROM network_probe_tasks WHERE epoch_id=? AND source='scheduled' AND scheduled_at>=? AND scheduled_at<?
   UNION ALL SELECT bucket_at AS at,bucket_seconds AS level,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,CASE WHEN length(summary)<=? THEN summary END FROM network_quality_rollups WHERE epoch_id=? AND bucket_at>=? AND bucket_at<?
   ORDER BY at,level LIMIT ?`, e.id, ceilNetworkSecond(startNS), ceilNetworkSecond(matureEndNS), quality.MaxSegmentBytes, e.id, startNS/int64(time.Second)/300*300, ceilNetworkSecond(matureEndNS), remaining+1)
		if err != nil {
			return result, err
		}
		for observations.Next() {
			remaining--
			if remaining < 0 {
				err = ErrNetworkQualityUnavailable
				break
			}
			var at, level int64
			var o networkObservation
			var id, state, dispatch sql.NullString
			var slot sql.NullInt64
			var payload []byte
			if err = observations.Scan(&at, &level, &id, &slot, &state, &dispatch, &o.outcome, &o.raw, &o.sha, &o.received, &payload); err != nil {
				break
			}
			if level == 0 {
				if !id.Valid || !slot.Valid || !state.Valid || !dispatch.Valid {
					err = ErrNetworkQualityUnavailable
					break
				}
				o.id, o.slot, o.state, o.dispatch = id.String, slot.Int64, state.String, dispatch.String
				var rtt float64
				var success bool
				rtt, success, err = o.add(&segment, q.TargetID)
				if err != nil {
					break
				}
				if success {
					samples = append(samples, rtt)
				}
			} else {
				// Empty clipping is allowed; splitting a retained histogram is not.
				if at+level <= ceilNetworkSecond(startNS) {
					continue
				}
				var part quality.Segment
				part, err = decodeNetworkBucket(e.id, e.anchor, e.interval, at, level, payload)
				if err != nil {
					break
				}
				if part.First.Slot < ceilNetworkSecond(startNS) || part.Last.Slot >= ceilNetworkSecond(matureEndNS) {
					err = ErrNetworkQualityUnavailable
					break
				}
				err = segment.MergeEpoch(part)
				if err != nil {
					break
				}
				approximate = true
			}
		}
		if err == nil {
			err = observations.Err()
		}
		observations.Close()
		if err != nil {
			return result, err
		}
		if err = result.Totals.Merge(segment.Totals); err != nil {
			return result, ErrNetworkQualityUnavailable
		}
	}
	result.Success, result.Failure = result.Totals.Success, result.Totals.Failure
	if result.Totals.Observed > result.Planned || result.Success+result.Failure > result.Planned {
		return result, ErrNetworkQualityUnavailable
	}
	result.Missing = result.Planned - result.Success - result.Failure
	result.NotExecuted, result.ObservedMissing = result.Totals.NotExecuted, result.Totals.Unobserved
	percent := func(n, d uint64) *float64 {
		if d == 0 {
			return nil
		}
		v := 100 * float64(n) / float64(d)
		return &v
	}
	result.Coverage = percent(result.Success+result.Failure, result.Planned)
	result.FailureRate = percent(result.Failure, result.Success+result.Failure)
	if result.Success > 0 {
		mean := result.Totals.RTTSum / float64(result.Success)
		low, high := result.Totals.RTTMin, result.Totals.RTTMax
		result.RTTMean, result.RTTMin, result.RTTMax = &mean, &low, &high
		if approximate {
			p50, err := result.Totals.Histogram.Quantile(50)
			if err != nil {
				return result, ErrNetworkQualityUnavailable
			}
			p95, err := result.Totals.Histogram.Quantile(95)
			if err != nil {
				return result, ErrNetworkQualityUnavailable
			}
			result.P50Bounds, result.P95Bounds = &p50, &p95
			middle50, middle95 := (p50.LowerMS+p50.UpperMS)/2, (p95.LowerMS+p95.UpperMS)/2
			result.P50, result.P95 = &middle50, &middle95
			result.QuantileMethod = "histogram_v1"
		} else {
			sort.Float64s(samples)
			p50, p95 := samples[(len(samples)*50+99)/100-1], samples[(len(samples)*95+99)/100-1]
			result.P50, result.P95 = &p50, &p95
			result.QuantileMethod = "exact_raw"
		}
	}
	result.JitterPairs = result.Totals.JitterPairs
	if result.JitterPairs > 0 {
		jitter := result.Totals.JitterSum / float64(result.JitterPairs)
		result.Jitter = &jitter
	}
	return result, nil
}
