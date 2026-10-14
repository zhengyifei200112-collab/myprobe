package store

import (
	"context"
	"database/sql"
	"time"
)

// QueueCounts describes stored active logical rows, not messages accepted by a
// provider or rows currently eligible after all final permission checks.
type QueueCounts struct {
	Pending                  int64  `json:"pending"`
	PendingDueByTime         int64  `json:"pending_due_by_time"`
	PendingFuture            int64  `json:"pending_future"`
	Inflight                 int64  `json:"inflight"`
	ExpiredLeases            int64  `json:"expired_leases"`
	AggregateMembers         int64  `json:"aggregate_members,omitempty"`
	Collecting               int64  `json:"collecting,omitempty"`
	OldestPendingAvailableMS *int64 `json:"oldest_pending_available_ms,omitempty"`
}

type NotificationQueueDiagnostics struct {
	Status     string       `json:"status"`
	ErrorCode  string       `json:"error_code,omitempty"`
	ObservedAt time.Time    `json:"observed_at"`
	Scope      string       `json:"scope"`
	Deliveries *QueueCounts `json:"deliveries,omitempty"`
	Aggregates *QueueCounts `json:"aggregates,omitempty"`
	System     *QueueCounts `json:"system,omitempty"`
}

// NotificationQueueDiagnostics reads safe counters in one read-only snapshot.
// It does not claim jobs, expire leases, inspect payloads or alter channels.
func (s *Store) NotificationQueueDiagnostics(ctx context.Context, now time.Time) NotificationQueueDiagnostics {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	result := NotificationQueueDiagnostics{Status: "unavailable", ErrorCode: "queue_query_failed", ObservedAt: now.UTC(), Scope: "stored_active_logical_rows"}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result
	}
	defer tx.Rollback()
	deliveries, groups := QueueCounts{}, QueueCounts{}
	var oldest sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(available_at_ms<=?),0), COALESCE(SUM(aggregate_id IS NOT NULL),0), MIN(available_at_ms)
 FROM notification_deliveries WHERE status='pending'`, now.UnixMilli()).Scan(&deliveries.Pending, &deliveries.PendingDueByTime, &deliveries.AggregateMembers, &oldest)
	if err != nil {
		return result
	}
	deliveries.PendingFuture = deliveries.Pending - deliveries.PendingDueByTime
	if oldest.Valid {
		value := oldest.Int64
		deliveries.OldestPendingAvailableMS = &value
	}
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(lease_until_ms<=?),0), COALESCE(SUM(aggregate_id IS NOT NULL),0)
 FROM notification_deliveries WHERE status='inflight'`, now.UnixMilli()).Scan(&deliveries.Inflight, &deliveries.ExpiredLeases, &groups.AggregateMembers)
	if err != nil {
		return result
	}
	deliveries.AggregateMembers += groups.AggregateMembers
	groups.AggregateMembers = 0
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(available_at_ms<=? AND ends_at_ms<=?),0),COALESCE(SUM(sealed=0 AND ends_at_ms>?),0),MIN(available_at_ms)
 FROM notification_aggregates WHERE status='pending'`, now.UnixMilli(), now.UnixMilli(), now.UnixMilli()).Scan(&groups.Pending, &groups.PendingDueByTime, &groups.Collecting, &oldest)
	if err != nil {
		return result
	}
	groups.PendingFuture = groups.Pending - groups.PendingDueByTime
	if oldest.Valid {
		value := oldest.Int64
		groups.OldestPendingAvailableMS = &value
	}
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(lease_until_ms<=?),0) FROM notification_aggregates WHERE status='inflight'`, now.UnixMilli()).Scan(&groups.Inflight, &groups.ExpiredLeases)
	if err != nil {
		return result
	}
	system := QueueCounts{}
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(available_at_ms<=?),0),MIN(available_at_ms) FROM system_notifications WHERE status='pending'`, now.UnixMilli()).Scan(&system.Pending, &system.PendingDueByTime, &oldest)
	if err != nil {
		return result
	}
	system.PendingFuture = system.Pending - system.PendingDueByTime
	if oldest.Valid {
		value := oldest.Int64
		system.OldestPendingAvailableMS = &value
	}
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(lease_until_ms<=?),0) FROM system_notifications WHERE status='inflight'`, now.UnixMilli()).Scan(&system.Inflight, &system.ExpiredLeases)
	if err != nil || tx.Commit() != nil {
		return result
	}
	result.System = &system
	result.Status, result.ErrorCode, result.Deliveries, result.Aggregates = "available", "", &deliveries, &groups
	return result
}
