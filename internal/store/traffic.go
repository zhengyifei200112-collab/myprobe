package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type trafficSample struct {
	at     time.Time
	rx, tx uint64
}
type sqlQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func billingPeriod(now time.Time, resetDay *int) (time.Time, time.Time) {
	now = now.UTC()
	day := 1
	if resetDay != nil {
		day = *resetDay
	}
	start := monthBoundary(now.Year(), now.Month(), day)
	if now.Before(start) {
		previous := time.Date(start.Year(), start.Month()-1, 1, 0, 0, 0, 0, time.UTC)
		start = monthBoundary(previous.Year(), previous.Month(), day)
	}
	nextMonth := time.Date(start.Year(), start.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	end := monthBoundary(nextMonth.Year(), nextMonth.Month(), day)
	return start, end
}
func monthBoundary(year int, month time.Month, day int) time.Time {
	last := time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if day > last {
		day = last
	}
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

func (s *Store) TrafficUsage(ctx context.Context, nodeID string, resetDay *int, now time.Time) (TrafficUsage, error) {
	start, end := billingPeriod(now, resetDay)
	var storedStart, storedEnd string
	var storedRX, storedTX int64
	err := s.db.QueryRowContext(ctx, `SELECT period_start,period_end,cycle_rx_bytes,cycle_tx_bytes FROM traffic_state WHERE node_id=?`, nodeID).Scan(&storedStart, &storedEnd, &storedRX, &storedTX)
	if err == nil {
		parsedStart, _ := parseTime(storedStart)
		parsedEnd, _ := parseTime(storedEnd)
		if parsedStart.Equal(start) {
			return TrafficUsage{PeriodStart: parsedStart, PeriodEnd: parsedEnd, RXBytes: uint64(max(storedRX, 0)), TXBytes: uint64(max(storedTX, 0))}, nil
		}
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return TrafficUsage{}, err
	}
	samples, err := s.trafficSamples(ctx, nodeID, start, now)
	if err != nil {
		return TrafficUsage{}, err
	}
	rx, tx := trafficDelta(samples)
	return TrafficUsage{PeriodStart: start, PeriodEnd: end, RXBytes: rx, TXBytes: tx}, nil
}

func (s *Store) updateTrafficState(ctx context.Context, tx *sql.Tx, nodeID string, captured time.Time, rxTotal, txTotal uint64) error {
	var reset sql.NullInt64
	if err := tx.QueryRowContext(ctx, "SELECT traffic_reset_day FROM nodes WHERE id=?", nodeID).Scan(&reset); err != nil {
		return err
	}
	var resetDay *int
	if reset.Valid {
		value := int(reset.Int64)
		resetDay = &value
	}
	start, end := billingPeriod(captured, resetDay)
	var storedStart, lastCaptured string
	var lastRX, lastTX, cycleRX, cycleTX int64
	err := tx.QueryRowContext(ctx, `SELECT period_start,last_captured_at,last_rx_total,last_tx_total,cycle_rx_bytes,cycle_tx_bytes FROM traffic_state WHERE node_id=?`, nodeID).Scan(&storedStart, &lastCaptured, &lastRX, &lastTX, &cycleRX, &cycleTX)
	if errors.Is(err, sql.ErrNoRows) {
		samples, sampleErr := queryTrafficSamples(ctx, tx, nodeID, start, captured)
		if sampleErr != nil {
			return sampleErr
		}
		backfillRX, backfillTX := trafficDelta(samples)
		_, err = tx.ExecContext(ctx, `INSERT INTO traffic_state(node_id,period_start,period_end,last_captured_at,last_rx_total,last_tx_total,cycle_rx_bytes,cycle_tx_bytes,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, nodeID, formatTime(start), formatTime(end), formatTime(captured), rxTotal, txTotal, backfillRX, backfillTX, nowText())
		return err
	}
	if err != nil {
		return err
	}
	previousAt, _ := parseTime(lastCaptured)
	if !captured.After(previousAt) {
		return nil
	}
	if storedStart != formatTime(start) {
		cycleRX = 0
		cycleTX = 0
	} else {
		cycleRX += int64(counterDelta(uint64(max(lastRX, 0)), rxTotal))
		cycleTX += int64(counterDelta(uint64(max(lastTX, 0)), txTotal))
	}
	if storedStart != formatTime(start) {
		samples, sampleErr := queryTrafficSamples(ctx, tx, nodeID, start, captured)
		if sampleErr != nil {
			return sampleErr
		}
		backfillRX, backfillTX := trafficDelta(samples)
		cycleRX = int64(backfillRX)
		cycleTX = int64(backfillTX)
	}
	_, err = tx.ExecContext(ctx, `UPDATE traffic_state SET period_start=?,period_end=?,last_captured_at=?,last_rx_total=?,last_tx_total=?,cycle_rx_bytes=?,cycle_tx_bytes=?,updated_at=? WHERE node_id=?`, formatTime(start), formatTime(end), formatTime(captured), rxTotal, txTotal, cycleRX, cycleTX, nowText(), nodeID)
	return err
}

func (s *Store) TrafficHistory(ctx context.Context, nodeID string, start, end time.Time, bucketSeconds int) ([]TrafficHistoryPoint, error) {
	if bucketSeconds < 1 {
		return nil, errors.New("invalid traffic bucket")
	}
	samples, err := s.trafficSamples(ctx, nodeID, start, end)
	if err != nil {
		return nil, err
	}
	if len(samples) < 2 {
		return []TrafficHistoryPoint{}, nil
	}
	result := make([]TrafficHistoryPoint, 0)
	var rxTotal, txTotal uint64
	previous := samples[0]
	var bucket int64 = -1
	for _, current := range samples[1:] {
		rxTotal += counterDelta(previous.rx, current.rx)
		txTotal += counterDelta(previous.tx, current.tx)
		currentBucket := current.at.Unix() / int64(bucketSeconds) * int64(bucketSeconds)
		if currentBucket != bucket {
			result = append(result, TrafficHistoryPoint{Time: time.Unix(currentBucket, 0).UTC(), RXBytes: rxTotal, TXBytes: txTotal, Total: rxTotal + txTotal})
			bucket = currentBucket
		} else {
			last := &result[len(result)-1]
			last.RXBytes = rxTotal
			last.TXBytes = txTotal
			last.Total = rxTotal + txTotal
		}
		previous = current
	}
	return result, nil
}

func (s *Store) trafficSamples(ctx context.Context, nodeID string, start, end time.Time) ([]trafficSample, error) {
	return queryTrafficSamples(ctx, s.db, nodeID, start, end)
}
func queryTrafficSamples(ctx context.Context, q sqlQueryer, nodeID string, start, end time.Time) ([]trafficSample, error) {
	rows, err := q.QueryContext(ctx, `SELECT m.captured_at,m.net_rx_total,m.net_tx_total FROM metric_samples m JOIN nodes n ON n.id=m.node_id WHERE m.node_id=? AND n.hidden=0 AND m.captured_at>=? AND m.captured_at<=? ORDER BY m.captured_at`, nodeID, formatTime(start), formatTime(end))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]trafficSample, 0)
	for rows.Next() {
		var raw string
		var rx, tx int64
		if err := rows.Scan(&raw, &rx, &tx); err != nil {
			return nil, err
		}
		at, err := parseTime(raw)
		if err != nil {
			return nil, err
		}
		items = append(items, trafficSample{at: at, rx: uint64(max(rx, 0)), tx: uint64(max(tx, 0))})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}
func trafficDelta(samples []trafficSample) (uint64, uint64) {
	if len(samples) < 2 {
		return 0, 0
	}
	var rx, tx uint64
	previous := samples[0]
	for _, current := range samples[1:] {
		rx += counterDelta(previous.rx, current.rx)
		tx += counterDelta(previous.tx, current.tx)
		previous = current
	}
	return rx, tx
}
func counterDelta(previous, current uint64) uint64 {
	if current >= previous {
		return current - previous
	}
	return current
}
