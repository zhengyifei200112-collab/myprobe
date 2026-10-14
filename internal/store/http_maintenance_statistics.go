package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"
)

type httpMaintenanceInterval struct{ start, end int64 }

const httpMaintenanceIntervalsQuery = `SELECT w.starts_at_ms,w.ends_at_ms,w.canceled_at_ms,w.created_at FROM maintenance_window_nodes n JOIN maintenance_windows w ON w.id=n.window_id WHERE n.node_id=? AND w.starts_at_ms<? AND w.ends_at_ms>? ORDER BY w.starts_at_ms,w.id LIMIT 1001`

// Windows/membership share the statistics transaction. CreatedAt prevents a
// subsequently backdated window from removing earlier planned slots.
func httpMaintenanceIntervals(ctx context.Context, tx *sql.Tx, nodeID string, start, end time.Time) ([]httpMaintenanceInterval, error) {
	upper := end.UnixMilli()
	if time.UnixMilli(upper).Before(end) {
		upper++
	}
	rows, err := tx.QueryContext(ctx, httpMaintenanceIntervalsQuery, nodeID, upper, start.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	intervals := []httpMaintenanceInterval{}
	count := 0
	for rows.Next() {
		var from, to int64
		var canceled sql.NullInt64
		var raw string
		if err := rows.Scan(&from, &to, &canceled, &raw); err != nil {
			return nil, err
		}
		count++
		if count > 1000 {
			return nil, errors.New("too many HTTP maintenance windows")
		}
		created, err := parseTime(raw)
		if err != nil {
			return nil, err
		}
		lo, hi := time.UnixMilli(from).UTC(), time.UnixMilli(to).UTC()
		if created.After(lo) {
			lo = created
		}
		if canceled.Valid && time.UnixMilli(canceled.Int64).Before(hi) {
			hi = time.UnixMilli(canceled.Int64).UTC()
		}
		if lo.Before(start) {
			lo = start
		}
		if hi.After(end) {
			hi = end
		}
		if hi.After(lo) {
			intervals = append(intervals, httpMaintenanceInterval{lo.UnixNano(), hi.UnixNano()})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].start < intervals[j].start })
	merged := make([]httpMaintenanceInterval, 0, len(intervals))
	for _, interval := range intervals {
		if len(merged) > 0 && interval.start <= merged[len(merged)-1].end {
			merged[len(merged)-1].end = max(merged[len(merged)-1].end, interval.end)
		} else {
			merged = append(merged, interval)
		}
	}
	return merged, nil
}

func httpSlotInMaintenance(intervals []httpMaintenanceInterval, at int64) bool {
	i := sort.Search(len(intervals), func(i int) bool { return intervals[i].end > at })
	return i < len(intervals) && intervals[i].start <= at
}
