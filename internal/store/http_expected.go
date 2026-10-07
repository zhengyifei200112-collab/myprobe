package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Preserve nanosecond anchors exactly; SQLite date functions round fractions.
// Only the current revision can be reconstructed from a pre-018 database.
func seedHTTPScheduleEpochs(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT s.id,a.node_id,s.revision,s.updated_at,s.interval_seconds FROM http_services s JOIN http_service_nodes a ON a.service_id=s.id WHERE s.enabled=1`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var service, node, raw string
		var revision, seconds int64
		if err = rows.Scan(&service, &node, &revision, &raw, &seconds); err != nil {
			return err
		}
		anchor, err := parseTime(raw)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO http_schedule_epochs(service_id,node_id,revision,start_ns,interval_seconds) VALUES(?,?,?,?,?)`, service, node, revision, anchor.UnixNano(), seconds); err != nil {
			return err
		}
	}
	return rows.Err()
}

// CountHTTPExpected counts planned slots in [start,end), independent of whether
// dispatch succeeded. It is an internal primitive, before maintenance exclusions.
// Callers must bound end to the observation time when reporting historical data.
func (s *Store) CountHTTPExpected(ctx context.Context, serviceID, nodeID string, start, end time.Time) (int64, error) {
	if !end.After(start) || end.Sub(start) > 31*24*time.Hour || start.Year() < 1970 || end.Year() > 2100 {
		return 0, errors.New("invalid HTTP statistics range")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT start_ns,end_ns,interval_seconds FROM http_schedule_epochs WHERE service_id=? AND node_id=? AND start_ns<? AND (end_ns IS NULL OR end_ns>?) ORDER BY start_ns LIMIT 10001`, serviceID, nodeID, end.UnixNano(), start.UnixNano())
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var total int64
	count := 0
	for rows.Next() {
		var anchor int64
		var until sql.NullInt64
		var seconds int64
		if err = rows.Scan(&anchor, &until, &seconds); err != nil {
			return 0, err
		}
		count++
		if count > 10000 {
			return 0, errors.New("HTTP statistics range contains too many revisions")
		}
		stop := end.UnixNano()
		if until.Valid && until.Int64 < stop {
			stop = until.Int64
		}
		total += countHTTPSlots(anchor, seconds*int64(time.Second), start.UnixNano(), stop)
	}
	return total, rows.Err()
}

func countHTTPSlots(anchor, period, start, end int64) int64 {
	if period <= 0 || end <= anchor || end <= start {
		return 0
	}
	first := anchor
	if start > anchor {
		delta := start - anchor
		steps := delta / period
		if delta%period != 0 {
			steps++
		}
		first = anchor + steps*period
	}
	if first >= end {
		return 0
	}
	return 1 + (end-1-first)/period
}
