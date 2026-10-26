package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

const activeMaintenancePredicate = `w.starts_at_ms<=? AND w.ends_at_ms>? AND (w.canceled_at_ms IS NULL OR w.canceled_at_ms>?)`
const overlappingMaintenancePredicate = `w.starts_at_ms<? AND w.ends_at_ms>? AND (w.canceled_at_ms IS NULL OR (w.canceled_at_ms>w.starts_at_ms AND w.canceled_at_ms>?))`

func matchingMaintenanceIDs(ctx context.Context, q maintenanceSelectionQuery, nodeID string, start, end time.Time, overlap bool) ([]string, error) {
	if nodeID == "" || end.IsZero() || (overlap && (start.IsZero() || !end.After(start))) {
		return nil, ErrInvalidMaintenance
	}
	predicate := activeMaintenancePredicate
	args := []any{nodeID, end.UnixMilli(), end.UnixMilli(), end.UnixMilli()}
	if overlap {
		upper := end.UnixMilli()
		if time.UnixMilli(upper).Before(end) {
			upper++
		}
		predicate = overlappingMaintenancePredicate
		args = []any{nodeID, upper, start.UnixMilli(), start.UnixMilli()}
	}
	rows, err := q.QueryContext(ctx, `SELECT w.id FROM maintenance_windows w JOIN maintenance_window_nodes n ON n.window_id=w.id WHERE n.node_id=? AND (`+predicate+`) ORDER BY w.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Called before canceling jobs while the writer is held. Empty deliveryID links
// only jobs that this observation is about to cancel; a worker passes its job ID.
func recordMaintenanceDecision(ctx context.Context, tx *sql.Tx, incidentID string, windowIDs []string, kind string, at time.Time, deliveryID string) error {
	// Keep statement/parameter size bounded even at the 1000-window limit. All
	// batches share the caller's writer transaction, including link/cancel work.
	for len(windowIDs) > 0 {
		batch := windowIDs[:min(len(windowIDs), 200)]
		windowIDs = windowIDs[len(batch):]
		matched := `WITH matched(window_id) AS (VALUES ` + strings.TrimSuffix(strings.Repeat(`(?),`, len(batch)), ",") + `) `
		args := make([]any, 0, len(batch)+4)
		for _, id := range batch {
			args = append(args, id)
		}
		decisionArgs := append(args, incidentID, kind, at.UnixMilli(), at.UnixMilli())
		if _, err := tx.ExecContext(ctx, matched+`INSERT INTO incident_maintenance_decisions(incident_id,window_id,kind,first_at_ms,last_at_ms) SELECT ?,window_id,?,?,? FROM matched WHERE 1 ON CONFLICT(incident_id,window_id,kind) DO UPDATE SET first_at_ms=min(first_at_ms,excluded.first_at_ms),last_at_ms=max(last_at_ms,excluded.last_at_ms)`, decisionArgs...); err != nil {
			return err
		}
		query := matched + `INSERT INTO delivery_maintenance_links(delivery_id,window_id,recorded_at_ms) SELECT d.id,m.window_id,? FROM notification_deliveries d CROSS JOIN matched m WHERE d.incident_id=? AND d.status IN ('pending','inflight')`
		linkArgs := append(args, at.UnixMilli(), incidentID)
		if deliveryID != "" {
			query += ` AND d.id=?`
			linkArgs = append(linkArgs, deliveryID)
		}
		query += ` ON CONFLICT(delivery_id,window_id) DO NOTHING`
		if _, err := tx.ExecContext(ctx, query, linkArgs...); err != nil {
			return err
		}
	}
	return nil
}
