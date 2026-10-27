package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"
)

type DeliveryAttempt struct {
	Number      int        `json:"number"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	Outcome     string     `json:"outcome"`
	ErrorClass  string     `json:"error_class,omitempty"`
}

// Compatibility helper for internal readers. Administrative clients use the
// paginated view because known-unsent reservations can outnumber retry budgets.
func (s *Store) ListDeliveryAttempts(ctx context.Context, deliveryID string) ([]DeliveryAttempt, error) {
	page, err := s.ListDeliveryAttemptPage(ctx, deliveryID, 0, 100)
	return page.Attempts, err
}

type DeliveryAttemptPage struct {
	Attempts   []DeliveryAttempt `json:"attempts"`
	NextBefore int64             `json:"next_before"`
}

func (s *Store) ListDeliveryAttemptPage(ctx context.Context, deliveryID string, before int64, limit int) (DeliveryAttemptPage, error) {
	page := DeliveryAttemptPage{Attempts: []DeliveryAttempt{}}
	if before < 0 || limit < 1 || limit > 100 {
		return page, errors.New("invalid attempt pagination")
	}
	if _, err := s.Delivery(ctx, deliveryID); err != nil {
		return page, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT attempt_number,started_at,completed_at,outcome,error_class FROM delivery_attempts WHERE delivery_id=? AND (?=0 OR attempt_number<?) ORDER BY attempt_number DESC LIMIT ?", deliveryID, before, before, limit+1)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	items := make([]DeliveryAttempt, 0)
	for rows.Next() {
		var item DeliveryAttempt
		var started string
		var completed sql.NullString
		if err := rows.Scan(&item.Number, &started, &completed, &item.Outcome, &item.ErrorClass); err != nil {
			return page, err
		}
		if item.StartedAt, err = parseTime(started); err != nil {
			return page, err
		}
		if completed.Valid {
			at, err := parseTime(completed.String)
			if err != nil {
				return page, err
			}
			item.CompletedAt = &at
		}
		items = append(items, item)
	}
	if len(items) > limit {
		items = items[:limit]
		page.NextBefore = int64(items[len(items)-1].Number)
	}
	slices.Reverse(items)
	page.Attempts = items
	return page, rows.Err()
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
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, query, args...)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := incidentAcknowledgementSummaries(ctx, tx, items); err != nil {
		return nil, err
	}
	return items, tx.Commit()
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
