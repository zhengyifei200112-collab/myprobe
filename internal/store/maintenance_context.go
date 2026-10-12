package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type MaintenanceWindowRef struct {
	ID         string     `json:"id"`
	Reason     string     `json:"reason"`
	Timezone   string     `json:"timezone"`
	StartsAt   time.Time  `json:"starts_at"`
	EndsAt     time.Time  `json:"ends_at"`
	CanceledAt *time.Time `json:"canceled_at,omitempty"`
}
type MaintenanceDecision struct {
	Kind    string    `json:"kind"`
	FirstAt time.Time `json:"first_at"`
	LastAt  time.Time `json:"last_at"`
}
type IncidentMaintenanceWindow struct {
	MaintenanceWindowRef
	Active    bool                  `json:"active"`
	Decisions []MaintenanceDecision `json:"decisions"`
}
type IncidentMaintenancePage struct {
	Windows           []IncidentMaintenanceWindow `json:"windows"`
	NextCursor        string                      `json:"next_cursor"`
	CheckedAt         time.Time                   `json:"checked_at"`
	NodeExists        bool                        `json:"node_exists"`
	NodeInMaintenance bool                        `json:"node_in_maintenance"`
}
type DeliveryMaintenanceWindow struct {
	MaintenanceWindowRef
	RecordedAt time.Time `json:"recorded_at"`
}
type DeliveryMaintenancePage struct {
	Windows    []DeliveryMaintenanceWindow `json:"windows"`
	NextCursor string                      `json:"next_cursor"`
}

const maintenanceRefColumns = `w.id,w.reason,w.timezone,w.starts_at_ms,w.ends_at_ms,w.canceled_at_ms`

func scanMaintenanceRef(row interface{ Scan(...any) error }) (MaintenanceWindowRef, error) {
	var w MaintenanceWindowRef
	var start, end int64
	var canceled sql.NullInt64
	if err := row.Scan(&w.ID, &w.Reason, &w.Timezone, &start, &end, &canceled); err != nil {
		return w, err
	}
	w.StartsAt, w.EndsAt = time.UnixMilli(start).UTC(), time.UnixMilli(end).UTC()
	if canceled.Valid {
		at := time.UnixMilli(canceled.Int64).UTC()
		w.CanceledAt = &at
	}
	return w, nil
}
func maintenanceRefs(ctx context.Context, tx *sql.Tx, query string, args []any, limit int) ([]MaintenanceWindowRef, string, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := []MaintenanceWindowRef{}
	for rows.Next() {
		w, err := scanMaintenanceRef(rows)
		if err != nil {
			return nil, "", err
		}
		items = append(items, w)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		next = items[len(items)-1].ID
	}
	return items, next, nil
}

// ListIncidentMaintenance reads current frozen-membership scope and recorded
// decisions in one snapshot. It never backfills causal history from overlap.
func (s *Store) ListIncidentMaintenance(ctx context.Context, id, after string, limit int, at time.Time) (IncidentMaintenancePage, error) {
	page := IncidentMaintenancePage{Windows: []IncidentMaintenanceWindow{}, CheckedAt: at.UTC().Truncate(time.Millisecond)}
	if id == "" || len(id) > 128 || len(after) > 128 || limit < 1 || limit > 100 || at.IsZero() {
		return page, ErrInvalidMaintenance
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	var nodeID string
	err = tx.QueryRowContext(ctx, `SELECT node_id FROM alert_incidents WHERE id=?`, id).Scan(&nodeID)
	if errors.Is(err, sql.ErrNoRows) {
		return page, ErrNotFound
	}
	if err != nil {
		return page, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM nodes WHERE id=?)`, nodeID).Scan(&page.NodeExists); err != nil {
		return page, err
	}
	if page.NodeExists {
		page.NodeInMaintenance, err = nodeInMaintenance(ctx, tx, nodeID, page.CheckedAt)
		if err != nil {
			return page, err
		}
	}
	query := `SELECT ` + maintenanceRefColumns + ` FROM maintenance_windows w WHERE w.id>? AND (
		EXISTS(SELECT 1 FROM incident_maintenance_decisions d WHERE d.incident_id=? AND d.window_id=w.id)
		OR (? AND EXISTS(SELECT 1 FROM maintenance_window_nodes n WHERE n.window_id=w.id AND n.node_id=?) AND (` + activeMaintenancePredicate + `))) ORDER BY w.id LIMIT ?`
	refs, next, err := maintenanceRefs(ctx, tx, query, []any{after, id, page.NodeExists, nodeID, page.CheckedAt.UnixMilli(), page.CheckedAt.UnixMilli(), page.CheckedAt.UnixMilli(), limit + 1}, limit)
	if err != nil {
		return page, err
	}
	page.NextCursor = next
	for _, w := range refs {
		item := IncidentMaintenanceWindow{MaintenanceWindowRef: w, Decisions: []MaintenanceDecision{}, Active: page.NodeExists && !page.CheckedAt.Before(w.StartsAt) && page.CheckedAt.Before(w.EndsAt) && (w.CanceledAt == nil || page.CheckedAt.Before(*w.CanceledAt))}
		rows, err := tx.QueryContext(ctx, `SELECT kind,first_at_ms,last_at_ms FROM incident_maintenance_decisions WHERE incident_id=? AND window_id=? ORDER BY kind`, id, w.ID)
		if err != nil {
			return page, err
		}
		for rows.Next() {
			var d MaintenanceDecision
			var first, last int64
			if err := rows.Scan(&d.Kind, &first, &last); err != nil {
				rows.Close()
				return page, err
			}
			d.FirstAt, d.LastAt = time.UnixMilli(first).UTC(), time.UnixMilli(last).UTC()
			item.Decisions = append(item.Decisions, d)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return page, err
		}
		page.Windows = append(page.Windows, item)
	}
	return page, tx.Commit()
}

// ListDeliveryMaintenance returns only links recorded while canceling this job.
func (s *Store) ListDeliveryMaintenance(ctx context.Context, id, after string, limit int) (DeliveryMaintenancePage, error) {
	page := DeliveryMaintenancePage{Windows: []DeliveryMaintenanceWindow{}}
	if id == "" || len(id) > 128 || len(after) > 128 || limit < 1 || limit > 100 {
		return page, ErrInvalidMaintenance
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	var exists int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM notification_deliveries WHERE id=?`, id).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return page, ErrNotFound
	}
	if err != nil {
		return page, err
	}
	refs, next, err := maintenanceRefs(ctx, tx, `SELECT `+maintenanceRefColumns+` FROM maintenance_windows w JOIN delivery_maintenance_links d ON d.window_id=w.id WHERE d.delivery_id=? AND w.id>? ORDER BY w.id LIMIT ?`, []any{id, after, limit + 1}, limit)
	if err != nil {
		return page, err
	}
	page.NextCursor = next
	for _, w := range refs {
		var recorded int64
		if err := tx.QueryRowContext(ctx, `SELECT recorded_at_ms FROM delivery_maintenance_links WHERE delivery_id=? AND window_id=?`, id, w.ID).Scan(&recorded); err != nil {
			return page, err
		}
		page.Windows = append(page.Windows, DeliveryMaintenanceWindow{MaintenanceWindowRef: w, RecordedAt: time.UnixMilli(recorded).UTC()})
	}
	return page, tx.Commit()
}
