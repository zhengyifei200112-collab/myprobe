package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrChannelRateLimited = errors.New("notification channel is waiting before retry")
var ErrChannelConfigurationChanged = errors.New("notification channel configuration changed; reload before sending")

func channelPauseTime(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	at := time.UnixMilli(value.Int64).UTC()
	return &at
}

func channelPaused(ctx context.Context, tx *sql.Tx, id string, at time.Time) (bool, error) {
	var paused bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM notification_channels WHERE id=? AND delivery_paused_until_ms>?)`, id, at.UnixMilli()).Scan(&paused)
	return paused, err
}

// Only a validated current sender may extend this destination revision's wait.
// Success never clears a wait reported by another concurrent sender.
func recordChannelPause(ctx context.Context, tx *sql.Tx, id string, outcome DeliveryOutcome, at time.Time) error {
	if outcome.ChannelRetryAt.IsZero() {
		return nil
	}
	if outcome.Delivered || outcome.Permanent || outcome.Ambiguous || outcome.ChannelRevision < 1 || !outcome.ChannelRetryAt.After(at) {
		return errors.New("invalid notification channel retry deadline")
	}
	_, err := tx.ExecContext(ctx, `UPDATE notification_channels SET delivery_paused_until_ms=MAX(COALESCE(delivery_paused_until_ms,?),?) WHERE id=? AND delivery_revision=?`, ceilMilliseconds(outcome.ChannelRetryAt).UnixMilli(), ceilMilliseconds(outcome.ChannelRetryAt).UnixMilli(), id, outcome.ChannelRevision)
	return err
}

// Before I/O, retain a canceled reservation record but refund its retry budget.
// The monotonic history sequence is never decremented or reused.
func deferUnsentReservation(ctx context.Context, tx *sql.Tx, d NotificationDelivery, at time.Time, class string) error {
	result, err := tx.ExecContext(ctx, `UPDATE delivery_attempts SET outcome='canceled',completed_at=?,error_class=? WHERE delivery_id=? AND attempt_number=? AND lease_token=? AND outcome='started'`, formatTime(at), class, d.ID, d.AttemptSequence, d.LeaseToken)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil {
		return err
	} else if count != 1 {
		return ErrDeliveryLeaseLost
	}
	result, err = tx.ExecContext(ctx, `UPDATE notification_deliveries SET status='pending',attempt_count=attempt_count-1,lease_token=NULL,lease_until_ms=NULL,updated_at=?,error_class=? WHERE id=? AND status='inflight' AND send_authorized=0 AND lease_token=? AND attempt_sequence=? AND attempt_count>0`, formatTime(at), class, d.ID, d.LeaseToken, d.AttemptSequence)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil {
		return err
	} else if count != 1 {
		return ErrDeliveryLeaseLost
	}
	return nil
}

func (s *Store) CheckNotificationChannelSend(ctx context.Context, id string, revision int64, at time.Time) error {
	c, err := s.NotificationChannel(ctx, id)
	if err != nil {
		return err
	}
	if c.DeliveryRevision != revision {
		return ErrChannelConfigurationChanged
	}
	if c.DeliveryPausedUntil != nil && at.Before(*c.DeliveryPausedUntil) {
		return ErrChannelRateLimited
	}
	return nil
}

// Test sends are synchronous but share the same persisted provider gate. Their
// late result cannot overwrite test state or pause a replacement configuration.
func (s *Store) CompleteNotificationChannelTest(ctx context.Context, id string, outcome DeliveryOutcome, at time.Time) error {
	if at.IsZero() || outcome.ChannelRevision < 1 || (outcome.Delivered && outcome.Ambiguous) || (!outcome.Delivered && !deliveryErrorClass.MatchString(outcome.ErrorClass)) {
		return errors.New("invalid notification test outcome")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	status := "failed"
	if outcome.Delivered {
		status = "success"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE notification_channels SET last_test_status=?,last_test_error=?,last_test_at=?,updated_at=? WHERE id=? AND delivery_revision=?`, status, outcome.ErrorClass, formatTime(at), formatTime(at), id, outcome.ChannelRevision); err != nil {
		return err
	}
	if err := recordChannelPause(ctx, tx, id, outcome, at); err != nil {
		return err
	}
	return tx.Commit()
}
