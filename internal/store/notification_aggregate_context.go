package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

var ErrInvalidAggregateQuery = errors.New("invalid aggregate query")

type AggregateTotals struct {
	Pending   int `json:"pending"`
	Inflight  int `json:"inflight"`
	Delivered int `json:"delivered"`
	Failed    int `json:"failed"`
	Canceled  int `json:"canceled"`
}
type AggregateMemberView struct {
	NotificationDelivery
	NodeName         string `json:"node_name"`
	IncidentState    string `json:"incident_state"`
	ObservationStale bool   `json:"observation_stale"`
}
type AggregateAttemptMemberView struct {
	DeliveryID      string `json:"delivery_id"`
	IncidentID      string `json:"incident_id"`
	NodeName        string `json:"node_name"`
	DeliveryAttempt int    `json:"delivery_attempt"`
	Included        bool   `json:"included"`
}
type AggregateAttemptView struct {
	Number      int                          `json:"number"`
	StartedAt   time.Time                    `json:"started_at"`
	CompletedAt *time.Time                   `json:"completed_at,omitempty"`
	Outcome     string                       `json:"outcome"`
	ErrorClass  string                       `json:"error_class,omitempty"`
	Content     *AggregateNotification       `json:"content"`
	Members     []AggregateAttemptMemberView `json:"members"`
}
type AggregateContext struct {
	Aggregate  NotificationAggregate  `json:"aggregate"`
	CheckedAt  time.Time              `json:"checked_at"`
	Totals     AggregateTotals        `json:"totals"`
	Members    []AggregateMemberView  `json:"members"`
	Attempts   []AggregateAttemptView `json:"attempts"`
	NextBefore int                    `json:"next_before"`
}

type AggregateSummary struct {
	NotificationAggregate
	ChannelName string          `json:"channel_name"`
	Totals      AggregateTotals `json:"totals"`
}
type AggregatePage struct {
	Aggregates []AggregateSummary `json:"aggregates"`
	NextBefore int64              `json:"next_before"`
	CheckedAt  time.Time          `json:"checked_at"`
}

