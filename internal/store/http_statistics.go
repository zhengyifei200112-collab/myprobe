package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
)

// HTTPStatisticsCounts holds outcome counts for one declared slot denominator.
// Rates use fractions [0,1], with nil for an empty denominator.
type HTTPStatisticsCounts struct {
	Expected    int64    `json:"expected"`
	Success     int64    `json:"success"`
	Failure     int64    `json:"failure"`
	Unobserved  int64    `json:"unobserved"`
	Missing     int64    `json:"missing"`
	SuccessRate *float64 `json:"success_rate"`
	Coverage    *float64 `json:"coverage"`
}
type HTTPStatistics struct {
	HTTPStatisticsCounts
	Raw                       HTTPStatisticsCounts `json:"raw"`
	MaintenanceExcludedCounts HTTPStatisticsCounts `json:"maintenance_excluded_counts"`
	MaintenanceExcluded       bool                 `json:"maintenance_excluded"`
	MaintenancePolicy         string               `json:"maintenance_policy"`
	RequestedStart            time.Time            `json:"requested_start"`
	RequestedEnd              time.Time            `json:"requested_end"`
	RetainedFrom              time.Time            `json:"retained_from"`
	ScheduleKnownFrom         time.Time            `json:"schedule_known_from"`
	Start                     time.Time            `json:"start"`
	End                       time.Time            `json:"end"`
}

// HTTPServiceStatistics summarizes only mature slots: the conservative 126s lag
// includes the 5s dispatch window, 60s request limit, 60s receipt grace and cleanup.
// Original/excluded counters, maintenance windows and results share one SQLite
// snapshot, clipped to retained and reconstructable schedule history. Omitting
// excludeMaintenance keeps the original raw result for existing callers.
func (s *Store) HTTPServiceStatistics(ctx context.Context, serviceID, nodeID string, start, end, now time.Time, excludeMaintenance ...bool) (HTTPStatistics, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return HTTPStatistics{}, err
	}
	defer tx.Rollback()
	return readHTTPServiceStatistics(ctx, tx, serviceID, nodeID, start, end, now, excludeMaintenance...)
}

