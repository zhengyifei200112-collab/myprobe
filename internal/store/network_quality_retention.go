package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	quality "github.com/zhengyifei200112-collab/myprobe/internal/networkquality"
)

const networkArchiveBuckets = 32
const networkPruneRows = 1000

type NetworkQualityRetentionBatch struct {
	RawBuckets, MinuteBuckets, TasksDeleted, RollupsDeleted, EpochsDeleted int64
	RetainedFrom                                                           time.Time
	More                                                                   bool
}

// RetainNetworkQuality commits bounded work. Publishing monotonic floors makes
// availability and resolution truthful even while a long backlog drains. A
// reader sees either a raw bucket or its rollup, never both or neither.
func (s *Store) RetainNetworkQuality(ctx context.Context, now time.Time, policy RetentionPolicy) (batch NetworkQualityRetentionBatch, resultErr error) {
	complete := s.networkRetentionJob.Begin()
	defer func() {
		if resultErr != nil {
			batch = NetworkQualityRetentionBatch{}
		}
		complete(resultErr)
	}()
	if err := policy.Validate(); err != nil {
		return batch, err
	}
	if now.UnixNano() <= 0 {
		return batch, quality.ErrInvalid
	}
	tx, err := s.networkWrite(ctx)
	if err != nil {
		return batch, err
	}
	defer tx.Rollback()
	// Even very short custom policies cannot remove an accepting task. All
	// epochs allow at most 60s execution plus the 15s transport grace period.
	raw := max(int64(0), min(floorTime(now.Add(-policy.Raw), time.Minute).Unix(), floorTime(now.Add(-75*time.Second), time.Minute).Unix()))
	minute := max(int64(0), min(floorTime(now.Add(-policy.OneMinute), 5*time.Minute).Unix(), raw/300*300))
	floor := max(int64(0), min(floorTime(now.Add(-policy.FiveMinute), 5*time.Minute).Unix(), minute))
	if _, err = tx.ExecContext(ctx, `UPDATE network_quality_history_state SET raw_floor=MAX(raw_floor,?),minute_floor=MAX(minute_floor,?),retained_from=MAX(retained_from,?) WHERE singleton=1`, raw, minute, floor); err != nil {
		return batch, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT raw_floor,minute_floor,retained_from FROM network_quality_history_state WHERE singleton=1`).Scan(&raw, &minute, &floor); err != nil {
		return batch, err
	}
	batch.RetainedFrom = time.Unix(floor, 0).UTC()
	execDelete := func(query string, args ...any) (int64, error) {
		r, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return 0, err
		}
		return r.RowsAffected()
	}
	// Expired raw data outside the retained range needs no temporary rollup.
	deleted, err := execDelete(`DELETE FROM network_probe_tasks WHERE id IN (SELECT id FROM network_probe_tasks WHERE source='scheduled' AND scheduled_at<? ORDER BY scheduled_at LIMIT ?)`, floor, networkPruneRows)
	if err != nil {
		return batch, err
	}
	batch.TasksDeleted += deleted
	batch.More = deleted == networkPruneRows
	manual, err := execDelete(`DELETE FROM network_probe_tasks WHERE id IN (SELECT id FROM network_probe_tasks WHERE source='manual' AND expires_at_ns<? ORDER BY expires_at_ns LIMIT ?)`, min(now.Add(-policy.Raw).UnixNano(), now.UnixNano()), networkPruneRows)
	if err != nil {
		return batch, err
	}
	batch.TasksDeleted += manual
	batch.More = batch.More || manual == networkPruneRows
	for range networkArchiveBuckets {
		var epoch, target string
		var slot, anchor, interval int64
		err = tx.QueryRowContext(ctx, `SELECT t.epoch_id,t.scheduled_at,e.target_id,e.starts_at,e.interval_seconds FROM network_probe_tasks t JOIN network_schedule_epochs e ON e.id=t.epoch_id WHERE t.source='scheduled' AND t.scheduled_at>=? AND t.scheduled_at<? ORDER BY t.scheduled_at,t.epoch_id LIMIT 1`, floor, raw).Scan(&epoch, &slot, &target, &anchor, &interval)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return batch, err
		}
		start := slot / 60 * 60
		segment, err := readNetworkRawSegment(ctx, tx, epoch, target, anchor, interval, start, start+60)
		if err != nil {
			return batch, err
		}
		summary, err := segment.MarshalBinary()
		if err != nil {
			return batch, ErrNetworkQualityUnavailable
		}
		// A conflict means a broken atomicity invariant. Never overwrite evidence.
		if _, err = tx.ExecContext(ctx, `INSERT INTO network_quality_rollups(epoch_id,bucket_seconds,bucket_at,summary) VALUES(?,60,?,?)`, epoch, start, summary); err != nil {
			return batch, err
		}
		n, err := execDelete(`DELETE FROM network_probe_tasks WHERE epoch_id=? AND source='scheduled' AND scheduled_at>=? AND scheduled_at<?`, epoch, start, start+60)
		if err != nil {
			return batch, err
		}
		batch.TasksDeleted += n
		batch.RawBuckets++
	}
	batch.More = batch.More || batch.RawBuckets == networkArchiveBuckets
	for range networkArchiveBuckets {
		var epoch string
		var bucket, anchor, interval int64
		err = tx.QueryRowContext(ctx, `SELECT r.epoch_id,r.bucket_at,e.starts_at,e.interval_seconds FROM network_quality_rollups r JOIN network_schedule_epochs e ON e.id=r.epoch_id WHERE r.bucket_seconds=60 AND r.bucket_at>=? AND r.bucket_at<? AND NOT EXISTS(SELECT 1 FROM network_probe_tasks t WHERE t.epoch_id=r.epoch_id AND t.source='scheduled' AND t.scheduled_at>=r.bucket_at/300*300 AND t.scheduled_at<r.bucket_at/300*300+300) ORDER BY r.bucket_at,r.epoch_id LIMIT 1`, floor, minute).Scan(&epoch, &bucket, &anchor, &interval)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return batch, err
		}
		start := bucket / 300 * 300
		segment, err := quality.NewSegment(epoch, anchor, interval)
		if err != nil {
			return batch, ErrNetworkQualityUnavailable
		}
		rows, err := tx.QueryContext(ctx, `SELECT bucket_at,CASE WHEN length(summary)<=? THEN summary END FROM network_quality_rollups WHERE epoch_id=? AND bucket_seconds=60 AND bucket_at>=? AND bucket_at<? ORDER BY bucket_at`, quality.MaxSegmentBytes, epoch, start, start+300)
		if err != nil {
			return batch, err
		}
		for rows.Next() {
			var at int64
			var payload []byte
			if err = rows.Scan(&at, &payload); err != nil {
				break
			}
			var part quality.Segment
			part, err = decodeNetworkBucket(epoch, anchor, interval, at, 60, payload)
			if err == nil {
				err = segment.MergeEpoch(part)
			}
			if err != nil {
				break
			}
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return batch, ErrNetworkQualityUnavailable
		}
		summary, err := segment.MarshalBinary()
		if err != nil {
			return batch, ErrNetworkQualityUnavailable
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO network_quality_rollups(epoch_id,bucket_seconds,bucket_at,summary) VALUES(?,300,?,?)`, epoch, start, summary); err != nil {
			return batch, err
		}
		n, err := execDelete(`DELETE FROM network_quality_rollups WHERE epoch_id=? AND bucket_seconds=60 AND bucket_at>=? AND bucket_at<?`, epoch, start, start+300)
		if err != nil {
			return batch, err
		}
		batch.RollupsDeleted += n
		batch.MinuteBuckets++
	}
	batch.More = batch.More || batch.MinuteBuckets == networkArchiveBuckets
	for _, level := range []int{60, 300} {
		n, err := execDelete(`DELETE FROM network_quality_rollups WHERE (epoch_id,bucket_seconds,bucket_at) IN (SELECT epoch_id,bucket_seconds,bucket_at FROM network_quality_rollups WHERE bucket_seconds=? AND bucket_at<? ORDER BY bucket_at LIMIT ?)`, level, floor, networkPruneRows)
		if err != nil {
			return batch, err
		}
		batch.RollupsDeleted += n
		batch.More = batch.More || n == networkPruneRows
	}
	batch.EpochsDeleted, err = execDelete(`DELETE FROM network_schedule_epochs WHERE id IN (SELECT e.id FROM network_schedule_epochs e WHERE e.ends_at IS NOT NULL AND e.ends_at<=? AND NOT EXISTS(SELECT 1 FROM network_probe_tasks t WHERE t.epoch_id=e.id) AND NOT EXISTS(SELECT 1 FROM network_quality_rollups r WHERE r.epoch_id=e.id) ORDER BY e.ends_at LIMIT ?)`, floor, networkPruneRows)
	if err != nil {
		return batch, err
	}
	batch.More = batch.More || batch.EpochsDeleted == networkPruneRows
	if err = tx.Commit(); err != nil {
		return NetworkQualityRetentionBatch{}, err
	}
	return batch, nil
}

func decodeNetworkBucket(epoch string, anchor, interval, start, size int64, payload []byte) (quality.Segment, error) {
	s, err := quality.DecodeSegment(epoch, anchor, interval, payload)
	if err != nil || s.Totals.Observed == 0 || s.First.Slot < start || s.Last.Slot >= start+size || start%size != 0 {
		return quality.Segment{}, ErrNetworkQualityUnavailable
	}
	return s, nil
}