// The sequence cursor is independent of window/attempt times. A page reads
// current outcomes in one snapshot, including channels that have been removed.
func (s *Store) ListNotificationAggregates(ctx context.Context, before int64, limit int) (AggregatePage, error) {
	page := AggregatePage{Aggregates: []AggregateSummary{}}
	if before < 0 || limit < 1 || limit > 100 {
		return page, ErrInvalidAggregateQuery
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	page.CheckedAt = time.Now().UTC()
	rows, err := tx.QueryContext(ctx, `SELECT `+aggregateColumns+` FROM notification_aggregates WHERE (?=0 OR seq<?) ORDER BY seq DESC LIMIT ?`, before, before, limit+1)
	if err != nil {
		return page, err
	}
	for rows.Next() {
		g, err := scanAggregate(rows)
		if err != nil {
			rows.Close()
			return page, err
		}
		page.Aggregates = append(page.Aggregates, AggregateSummary{NotificationAggregate: g})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return page, err
	}
	if len(page.Aggregates) > limit {
		page.Aggregates = page.Aggregates[:limit]
		page.NextBefore = page.Aggregates[limit-1].Seq
	}
	for n := range page.Aggregates {
		g := &page.Aggregates[n]
		if err := tx.QueryRowContext(ctx, `SELECT coalesce((SELECT channel_name FROM notification_deliveries WHERE aggregate_id=? ORDER BY seq LIMIT 1),''),count(*) FILTER(WHERE status='pending'),count(*) FILTER(WHERE status='inflight'),count(*) FILTER(WHERE status='delivered'),count(*) FILTER(WHERE status='failed'),count(*) FILTER(WHERE status='canceled') FROM notification_deliveries WHERE aggregate_id=?`, g.ID, g.ID).Scan(&g.ChannelName, &g.Totals.Pending, &g.Totals.Inflight, &g.Totals.Delivered, &g.Totals.Failed, &g.Totals.Canceled); err != nil {
			return page, err
		}
	}
	return page, tx.Commit()
}

// Metadata, current logical outcomes and exact historical membership share one
// read snapshot. Leases, keys, channel credentials and raw payloads stay private.
func (s *Store) AggregateContext(ctx context.Context, id string, before, limit int) (AggregateContext, error) {
	page := AggregateContext{Members: []AggregateMemberView{}, Attempts: []AggregateAttemptView{}}
	if !portableID.MatchString(id) || before < 0 || limit < 1 || limit > 100 {
		return page, ErrInvalidAggregateQuery
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	page.Aggregate, err = scanAggregate(tx.QueryRowContext(ctx, `SELECT `+aggregateColumns+` FROM notification_aggregates WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return page, ErrNotFound
	}
	if err != nil {
		return page, err
	}
	page.CheckedAt = time.Now().UTC()
	jobs, err := aggregateDeliveries(ctx, tx, id)
	if err != nil {
		return page, err
	}
	for _, d := range jobs {
		item := AggregateMemberView{NotificationDelivery: d}
		if err := tx.QueryRowContext(ctx, `SELECT node_name,state,observation_stale FROM alert_incidents WHERE id=?`, d.IncidentID).Scan(&item.NodeName, &item.IncidentState, &item.ObservationStale); err != nil {
			return page, err
		}
		page.Members = append(page.Members, item)
		switch d.Status {
		case "pending":
			page.Totals.Pending++
		case "inflight":
			page.Totals.Inflight++
		case "delivered":
			page.Totals.Delivered++
		case "failed":
			page.Totals.Failed++
		case "canceled":
			page.Totals.Canceled++
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT attempt_number,started_at,completed_at,outcome,error_class,payload_json FROM notification_aggregate_attempts WHERE aggregate_id=? AND (?=0 OR attempt_number<?) ORDER BY attempt_number DESC LIMIT ?`, id, before, before, limit+1)
	if err != nil {
		return page, err
	}
	for rows.Next() {
		var a AggregateAttemptView
		var started, raw string
		var completed sql.NullString
		if err := rows.Scan(&a.Number, &started, &completed, &a.Outcome, &a.ErrorClass, &raw); err != nil {
			rows.Close()
			return page, err
		}
		if a.StartedAt, err = parseTime(started); err != nil {
			rows.Close()
			return page, err
		}
		if completed.Valid {
			at, err := parseTime(completed.String)
			if err != nil {
				rows.Close()
				return page, err
			}
			a.CompletedAt = &at
		}
		if raw != "{}" {
			var content AggregateNotification
			if err := json.Unmarshal([]byte(raw), &content); err != nil {
				rows.Close()
				return page, err
			}
			a.Content = &content
		}
		a.Members = []AggregateAttemptMemberView{}
		page.Attempts = append(page.Attempts, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return page, err
	}
	if len(page.Attempts) > limit {
		page.Attempts = page.Attempts[:limit]
		page.NextBefore = page.Attempts[limit-1].Number
	}
	for n := range page.Attempts {
		a := &page.Attempts[n]
		rows, err := tx.QueryContext(ctx, `SELECT m.delivery_id,d.incident_id,i.node_name,m.delivery_attempt,m.included FROM notification_aggregate_attempt_members m JOIN notification_deliveries d ON d.id=m.delivery_id JOIN alert_incidents i ON i.id=d.incident_id WHERE m.aggregate_id=? AND m.aggregate_attempt=? ORDER BY d.seq LIMIT ?`, id, a.Number, AggregateMemberLimit+1)
		if err != nil {
			return page, err
		}
		for rows.Next() {
			var ref AggregateAttemptMemberView
			if err := rows.Scan(&ref.DeliveryID, &ref.IncidentID, &ref.NodeName, &ref.DeliveryAttempt, &ref.Included); err != nil {
				rows.Close()
				return page, err
			}
			a.Members = append(a.Members, ref)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return page, err
		}
		if len(a.Members) > AggregateMemberLimit {
			return page, errors.New("aggregate membership overflow")
		}
	}
	return page, tx.Commit()
}
