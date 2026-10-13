package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/notificationjson"
)

func beginAggregateWrite(ctx context.Context, s *Store) (*sql.Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE notification_aggregates SET updated_at=updated_at WHERE seq=-1`); err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}
func aggregateAttemptMembers(ctx context.Context, tx *sql.Tx, g NotificationAggregate, includedOnly bool) (map[string]int, error) {
	query := `SELECT delivery_id,delivery_attempt FROM notification_aggregate_attempt_members WHERE aggregate_id=? AND aggregate_attempt=?`
	if includedOnly {
		query += ` AND included=1`
	}
	rows, err := tx.QueryContext(ctx, query, g.ID, g.AttemptCount)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := map[string]int{}
	for rows.Next() {
		var id string
		var number int
		if err := rows.Scan(&id, &number); err != nil {
			return nil, err
		}
		items[id] = number
	}
	return items, rows.Err()
}
func recoverAggregateLeases(ctx context.Context, tx *sql.Tx, at time.Time) error {
	rows, err := tx.QueryContext(ctx, `SELECT `+aggregateColumns+` FROM notification_aggregates WHERE status='inflight' AND lease_until_ms<=? ORDER BY seq LIMIT 100`, at.UnixMilli())
	if err != nil {
		return err
	}
	groups := []NotificationAggregate{}
	for rows.Next() {
		g, err := scanAggregate(rows)
		if err != nil {
			rows.Close()
			return err
		}
		groups = append(groups, g)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, g := range groups {
		var phase string
		if err := tx.QueryRowContext(ctx, `SELECT outcome FROM notification_aggregate_attempts WHERE aggregate_id=? AND attempt_number=?`, g.ID, g.AttemptCount).Scan(&phase); err != nil {
			return err
		}
		outcome := "unknown"
		if phase == "prepared" {
			outcome = "canceled"
		}
		if _, err := tx.ExecContext(ctx, `UPDATE notification_aggregate_attempts SET outcome=?,completed_at=?,error_class='lease_expired' WHERE aggregate_id=? AND attempt_number=? AND outcome IN ('prepared','started')`, outcome, formatTime(at), g.ID, g.AttemptCount); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE delivery_attempts SET outcome=?,completed_at=?,error_class='lease_expired' WHERE lease_token=? AND outcome='started' AND delivery_id IN (SELECT id FROM notification_deliveries WHERE aggregate_id=?)`, outcome, formatTime(at), g.LeaseToken, g.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE alert_incidents SET last_enqueued_at=? WHERE id IN (SELECT incident_id FROM notification_deliveries WHERE aggregate_id=? AND status='inflight' AND lease_token=? AND attempt_count>=5)`, formatTime(at), g.ID, g.LeaseToken); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE notification_deliveries SET status=CASE WHEN attempt_count>=5 THEN 'failed' ELSE 'pending' END,finished_at=CASE WHEN attempt_count>=5 THEN ? ELSE NULL END,available_at_ms=?,lease_token=NULL,lease_until_ms=NULL,updated_at=?,error_class='lease_expired' WHERE aggregate_id=? AND status='inflight' AND lease_token=?`, formatTime(at), at.UnixMilli(), formatTime(at), g.ID, g.LeaseToken); err != nil {
			return err
		}
		if err := scheduleAggregate(ctx, tx, g.ID, at); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ClaimAggregate(ctx context.Context, at time.Time, lease time.Duration) (*NotificationAggregate, error) {
	if at.IsZero() || lease < 10*time.Second || lease > 5*time.Minute {
		return nil, errors.New("invalid aggregate lease")
	}
	at = at.UTC()
	tx, err := beginAggregateWrite(ctx, s)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := recoverAggregateLeases(ctx, tx, at); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+aggregateColumns+` FROM notification_aggregates WHERE status='pending' AND available_at_ms<=? AND NOT EXISTS(SELECT 1 FROM notification_channels c WHERE c.id=notification_aggregates.channel_id AND c.delivery_paused_until_ms>?) ORDER BY available_at_ms,seq LIMIT 100`, at.UnixMilli(), at.UnixMilli())
	if err != nil {
		return nil, err
	}
	groups := []NotificationAggregate{}
	for rows.Next() {
		g, err := scanAggregate(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		groups = append(groups, g)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, g := range groups {
		items, err := aggregateDeliveries(ctx, tx, g.ID)
		if err != nil {
			return nil, err
		}
		candidates := []NotificationDelivery{}
		for _, d := range items {
			if d.Status != "pending" || d.AvailableAt.After(at) {
				continue
			}
			valid, err := aggregateApplicable(ctx, tx, d, at)
			if err != nil {
				return nil, err
			}
			if !valid {
				if _, err := tx.ExecContext(ctx, `UPDATE notification_deliveries SET available_at_ms=? WHERE id=? AND status='pending'`, ceilMilliseconds(at.Add(15*time.Second)).UnixMilli(), d.ID); err != nil {
					return nil, err
				}
				continue
			}
			if d.AttemptCount >= 5 {
				if _, err := tx.ExecContext(ctx, `UPDATE alert_incidents SET last_enqueued_at=? WHERE id=?`, formatTime(at), d.IncidentID); err != nil {
					return nil, err
				}
				if _, err := tx.ExecContext(ctx, `UPDATE notification_deliveries SET status='failed',finished_at=?,updated_at=? WHERE id=?`, formatTime(at), formatTime(at), d.ID); err != nil {
					return nil, err
				}
				continue
			}
			candidates = append(candidates, d)
		}
		if len(candidates) == 0 {
			if err := scheduleAggregate(ctx, tx, g.ID, at); err != nil {
				return nil, err
			}
			continue
		}
		g.Status = "inflight"
		g.Sealed = true
		g.AttemptCount++
		g.LeaseToken = randomID()
		until := ceilMilliseconds(at.Add(lease))
		g.LeaseUntil = &until
		if _, err := tx.ExecContext(ctx, `UPDATE notification_aggregates SET status='inflight',sealed=1,attempt_count=?,lease_token=?,lease_until_ms=?,updated_at=? WHERE id=?`, g.AttemptCount, g.LeaseToken, until.UnixMilli(), formatTime(at), g.ID); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO notification_aggregate_attempts(aggregate_id,attempt_number,lease_token,started_at,outcome) VALUES(?,?,?,?,'prepared')`, g.ID, g.AttemptCount, g.LeaseToken, formatTime(at)); err != nil {
			return nil, err
		}
		for _, d := range candidates {
			if _, err := tx.ExecContext(ctx, `UPDATE notification_deliveries SET status='inflight',send_authorized=0,attempt_count=attempt_count+1,attempt_sequence=attempt_sequence+1,lease_token=?,lease_until_ms=?,updated_at=? WHERE id=?`, g.LeaseToken, until.UnixMilli(), formatTime(at), d.ID); err != nil {
				return nil, err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO delivery_attempts(delivery_id,attempt_number,lease_token,started_at,outcome) VALUES(?,?,?,?,'started')`, d.ID, d.AttemptSequence+1, g.LeaseToken, formatTime(at)); err != nil {
				return nil, err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO notification_aggregate_attempt_members(aggregate_id,aggregate_attempt,delivery_id,delivery_attempt) VALUES(?,?,?,?)`, g.ID, g.AttemptCount, d.ID, d.AttemptSequence+1); err != nil {
				return nil, err
			}
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &g, nil
	}
	return nil, tx.Commit()
}

type AggregateSend struct {
	Payload           json.RawMessage
	IdempotencyKey    string
	MaxMemberAttempt  int
	TemplateEventTime string              `json:"-"`
	Channel           NotificationChannel `json:"-"`
}

// PrepareAggregateSend is a one-shot final check. It freezes the exact body/key
// and included members before I/O. A later mutation cannot recall that send.
func (s *Store) PrepareAggregateSend(ctx context.Context, id, token string, at time.Time) (AggregateSend, error) {
	var result AggregateSend
	tx, err := beginAggregateWrite(ctx, s)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	g, err := scanAggregate(tx.QueryRowContext(ctx, `SELECT `+aggregateColumns+` FROM notification_aggregates WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return result, ErrDeliveryLeaseLost
	}
	if err != nil {
		return result, err
	}
	if g.Status != "inflight" || g.LeaseToken != token || g.LeaseUntil == nil || !at.Before(*g.LeaseUntil) {
		return result, ErrDeliveryLeaseLost
	}
	var phase string
	if err := tx.QueryRowContext(ctx, `SELECT outcome FROM notification_aggregate_attempts WHERE aggregate_id=? AND attempt_number=?`, id, g.AttemptCount).Scan(&phase); err != nil {
		return result, err
	}
	if phase != "prepared" {
		return result, ErrDeliveryLeaseLost
	}
	source, err := aggregateAttemptMembers(ctx, tx, g, false)
	if err != nil {
		return result, err
	}
	items, err := aggregateDeliveries(ctx, tx, id)
	if err != nil {
		return result, err
	}
	included := []NotificationDelivery{}
	for _, d := range items {
		number, owned := source[d.ID]
		if !owned {
			continue
		}
		valid := d.Status == "inflight" && d.LeaseToken == token && d.AttemptSequence == number
		if valid {
			valid, err = aggregateApplicable(ctx, tx, d, at)
			if err != nil {
				return result, err
			}
		}
		if valid {
			included = append(included, d)
			result.MaxMemberAttempt = max(result.MaxMemberAttempt, d.AttemptCount)
			continue
		}
		// Final check knows this member has not entered a transport payload.
		if _, err := tx.ExecContext(ctx, `UPDATE delivery_attempts SET outcome='canceled',completed_at=?,error_class=CASE WHEN (SELECT status FROM notification_deliveries WHERE id=?)='canceled' THEN (SELECT error_class FROM notification_deliveries WHERE id=?) ELSE 'observation_stale' END WHERE delivery_id=? AND attempt_number=? AND lease_token=? AND outcome IN ('started','unknown')`, formatTime(at), d.ID, d.ID, d.ID, number, token); err != nil {
			return result, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE notification_deliveries SET status='pending',available_at_ms=?,lease_token=NULL,lease_until_ms=NULL,updated_at=?,error_class='observation_stale' WHERE id=? AND status='inflight' AND lease_token=?`, ceilMilliseconds(at.Add(15*time.Second)).UnixMilli(), formatTime(at), d.ID, token); err != nil {
			return result, err
		}
	}
	if len(included) == 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE notification_aggregate_attempts SET outcome='canceled',completed_at=?,error_class='no_applicable_members' WHERE aggregate_id=? AND attempt_number=?`, formatTime(at), id, g.AttemptCount); err != nil {
			return result, err
		}
		if err := scheduleAggregate(ctx, tx, id, at); err != nil {
			return result, err
		}
		if err := tx.Commit(); err != nil {
			return result, err
		}
		return result, ErrDeliveryCanceled
	}
	paused, err := channelPaused(ctx, tx, g.ChannelID, at)
	if err != nil {
		return result, err
	}
	if paused {
		for _, d := range included {
			if err := deferUnsentReservation(ctx, tx, d, at, "channel_rate_limited"); err != nil {
				return result, err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE notification_aggregate_attempts SET outcome='canceled',error_class='channel_rate_limited',completed_at=? WHERE aggregate_id=? AND attempt_number=? AND outcome='prepared'`, formatTime(at), id, g.AttemptCount); err != nil {
			return result, err
		}
		if err := scheduleAggregate(ctx, tx, id, at); err != nil {
			return result, err
		}
		if err := tx.Commit(); err != nil {
			return result, err
		}
		return result, ErrDeliveryDeferred
	}
	result.Payload, result.IdempotencyKey, err = renderAggregate(g, included)
	if err != nil {
		class := "aggregate_payload_invalid"
		if errors.Is(err, notificationjson.ErrTooLarge) {
			class = "aggregate_payload_too_large"
		}
		if err := rejectAggregatePreparation(ctx, tx, g, included, at, class); err != nil {
			return result, err
		}
		if err := tx.Commit(); err != nil {
			return result, err
		}
		return AggregateSend{}, ErrAggregatePreparationFailed
	}
	// Freeze the destination configuration from this same writer snapshot;
	// later envelope/credential changes must not reuse the previous send key.
	result.TemplateEventTime = g.TemplateEventTime
	result.Channel.ID = g.ChannelID
	if err := tx.QueryRowContext(ctx, `SELECT name,COALESCE(NULLIF(provider,''),kind),config_encrypted,enabled,delivery_revision FROM notification_channels WHERE id=?`, g.ChannelID).Scan(&result.Channel.Name, &result.Channel.Kind, &result.Channel.ConfigEncrypted, &result.Channel.Enabled, &result.Channel.DeliveryRevision); err != nil {
		return result, err
	}
	sum := sha256.Sum256([]byte(result.IdempotencyKey + ":" + result.Channel.ConfigEncrypted))
	result.IdempotencyKey = "aggregate:" + g.ID + ":" + hex.EncodeToString(sum[:])
	for _, d := range included {
		if _, err := tx.ExecContext(ctx, `UPDATE notification_deliveries SET send_authorized=1 WHERE id=? AND lease_token=?`, d.ID, token); err != nil {
			return result, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE notification_aggregate_attempt_members SET included=1 WHERE aggregate_id=? AND aggregate_attempt=? AND delivery_id=?`, id, g.AttemptCount, d.ID); err != nil {
			return result, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE notification_aggregate_attempts SET outcome='started',payload_json=?,idempotency_key=? WHERE aggregate_id=? AND attempt_number=?`, string(result.Payload), result.IdempotencyKey, id, g.AttemptCount); err != nil {
		return result, err
	}
	return result, tx.Commit()
}

// Local rendering failure is known unsent. Complete the prepared owner under
// the same writer without authorizing members or inventing a provider receipt.
func rejectAggregatePreparation(ctx context.Context, tx *sql.Tx, g NotificationAggregate, members []NotificationDelivery, at time.Time, class string) error {
	for _, d := range members {
		result, err := tx.ExecContext(ctx, `UPDATE notification_deliveries SET status='failed',error_class=?,finished_at=?,updated_at=?,lease_token=NULL,lease_until_ms=NULL WHERE id=? AND status='inflight' AND lease_token=? AND attempt_count=?`, class, formatTime(at), formatTime(at), d.ID, g.LeaseToken, d.AttemptCount)
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil {
			return err
		} else if n != 1 {
			return ErrDeliveryLeaseLost
		}
		if _, err := tx.ExecContext(ctx, `UPDATE delivery_attempts SET outcome='failed',error_class=?,completed_at=? WHERE delivery_id=? AND attempt_number=? AND lease_token=? AND outcome='started'`, class, formatTime(at), d.ID, d.AttemptSequence, g.LeaseToken); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE alert_incidents SET last_enqueued_at=? WHERE id=?`, formatTime(at), d.IncidentID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE notification_aggregate_attempts SET outcome='failed',error_class=?,completed_at=? WHERE aggregate_id=? AND attempt_number=? AND lease_token=? AND outcome='prepared'`, class, formatTime(at), g.ID, g.AttemptCount, g.LeaseToken); err != nil {
		return err
	}
	return scheduleAggregate(ctx, tx, g.ID, at)
}

func (s *Store) CompleteAggregate(ctx context.Context, id, token string, at time.Time, outcome DeliveryOutcome) error {
	if (outcome.Delivered && outcome.Ambiguous) || at.IsZero() || (!outcome.Delivered && !deliveryErrorClass.MatchString(outcome.ErrorClass)) {
		return errors.New("invalid aggregate outcome")
	}
	tx, err := beginAggregateWrite(ctx, s)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	g, err := scanAggregate(tx.QueryRowContext(ctx, `SELECT `+aggregateColumns+` FROM notification_aggregates WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrDeliveryLeaseLost
	}
	if err != nil {
		return err
	}
	var number int
	var previous, previousError, started string
	if err := tx.QueryRowContext(ctx, `SELECT attempt_number,outcome,error_class,started_at FROM notification_aggregate_attempts WHERE aggregate_id=? AND lease_token=?`, id, token).Scan(&number, &previous, &previousError, &started); errors.Is(err, sql.ErrNoRows) {
		return ErrDeliveryLeaseLost
	} else if err != nil {
		return err
	}
	status, errorClass := "failed", outcome.ErrorClass
	if outcome.Delivered {
		status, errorClass = "delivered", ""
	} else if outcome.Ambiguous {
		status = "unknown"
	}
	if previous == status && previousError == errorClass {
		return tx.Commit()
	}
	startedAt, err := parseTime(started)
	if err != nil {
		return err
	}
	if at.Before(startedAt) || (previous != "started" && !(previous == "unknown" && previousError == "lease_expired")) {
		return ErrDeliveryLeaseLost
	}
	parentFenced := number == g.AttemptCount && g.Status == "inflight" && g.LeaseToken == token
	attemptGroup := g
	attemptGroup.AttemptCount = number
	source, err := aggregateAttemptMembers(ctx, tx, attemptGroup, true)
	if err != nil {
		return err
	}
	items, err := aggregateDeliveries(ctx, tx, id)
	if err != nil {
		return err
	}
	for _, d := range items {
		number, exists := source[d.ID]
		if !exists {
			continue
		}
		if parentFenced {
			if number != d.AttemptSequence {
				return ErrDeliveryLeaseLost
			}
			if err := completeDeliveryWithFence(ctx, tx, d, token, at, outcome, true); err != nil {
				return err
			}
		} else if err := recordAggregateLateMember(ctx, tx, d, number, token, at, outcome); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE notification_aggregate_attempts SET outcome=?,error_class=?,completed_at=? WHERE aggregate_id=? AND attempt_number=?`, status, errorClass, formatTime(at), id, number); err != nil {
		return err
	}
	if parentFenced {
		var currentMember bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM notification_aggregate_attempt_members m JOIN notification_deliveries d ON d.id=m.delivery_id WHERE m.aggregate_id=? AND m.aggregate_attempt=? AND m.included=1 AND d.status<>'canceled')`, id, number).Scan(&currentMember); err != nil {
			return err
		}
		if currentMember {
			if err := recordChannelPause(ctx, tx, g.ChannelID, outcome, at); err != nil {
				return err
			}
		}
		if err := scheduleAggregate(ctx, tx, id, at); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// A real response can arrive after another parent epoch starts. Refine only the
// exact historical attempt; never its successor's job, clock or retry authority.
func recordAggregateLateMember(ctx context.Context, tx *sql.Tx, d NotificationDelivery, number int, token string, at time.Time, outcome DeliveryOutcome) error {
	var previous, class string
	if err := tx.QueryRowContext(ctx, `SELECT outcome,error_class FROM delivery_attempts WHERE delivery_id=? AND attempt_number=? AND lease_token=?`, d.ID, number, token).Scan(&previous, &class); err != nil {
		return err
	}
	status, errorClass := "failed", outcome.ErrorClass
	if outcome.Delivered {
		status, errorClass = "delivered", ""
	} else if outcome.Ambiguous {
		status = "unknown"
	}
	if previous == status && class == errorClass {
		return nil
	}
	if previous != "started" && !(previous == "unknown" && (class == "lease_expired" || (d.Status == "canceled" && d.AttemptSequence == number && class == d.ErrorClass))) {
		return ErrDeliveryLeaseLost
	}
	if _, err := tx.ExecContext(ctx, `UPDATE delivery_attempts SET outcome=?,error_class=?,completed_at=? WHERE delivery_id=? AND attempt_number=? AND lease_token=?`, status, errorClass, formatTime(at), d.ID, number, token); err != nil {
		return err
	}
	return recordDeliveryHistory(ctx, tx, d, at, outcome.Delivered, errorClass)
}
