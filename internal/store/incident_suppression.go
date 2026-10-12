package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

func matchingSilenceIDs(ctx context.Context, q maintenanceSelectionQuery, incidentID string, start, end time.Time, overlap bool) ([]string, error) {
	if incidentID == "" || end.IsZero() || (overlap && (start.IsZero() || !end.After(start))) {
		return nil, ErrInvalidSilence
	}
	predicate := activeSilencePredicate
	args := []any{incidentID, end.UnixMilli(), end.UnixMilli(), end.UnixMilli(), end.UnixMilli()}
	if overlap {
		upper := end.UnixMilli()
		if time.UnixMilli(upper).Before(end) {
			upper++
		}
		predicate = overlappingSilencePredicate
		args = []any{incidentID, upper, start.UnixMilli(), start.UnixMilli(), start.UnixMilli()}
	}
	rows, err := q.QueryContext(ctx, `SELECT s.id FROM incident_silences s WHERE s.incident_id=? AND (`+predicate+`) ORDER BY s.id`, args...)
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
func recordSilenceDecision(ctx context.Context, tx *sql.Tx, incidentID string, ids []string, kind string, at time.Time, deliveryID string) error {
	for len(ids) > 0 {
		batch := ids[:min(len(ids), 200)]
		ids = ids[len(batch):]
		matched := `WITH matched(id) AS (VALUES ` + strings.TrimSuffix(strings.Repeat(`(?),`, len(batch)), ",") + `) `
		args := make([]any, 0, len(batch)+4)
		for _, id := range batch {
			args = append(args, id)
		}
		decisionArgs := append(args, incidentID, kind, at.UnixMilli(), at.UnixMilli())
		if _, err := tx.ExecContext(ctx, matched+`INSERT INTO incident_silence_decisions(incident_id,silence_id,kind,first_at_ms,last_at_ms) SELECT ?,id,?,?,? FROM matched WHERE 1 ON CONFLICT(silence_id,kind) DO UPDATE SET first_at_ms=min(first_at_ms,excluded.first_at_ms),last_at_ms=max(last_at_ms,excluded.last_at_ms)`, decisionArgs...); err != nil {
			return err
		}
		query := matched + `INSERT INTO delivery_silence_links(delivery_id,silence_id,recorded_at_ms) SELECT d.id,m.id,? FROM notification_deliveries d CROSS JOIN matched m WHERE d.incident_id=? AND d.status IN ('pending','inflight')`
		linkArgs := append(args, at.UnixMilli(), incidentID)
		if deliveryID != "" {
			query += ` AND d.id=?`
			linkArgs = append(linkArgs, deliveryID)
		}
		if _, err := tx.ExecContext(ctx, query+` ON CONFLICT(delivery_id,silence_id) DO NOTHING`, linkArgs...); err != nil {
			return err
		}
	}
	return nil
}
func recordNotificationSuppression(ctx context.Context, tx *sql.Tx, incidentID string, maintenanceIDs, silenceIDs []string, kind string, at time.Time, deliveryID string) (string, error) {
	maintenanceKind := kind
	if kind == "creation" {
		maintenanceKind = "delivery" // Creation found real outstanding jobs under both scopes.
	}
	if err := recordMaintenanceDecision(ctx, tx, incidentID, maintenanceIDs, maintenanceKind, at, deliveryID); err != nil {
		return "", err
	}
	if err := recordSilenceDecision(ctx, tx, incidentID, silenceIDs, kind, at, deliveryID); err != nil {
		return "", err
	}
	if len(maintenanceIDs) > 0 && len(silenceIDs) > 0 {
		return "maintenance_and_silence", nil
	}
	if len(silenceIDs) > 0 {
		return "silence", nil
	}
	return "maintenance", nil
}
func suppressNotificationDelivery(ctx context.Context, tx *sql.Tx, d NotificationDelivery, now time.Time) (bool, error) {
	var nodeID string
	if err := tx.QueryRowContext(ctx, `SELECT node_id FROM alert_incidents WHERE id=?`, d.IncidentID).Scan(&nodeID); err != nil {
		return false, err
	}
	windows, err := matchingMaintenanceIDs(ctx, tx, nodeID, time.Time{}, now, false)
	if err == nil && len(windows) == 0 && now.After(d.CreatedAt) {
		windows, err = matchingMaintenanceIDs(ctx, tx, nodeID, d.CreatedAt, now, true)
	}
	if err != nil {
		return false, err
	}
	silences, err := matchingSilenceIDs(ctx, tx, d.IncidentID, time.Time{}, now, false)
	if err == nil && len(silences) == 0 && now.After(d.CreatedAt) {
		silences, err = matchingSilenceIDs(ctx, tx, d.IncidentID, d.CreatedAt, now, true)
	}
	if err != nil || len(windows)+len(silences) == 0 {
		return false, err
	}
	cause, err := recordNotificationSuppression(ctx, tx, d.IncidentID, windows, silences, "delivery", now, d.ID)
	if err != nil {
		return false, err
	}
	if err := cancelDelivery(ctx, tx, d.ID, cause, now); err != nil {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE alert_incidents SET last_enqueued_at=NULL WHERE id=?`, d.IncidentID)
	return true, err
}
