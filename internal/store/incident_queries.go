package store

import (
	"context"
	"errors"
)

type IncidentFilter struct {
	Before int64
	Limit  int
	State  string
	NodeID string
}

func (s *Store) ListIncidents(ctx context.Context, f IncidentFilter) ([]Incident, error) {
	if f.Limit < 1 || f.Limit > 100 || f.Before < 0 || len(f.NodeID) > 128 {
		return nil, errors.New("invalid incident filter")
	}
	if f.State != "" && f.State != "pending" && f.State != "firing" && f.State != "resolved" {
		return nil, errors.New("invalid incident state")
	}
	query := "SELECT " + incidentColumns + " FROM alert_incidents WHERE 1=1"
	args := []any{}
	if f.Before > 0 {
		query += " AND seq < ?"
		args = append(args, f.Before)
	}
	if f.State != "" {
		query += " AND state = ?"
		args = append(args, f.State)
	}
	if f.NodeID != "" {
		query += " AND node_id = ?"
		args = append(args, f.NodeID)
	}
	query += " ORDER BY seq DESC LIMIT ?"
	args = append(args, f.Limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Incident, 0)
	for rows.Next() {
		item, err := scanIncident(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) ListIncidentDeliveries(ctx context.Context, incidentID string, before int64, limit int) ([]NotificationDelivery, error) {
	if incidentID == "" || len(incidentID) > 128 || before < 0 || limit < 1 || limit > 100 {
		return nil, errors.New("invalid delivery filter")
	}
	query := "SELECT " + deliveryColumns + " FROM notification_deliveries WHERE incident_id=?"
	args := []any{incidentID}
	if before > 0 {
		query += " AND seq < ?"
		args = append(args, before)
	}
	query += " ORDER BY seq DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]NotificationDelivery, 0)
	for rows.Next() {
		item, err := scanDelivery(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
