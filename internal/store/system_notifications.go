package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type SystemNotification struct {
	ID              string     `json:"id"`
	RunID           string     `json:"backup_run_id"`
	ChannelID       string     `json:"channel_id"`
	ChannelName     string     `json:"channel_name"`
	Provider        string     `json:"provider"`
	ChannelRevision int64      `json:"-"`
	ErrorCode       string     `json:"error_code"`
	OccurredAt      time.Time  `json:"occurred_at"`
	Status          string     `json:"status"`
	AttemptCount    int        `json:"attempt_count"`
	AvailableAt     time.Time  `json:"available_at"`
	ErrorClass      string     `json:"error_class,omitempty"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	LeaseToken      string     `json:"-"`
	LeaseUntil      time.Time  `json:"-"`
	Authorized      bool       `json:"-"`
}

const systemNotificationColumns = `id,backup_run_id,channel_id,channel_name,provider,channel_revision,error_code,occurred_at,status,attempt_count,available_at_ms,error_class,finished_at,COALESCE(lease_token,''),COALESCE(lease_until_ms,0),send_authorized`

func scanSystemNotification(row rowScanner) (SystemNotification, error) {
	var n SystemNotification
	var occurred string
	var finished sql.NullString
	var available, until int64
	err := row.Scan(&n.ID, &n.RunID, &n.ChannelID, &n.ChannelName, &n.Provider, &n.ChannelRevision, &n.ErrorCode, &occurred, &n.Status, &n.AttemptCount, &available, &n.ErrorClass, &finished, &n.LeaseToken, &until, &n.Authorized)
	if err != nil {
		return n, err
	}
	n.OccurredAt, err = time.Parse(time.RFC3339Nano, occurred)
	if err != nil {
		return n, err
	}
	n.AvailableAt = time.UnixMilli(available).UTC()
	n.LeaseUntil = time.UnixMilli(until).UTC()
	if finished.Valid {
		at, e := time.Parse(time.RFC3339Nano, finished.String)
		if e != nil {
			return n, e
		}
		n.FinishedAt = &at
	}
	return n, nil
}
func (s *Store) SystemNotifications(ctx context.Context) ([]SystemNotification, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+systemNotificationColumns+` FROM system_notifications ORDER BY seq DESC LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []SystemNotification{}
	for rows.Next() {
		n, e := scanSystemNotification(rows)
		if e != nil {
			return nil, e
		}
		result = append(result, n)
	}
	return result, rows.Err()
}
func (s *Store) ClaimSystemNotification(ctx context.Context, now time.Time) (*SystemNotification, error) {
	// Even future pending rows can require channel-revision cancellation, so
	// conservatively enter the original transaction for any pending system job.
	// Empty/terminal-only queues and unexpired leases need no writer reservation.
	var work bool
	if err := s.db.QueryRowContext(ctx, `SELECT
		EXISTS(SELECT 1 FROM system_notifications WHERE status='pending')
		OR EXISTS(SELECT 1 FROM system_notifications WHERE status='inflight' AND lease_until_ms<=?)`, now.UnixMilli()).Scan(&work); err != nil {
		return nil, err
	}
	if !work {
		return nil, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Expired authorized I/O is unknown, never automatically retried. An unsent
	// reservation can safely return to pending without consuming its budget.
	if _, err = tx.ExecContext(ctx, `UPDATE system_notifications SET status=CASE WHEN send_authorized=1 THEN 'unknown' ELSE 'pending' END,finished_at=CASE WHEN send_authorized=1 THEN ? ELSE NULL END,error_class=CASE WHEN send_authorized=1 THEN 'send_result_unknown' ELSE '' END,attempt_count=attempt_count-CASE WHEN send_authorized=0 THEN 1 ELSE 0 END,lease_token=NULL,lease_until_ms=NULL WHERE status='inflight' AND lease_until_ms<=?`, formatTime(now), now.UnixMilli()); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE system_notifications SET status='canceled',finished_at=?,error_class='channel_configuration_changed' WHERE status='pending' AND NOT EXISTS(SELECT 1 FROM notification_channels c WHERE c.id=system_notifications.channel_id AND c.enabled=1 AND COALESCE(NULLIF(c.provider,''),c.kind)=system_notifications.provider AND c.delivery_revision=system_notifications.channel_revision)`, formatTime(now)); err != nil {
		return nil, err
	}
	n, err := scanSystemNotification(tx.QueryRowContext(ctx, `SELECT `+systemNotificationColumns+` FROM system_notifications WHERE status='pending' AND attempt_count<5 AND available_at_ms<=? AND NOT EXISTS(SELECT 1 FROM notification_channels c WHERE c.id=system_notifications.channel_id AND c.delivery_paused_until_ms>?) ORDER BY seq LIMIT 1`, now.UnixMilli(), now.UnixMilli()))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, tx.Commit()
	}
	if err != nil {
		return nil, err
	}
	n.LeaseToken = randomID()
	n.LeaseUntil = now.Add(time.Minute)
	n.AttemptCount++
	n.Status = "inflight"
	if _, err = tx.ExecContext(ctx, `UPDATE system_notifications SET status='inflight',attempt_count=attempt_count+1,lease_token=?,lease_until_ms=?,send_authorized=0 WHERE id=?`, n.LeaseToken, n.LeaseUntil.UnixMilli(), n.ID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &n, nil
}
func (s *Store) AuthorizeSystemNotification(ctx context.Context, id, token string, revision int64, now time.Time) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE system_notifications SET id=id WHERE id=?`, id); err != nil {
		return false, err
	}
	n, err := scanSystemNotification(tx.QueryRowContext(ctx, `SELECT `+systemNotificationColumns+` FROM system_notifications WHERE id=?`, id))
	if err != nil {
		return false, err
	}
	if n.Status != "inflight" || n.LeaseToken != token || !now.Before(n.LeaseUntil) || n.Authorized {
		return false, ErrDeliveryLeaseLost
	}
	var enabled bool
	var provider string
	var current int64
	var paused sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT enabled,COALESCE(NULLIF(provider,''),kind),delivery_revision,delivery_paused_until_ms FROM notification_channels WHERE id=?`, n.ChannelID).Scan(&enabled, &provider, &current, &paused)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if err != nil || !enabled || current != n.ChannelRevision || current != revision || provider != n.Provider {
		_, err = tx.ExecContext(ctx, `UPDATE system_notifications SET status='canceled',finished_at=?,error_class='channel_configuration_changed',lease_token=NULL,lease_until_ms=NULL WHERE id=?`, formatTime(now), id)
		if err != nil {
			return false, err
		}
		return false, tx.Commit()
	}
	if paused.Valid && paused.Int64 > now.UnixMilli() {
		_, err = tx.ExecContext(ctx, `UPDATE system_notifications SET status='pending',attempt_count=attempt_count-1,available_at_ms=MAX(available_at_ms,?),lease_token=NULL,lease_until_ms=NULL WHERE id=?`, paused.Int64, id)
		if err != nil {
			return false, err
		}
		return false, tx.Commit()
	}
	if _, err = tx.ExecContext(ctx, `UPDATE system_notifications SET send_authorized=1 WHERE id=?`, id); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
func (s *Store) CompleteSystemNotification(ctx context.Context, id, token string, now time.Time, outcome DeliveryOutcome) error {
	if now.IsZero() || (outcome.Delivered && outcome.Ambiguous) || (!outcome.Delivered && !deliveryErrorClass.MatchString(outcome.ErrorClass)) {
		return errors.New("invalid_system_notification_outcome")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE system_notifications SET id=id WHERE id=?`, id); err != nil {
		return err
	}
	n, err := scanSystemNotification(tx.QueryRowContext(ctx, `SELECT `+systemNotificationColumns+` FROM system_notifications WHERE id=?`, id))
	if err != nil {
		return err
	}
	if n.Status != "inflight" || n.LeaseToken != token || !n.Authorized || outcome.ChannelRevision != n.ChannelRevision || !now.Before(n.LeaseUntil) {
		return ErrDeliveryLeaseLost
	}
	status := "delivered"
	var finish any = formatTime(now)
	available := n.AvailableAt
	if !outcome.Delivered {
		if outcome.Ambiguous {
			status = "unknown"
		} else if outcome.Permanent || n.AttemptCount >= 5 {
			status = "failed"
		} else {
			status = "pending"
			finish = nil
			if !outcome.RetryAt.After(now) {
				return errors.New("invalid_system_retry_time")
			}
			available = ceilMilliseconds(outcome.RetryAt)
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE system_notifications SET status=?,finished_at=?,available_at_ms=?,error_class=?,lease_token=NULL,lease_until_ms=NULL WHERE id=?`, status, finish, available.UnixMilli(), outcome.ErrorClass, id); err != nil {
		return err
	}
	if err = recordChannelPause(ctx, tx, n.ChannelID, outcome, now); err != nil {
		return err
	}
	return tx.Commit()
}
