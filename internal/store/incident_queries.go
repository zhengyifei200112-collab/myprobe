package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type DeliveryAttempt struct {
	Number      int        `json:"number"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	Outcome     string     `json:"outcome"`
	ErrorClass  string     `json:"error_class,omitempty"`
}

// Attempts are bounded by the schema's five-attempt budget. Lease tokens and
// rendered notification payloads are deliberately excluded from this view.
func (s *Store) ListDeliveryAttempts(ctx context.Context, deliveryID string) ([]DeliveryAttempt, error) {
	if _, err := s.Delivery(ctx, deliveryID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT attempt_number,started_at,completed_at,outcome,error_class FROM delivery_attempts WHERE delivery_id=? ORDER BY attempt_number LIMIT 5", deliveryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]DeliveryAttempt, 0)
	for rows.Next() {
		var item DeliveryAttempt
		var started string
		var completed sql.NullString
		if err := rows.Scan(&item.Number, &started, &completed, &item.Outcome, &item.ErrorClass); err != nil {
			return nil, err
		}
		if item.StartedAt, err = parseTime(started); err != nil {
			return nil, err
		}
		if completed.Valid {
			at, err := parseTime(completed.String)
			if err != nil {
				return nil, err
			}
			item.CompletedAt = &at
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

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