func readHTTPServiceStatistics(ctx context.Context, tx *sql.Tx, serviceID, nodeID string, start, end, now time.Time, excludeMaintenance ...bool) (HTTPStatistics, error) {
	result := HTTPStatistics{Start: start.UTC(), End: end.UTC(), RequestedStart: start.UTC(), RequestedEnd: end.UTC(), MaintenancePolicy: "planned_slot_effective_window_v1"}
	if len(excludeMaintenance) > 1 || now.IsZero() || now.Year() < 1970 || now.Year() > 2100 || !end.After(start) || end.Sub(start) > 31*24*time.Hour || start.Year() < 1970 || end.Year() > 2100 {
		return result, errors.New("invalid HTTP statistics range")
	}
	result.MaintenanceExcluded = len(excludeMaintenance) == 1 && excludeMaintenance[0]
	cutoff := now.Add(-126 * time.Second)
	if result.End.After(cutoff) {
		result.End = cutoff.UTC()
	}
	if result.End.Before(result.Start) {
		result.End = result.Start
	}
	var err error
	var nodeExists int
	if err = tx.QueryRowContext(ctx, `SELECT 1 FROM nodes WHERE id=?`, nodeID).Scan(&nodeExists); errors.Is(err, sql.ErrNoRows) {
		return result, ErrNotFound
	} else if err != nil {
		return result, err
	}
	var created, updated string
	var revision int64
	if err = tx.QueryRowContext(ctx, `SELECT created_at,updated_at,revision FROM http_services WHERE id=?`, serviceID).Scan(&created, &updated, &revision); errors.Is(err, sql.ErrNoRows) {
		return result, ErrNotFound
	} else if err != nil {
		return result, err
	}
	result.ScheduleKnownFrom, err = parseTime(created)
	if err != nil {
		return result, err
	}
	// A pre-epoch database cannot reconstruct older revisions. Conservatively
	// restrict to the earliest surviving service epoch unless revision 1 survives.
	// This is service-wide: a node without assignments still has zero expected slots.
	var firstRevision, firstAnchor sql.NullInt64
	if err = tx.QueryRowContext(ctx, `SELECT MIN(revision),MIN(start_ns) FROM http_schedule_epochs WHERE service_id=?`, serviceID).Scan(&firstRevision, &firstAnchor); err != nil {
		return result, err
	}
	if revision > 1 && (!firstRevision.Valid || firstRevision.Int64 > 1) {
		result.ScheduleKnownFrom, err = parseTime(updated)
		if err != nil {
			return result, err
		}
		if firstAnchor.Valid {
			result.ScheduleKnownFrom = time.Unix(0, firstAnchor.Int64).UTC()
		}
	}
	var floor int64
	if err = tx.QueryRowContext(ctx, `SELECT retained_from_ns FROM http_history_state WHERE id=1`).Scan(&floor); err != nil {
		return result, err
	}
	result.RetainedFrom = time.Unix(0, floor).UTC()
	if result.Start.Before(result.RetainedFrom) {
		result.Start = result.RetainedFrom
	}
	if result.Start.Before(result.ScheduleKnownFrom) {
		result.Start = result.ScheduleKnownFrom
	}
	if result.Start.After(result.End) {
		result.Start = result.End
	}
	start = result.Start
	if result.End.Equal(result.Start) {
		return result, nil
	}
	windows, err := httpMaintenanceIntervals(ctx, tx, nodeID, result.Start, result.End)
	if err != nil {
		return result, err
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
		result.Raw.Expected += countHTTPSlots(e.start, e.period, start.UnixNano(), e.end)
		for _, window := range windows {
			if window.end <= max(e.start, start.UnixNano()) {
				continue
			}
			if window.start >= e.end {
				break
			}
			result.MaintenanceExcludedCounts.Expected += countHTTPSlots(e.start, e.period, max(start.UnixNano(), window.start), min(e.end, window.end))
		}
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
		if err := result.Raw.addOutcome(value.Outcome); err != nil {
			return result, err
		}
		if httpSlotInMaintenance(windows, at.UnixNano()) {
			if err := result.MaintenanceExcludedCounts.addOutcome(value.Outcome); err != nil {
				return result, err
			}
		}
	}
	if err = rows.Err(); err != nil {
		return result, err
	}
	if err := result.Raw.finish(); err != nil {
		return result, err
	}
	if err := result.MaintenanceExcludedCounts.finish(); err != nil {
		return result, err
	}
	result.HTTPStatisticsCounts = result.Raw
	if result.MaintenanceExcluded {
		excluded := result.MaintenanceExcludedCounts
		result.HTTPStatisticsCounts = HTTPStatisticsCounts{Expected: result.Raw.Expected - excluded.Expected, Success: result.Raw.Success - excluded.Success, Failure: result.Raw.Failure - excluded.Failure, Unobserved: result.Raw.Unobserved - excluded.Unobserved}
		if err := result.HTTPStatisticsCounts.finish(); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (c *HTTPStatisticsCounts) addOutcome(outcome string) error {
	switch outcome {
	case "success":
		c.Success++
	case "failure":
		c.Failure++
	case "unobserved":
		c.Unobserved++
	default:
		return errors.New("invalid stored HTTP outcome")
	}
	return nil
}
func (c *HTTPStatisticsCounts) finish() error {
	valid := c.Success + c.Failure
	c.Missing = c.Expected - valid
	if c.Expected < 0 || c.Success < 0 || c.Failure < 0 || c.Unobserved < 0 || c.Missing < 0 || c.Unobserved > c.Missing {
		return errors.New("inconsistent HTTP sample counts")
	}
	if valid > 0 {
		v := float64(c.Success) / float64(valid)
		c.SuccessRate = &v
	}
	if c.Expected > 0 {
		v := float64(valid) / float64(c.Expected)
		c.Coverage = &v
	}
	return nil
}
