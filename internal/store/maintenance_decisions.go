package store

import (
	"context"
	"database/sql"
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
	for _, id := range windowIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO incident_maintenance_decisions(incident_id,window_id,kind,first_at_ms,last_at_ms) VALUES(?,?,?,?,?) ON CONFLICT(incident_id,window_id,kind) DO UPDATE SET first_at_ms=min(first_at_ms,excluded.first_at_ms),last_at_ms=max(last_at_ms,excluded.last_at_ms)`, incidentID, id, kind, at.UnixMilli(), at.UnixMilli()); err != nil {
			return err
		}
		query := `INSERT INTO delivery_maintenance_links(delivery_id,window_id,recorded_at_ms) SELECT id,?,? FROM notification_deliveries WHERE incident_id=? AND status IN ('pending','inflight')`
		args := []any{id, at.UnixMilli(), incidentID}
		if deliveryID != "" {
			query += ` AND id=?`
			args = append(args, deliveryID)
		}
		query += ` ON CONFLICT(delivery_id,window_id) DO NOTHING`
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return err
		}
	}
	return nil
}
