package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
)

// HTTPStatistics is an internal per-observer summary, before maintenance support.
// Rates use fractions [0,1], with nil for an empty denominator.
type HTTPStatistics struct {
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	Expected    int64     `json:"expected"`
	Success     int64     `json:"success"`
	Failure     int64     `json:"failure"`
	Unobserved  int64     `json:"unobserved"`
	Missing     int64     `json:"missing"`
	SuccessRate *float64  `json:"success_rate"`
	Coverage    *float64  `json:"coverage"`
}

// HTTPServiceStatistics summarizes only mature slots: the conservative 126s lag
// includes the 5s dispatch window, 60s request limit, 60s receipt grace and cleanup.
// Both counters and results share one SQLite snapshot. Historical coverage before
// retained schedule epochs must be handled by the API before exposing this method.
func (s *Store) HTTPServiceStatistics(ctx context.Context, serviceID, nodeID string, start, end, now time.Time) (HTTPStatistics, error) {
	result := HTTPStatistics{Start: start.UTC(), End: end.UTC()}
	if !end.After(start) || end.Sub(start) > 31*24*time.Hour || start.Year() < 1970 || end.Year() > 2100 {
		return result, errors.New("invalid HTTP statistics range")
	}
	cutoff := now.Add(-126 * time.Second)
	if result.End.After(cutoff) {
		result.End = cutoff.UTC()
	}
	if result.End.Before(result.Start) {
		result.End = result.Start
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	var exists int
	if err = tx.QueryRowContext(ctx, `SELECT 1 FROM http_services WHERE id=?`, serviceID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return result, ErrNotFound
	} else if err != nil {
		return result, err
	}
	if result.End.Equal(result.Start) {
		return result, nil
	}
	type epoch struct{ start, end, period int64 }
	epochs := make(map[int64]epoch)
	rows, err := tx.QueryContext(ctx, `SELECT revision,start_ns,end_ns,interval_seconds FROM http_schedule_epochs WHERE service_id=? AND node_id=? AND start_ns<? AND (end_ns IS NULL OR end_ns>?) LIMIT 10001`, serviceID, nodeID, result.End.UnixNano(), start.UnixNano())
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var revision, anchor, seconds int64
		var until sql.NullInt64
		if err = rows.Scan(&revision, &anchor, &until, &seconds); err != nil {
			rows.Close()
			return result, err
		}
		if len(epochs) >= 10000 {
			rows.Close()
			return result, errors.New("too many HTTP schedule revisions")
		}
		stop := result.End.UnixNano()
		if until.Valid && until.Int64 < stop {
			stop = until.Int64
		}
		e := epoch{anchor, stop, seconds * int64(time.Second)}
		epochs[revision] = e
		result.Expected += countHTTPSlots(e.start, e.period, start.UnixNano(), e.end)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	// Widen the text index bounds by a second, then apply exact Go timestamps;
	// RFC3339Nano text has variable fractional widths and is not an exact ordering.
	rows, err = tx.QueryContext(ctx, `SELECT revision,scheduled_at,result_json FROM http_tasks WHERE service_id=? AND node_id=? AND scheduled_at>=? AND scheduled_at<? AND result_json IS NOT NULL LIMIT 100001`, serviceID, nodeID, formatTime(start.Truncate(time.Second).Add(-time.Second)), formatTime(result.End.Truncate(time.Second).Add(time.Second)))
	if err != nil {
		return result, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var revision int64
		var raw, body string
		if err = rows.Scan(&revision, &raw, &body); err != nil {
			return result, err
		}
		count++
		if count > 100000 {
			return result, errors.New("too many HTTP results")
		}
		at, err := parseTime(raw)
		if err != nil {
			return result, err
		}
		e, ok := epochs[revision]
		if !ok || at.Before(start) || !at.Before(result.End) || at.UnixNano() < e.start || at.UnixNano() >= e.end || (at.UnixNano()-e.start)%e.period != 0 {
			continue
		}
		var value httpcheck.Result
		if err = json.Unmarshal([]byte(body), &value); err != nil {
			return result, err
		}
		switch value.Outcome {
		case "success":
			result.Success++
		case "failure":
			result.Failure++
		case "unobserved":
			result.Unobserved++
		default:
			return result, errors.New("invalid stored HTTP outcome")
		}
	}
	if err = rows.Err(); err != nil {
		return result, err
	}
	valid := result.Success + result.Failure
	result.Missing = result.Expected - valid
	if result.Missing < 0 || result.Unobserved > result.Missing {
		return result, errors.New("inconsistent HTTP sample counts")
	}
	if valid > 0 {
		value := float64(result.Success) / float64(valid)
		result.SuccessRate = &value
	}
	if result.Expected > 0 {
		value := float64(valid) / float64(result.Expected)
		result.Coverage = &value
	}
	return result, nil
}
