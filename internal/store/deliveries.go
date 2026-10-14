package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"time"
)

var ErrDeliveryLeaseLost = errors.New("notification delivery lease lost")
var ErrDeliveryCanceled = errors.New("notification delivery no longer applicable")
var ErrDeliveryDeferred = errors.New("notification delivery deferred before sending")

type NotificationDelivery struct {
	SendAuthorized      bool            `json:"-"`
	AggregateID         string          `json:"aggregate_id,omitempty"`
	Seq                 int64           `json:"seq"`
	ID                  string          `json:"id"`
	IncidentID          string          `json:"incident_id"`
	ChannelID           string          `json:"channel_id"`
	ChannelName         string          `json:"channel_name"`
	Provider            string          `json:"provider"`
	NotificationType    string          `json:"notification_type"`
	IdempotencyKey      string          `json:"-"`
	Payload             json.RawMessage `json:"-"`
	Status              string          `json:"status"`
	AttemptCount        int             `json:"attempt_count"`
	AttemptSequence     int             `json:"attempt_sequence"`
	DeliveryPausedUntil *time.Time      `json:"delivery_paused_until,omitempty"`
	AvailableAt         time.Time       `json:"available_at"`
	LeaseToken          string          `json:"-"`
	LeaseUntil          *time.Time      `json:"-"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
	FinishedAt          *time.Time      `json:"finished_at,omitempty"`
	ErrorClass          string          `json:"error_class,omitempty"`
}

const deliveryColumns = `seq,id,incident_id,channel_id,channel_name,provider,notification_type,idempotency_key,payload_json,status,attempt_count,available_at_ms,lease_token,lease_until_ms,created_at,updated_at,finished_at,error_class,aggregate_id,attempt_sequence,send_authorized,(SELECT delivery_paused_until_ms FROM notification_channels WHERE id=notification_deliveries.channel_id)`

func scanDelivery(row rowScanner) (NotificationDelivery, error) {
	var d NotificationDelivery
	var payload, created, updated string
	var finished, lease, aggregate sql.NullString
	var available int64
	var until sql.NullInt64
	var paused sql.NullInt64
	err := row.Scan(&d.Seq, &d.ID, &d.IncidentID, &d.ChannelID, &d.ChannelName, &d.Provider, &d.NotificationType, &d.IdempotencyKey, &payload, &d.Status, &d.AttemptCount, &available, &lease, &until, &created, &updated, &finished, &d.ErrorClass, &aggregate, &d.AttemptSequence, &d.SendAuthorized, &paused)
	if err != nil {
		return d, err
	}
	d.Payload = json.RawMessage(payload)
	d.AvailableAt = time.UnixMilli(available).UTC()
	d.LeaseToken = lease.String
	d.AggregateID = aggregate.String
	d.DeliveryPausedUntil = channelPauseTime(paused)
	if until.Valid {
		at := time.UnixMilli(until.Int64).UTC()
		d.LeaseUntil = &at
	}
	if d.CreatedAt, err = parseTime(created); err != nil {
		return d, err
	}
	if d.UpdatedAt, err = parseTime(updated); err != nil {
		return d, err
	}
	if finished.Valid {
		at, err := parseTime(finished.String)
		if err != nil {
			return d, err
		}
		d.FinishedAt = &at
	}
	return d, nil
}
func cancelDelivery(ctx context.Context, tx *sql.Tx, id, reason string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `UPDATE delivery_attempts SET outcome='unknown',completed_at=?,error_class=? WHERE delivery_id=? AND outcome='started'`, formatTime(now), reason, id); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE notification_deliveries SET status='canceled',lease_token=NULL,lease_until_ms=NULL,finished_at=?,updated_at=?,error_class=? WHERE id=? AND status IN ('pending','inflight')`, formatTime(now), formatTime(now), reason, id)
	return err
}
func validDeliveryState(ctx context.Context, tx *sql.Tx, d NotificationDelivery, times ...time.Time) (bool, error) {
	i, err := scanIncident(tx.QueryRowContext(ctx, "SELECT "+incidentColumns+" FROM alert_incidents WHERE id=?", d.IncidentID))
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if d.NotificationType == "firing" && i.State != "firing" {
		return false, nil
	}
	if d.NotificationType == "resolved" && (i.State != "resolved" || (i.ResolutionReason != "recovered" && i.ResolutionReason != "legacy_recovery")) {
		return false, nil
	}
	rule, err := ruleInTransaction(ctx, tx, i.RuleID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !rule.Enabled || rule.ChannelID != d.ChannelID || !sameSnapshot(i.RuleSnapshot, snapshotRule(rule)) {
		return false, nil
	}
	if valid, err := policyRuleCurrent(ctx, tx, rule, times...); err != nil || !valid {
		return false, err
	}
	var enabled bool
	var provider string
	err = tx.QueryRowContext(ctx, "SELECT enabled,COALESCE(NULLIF(provider,''),kind) FROM notification_channels WHERE id=?", d.ChannelID).Scan(&enabled, &provider)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return enabled && provider == d.Provider, nil
}

// ClaimDelivery recovers expired leases and atomically reserves a due job. Network
// I/O is deliberately outside this transaction. The caller must recheck before send.
func (s *Store) ClaimDelivery(ctx context.Context, now time.Time, lease time.Duration) (*NotificationDelivery, error) {
	if now.IsZero() || lease < 10*time.Second || lease > 5*time.Minute {
		return nil, errors.New("invalid delivery lease")
	}
	now = now.UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE notification_deliveries SET updated_at=updated_at WHERE seq=-1"); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE delivery_attempts SET outcome='unknown',completed_at=?,error_class='lease_expired' WHERE outcome='started' AND delivery_id IN (SELECT id FROM notification_deliveries WHERE aggregate_id IS NULL AND status='inflight' AND lease_until_ms<=?)`, formatTime(now), now.UnixMilli()); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE alert_incidents SET last_enqueued_at=? WHERE id IN (SELECT incident_id FROM notification_deliveries WHERE aggregate_id IS NULL AND status='inflight' AND lease_until_ms<=? AND attempt_count>=5)`, formatTime(now), now.UnixMilli()); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE notification_deliveries SET status=CASE WHEN attempt_count>=5 THEN 'failed' ELSE 'pending' END,finished_at=CASE WHEN attempt_count>=5 THEN ? ELSE NULL END,lease_token=NULL,lease_until_ms=NULL,available_at_ms=?,updated_at=?,error_class='lease_expired' WHERE aggregate_id IS NULL AND status='inflight' AND lease_until_ms<=?`, formatTime(now), now.UnixMilli(), formatTime(now), now.UnixMilli()); err != nil {
		return nil, err
	}
	// Limit cleanup work per claim. Unknown observations pause firing jobs without
	// using an attempt; disabled/deleted destinations cancel outstanding work.
	rows, err := tx.QueryContext(ctx, "SELECT "+deliveryColumns+" FROM notification_deliveries WHERE aggregate_id IS NULL AND status='pending' AND available_at_ms<=? AND NOT EXISTS (SELECT 1 FROM notification_channels c WHERE c.id=notification_deliveries.channel_id AND c.delivery_paused_until_ms>?) AND NOT EXISTS (SELECT 1 FROM alert_incidents i WHERE i.id=notification_deliveries.incident_id AND i.state='firing' AND i.observation_stale=1 AND notification_deliveries.notification_type='firing') ORDER BY seq LIMIT 100", now.UnixMilli(), now.UnixMilli())
	if err != nil {
		return nil, err
	}
	candidates := []NotificationDelivery{}
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, d)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for index := range candidates {
		d := &candidates[index]
		valid, err := validDeliveryState(ctx, tx, *d, now)
		if err != nil {
			return nil, err
		}
		if !valid {
			if err := cancelDelivery(ctx, tx, d.ID, "configuration_changed", now); err != nil {
				return nil, err
			}
			continue
		}
		if suppressed, err := suppressNotificationDelivery(ctx, tx, *d, now); err != nil {
			return nil, err
		} else if suppressed {
			continue
		}
		var stale bool
		if err := tx.QueryRowContext(ctx, "SELECT observation_stale FROM alert_incidents WHERE id=?", d.IncidentID).Scan(&stale); err != nil {
			return nil, err
		}
		if stale && d.NotificationType == "firing" {
			continue
		}
		if d.AttemptCount >= 5 {
			if _, err := tx.ExecContext(ctx, "UPDATE notification_deliveries SET status='failed',finished_at=?,updated_at=? WHERE id=?", formatTime(now), formatTime(now), d.ID); err != nil {
				return nil, err
			}
			continue
		}
		d.LeaseToken = randomID()
		until := now.Add(lease)
		d.LeaseUntil = &until
		d.AttemptCount++
		d.AttemptSequence++
		d.Status = "inflight"
		d.SendAuthorized = false
		d.UpdatedAt = now
		if _, err := tx.ExecContext(ctx, `UPDATE notification_deliveries SET status='inflight',send_authorized=0,lease_token=?,lease_until_ms=?,attempt_count=?,attempt_sequence=?,updated_at=? WHERE id=?`, d.LeaseToken, until.UnixMilli(), d.AttemptCount, d.AttemptSequence, formatTime(now), d.ID); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO delivery_attempts(delivery_id,attempt_number,lease_token,started_at,outcome) VALUES(?,?,?,?,'started')`, d.ID, d.AttemptSequence, d.LeaseToken, formatTime(now)); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return d, nil
	}
	return nil, tx.Commit()
}

// CheckDeliveryLease is the final persisted-state check immediately before sending.
// A concurrent change after it can still race a remote send; attempt history keeps
// this uncertainty rather than promising exactly-once delivery.
func (s *Store) CheckDeliveryLease(ctx context.Context, id, token string, now time.Time, revisions ...int64) error {
	if len(revisions) > 1 {
		return ErrDeliveryLeaseLost
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE notification_deliveries SET updated_at=updated_at WHERE id=?", id); err != nil {
		return err
	}
	d, err := scanDelivery(tx.QueryRowContext(ctx, "SELECT "+deliveryColumns+" FROM notification_deliveries WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrDeliveryLeaseLost
	}
	if err != nil {
		return err
	}
	if d.AggregateID != "" {
		return ErrDeliveryLeaseLost
	}
	if d.Status == "canceled" && (d.ErrorClass == "silence" || d.ErrorClass == "maintenance" || d.ErrorClass == "maintenance_and_silence") {
		var ownsAttempt bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM delivery_attempts WHERE delivery_id=? AND lease_token=? AND attempt_number=?)`, d.ID, token, d.AttemptSequence).Scan(&ownsAttempt); err != nil {
			return err
		}
		if ownsAttempt {
			// An action may cancel the claimed job before this final check. Its
			// owner should skip sending normally, without reviving the lease.
			return ErrDeliveryCanceled
		}
	}
	if d.Status != "inflight" || d.LeaseToken != token || d.LeaseUntil == nil || !now.Before(*d.LeaseUntil) || d.SendAuthorized {
		return ErrDeliveryLeaseLost
	}
	valid, err := validDeliveryState(ctx, tx, d, now)
	if err != nil {
		return err
	}
	if !valid {
		if err := cancelDelivery(ctx, tx, id, "configuration_changed", now.UTC()); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		return ErrDeliveryCanceled
	}
	if suppressed, err := suppressNotificationDelivery(ctx, tx, d, now); err != nil {
		return err
	} else if suppressed {
		if err := tx.Commit(); err != nil {
			return err
		}
		return ErrDeliveryCanceled
	}
	var stale bool
	if err := tx.QueryRowContext(ctx, "SELECT observation_stale FROM alert_incidents WHERE id=?", d.IncidentID).Scan(&stale); err != nil {
		return err
	}
	if stale && d.NotificationType == "firing" {
		if _, err := tx.ExecContext(ctx, `UPDATE delivery_attempts SET outcome='canceled',completed_at=?,error_class='observation_stale' WHERE delivery_id=? AND lease_token=? AND outcome='started'`, formatTime(now), id, token); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE notification_deliveries SET status='pending',available_at_ms=?,lease_token=NULL,lease_until_ms=NULL,updated_at=?,error_class='observation_stale' WHERE id=?`, now.Add(15*time.Second).UnixMilli(), formatTime(now), id); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		return ErrDeliveryDeferred
	}
	var revision int64
	var paused sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT delivery_revision,delivery_paused_until_ms FROM notification_channels WHERE id=?`, d.ChannelID).Scan(&revision, &paused); err != nil {
		return err
	}
	class := ""
	if len(revisions) == 1 && revisions[0] != revision {
		class = "channel_configuration_changed"
	} else if paused.Valid && paused.Int64 > now.UnixMilli() {
		class = "channel_rate_limited"
	}
	if class != "" {
		if err := deferUnsentReservation(ctx, tx, d, now, class); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		return ErrDeliveryDeferred
	}
	if _, err := tx.ExecContext(ctx, `UPDATE notification_deliveries SET send_authorized=1 WHERE id=? AND lease_token=?`, id, token); err != nil {
		return err
	}
	var payload struct {
		Kind string `json:"kind"`
	}
	if json.Unmarshal(d.Payload, &payload) != nil {
		return ErrDeliveryCanceled
	}
	if payload.Kind == "service_certificate" {
		result, err := tx.ExecContext(ctx, `UPDATE http_certificate_reminders SET authorized_once=1,updated_at=? WHERE rule_id=(SELECT rule_id FROM alert_incidents WHERE id=?)`, formatTime(now), d.IncidentID)
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil {
			return err
		} else if n != 1 {
			return ErrDeliveryCanceled
		}
	}
	return tx.Commit()
}

type DeliveryOutcome struct {
	ChannelRevision int64     `json:"-"`
	ChannelRetryAt  time.Time `json:"-"`
	Ambiguous       bool
	Delivered       bool
	Permanent       bool
	ErrorClass      string
	RetryAt         time.Time
}

var deliveryErrorClass = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func (s *Store) CompleteDelivery(ctx context.Context, id, token string, now time.Time, outcome DeliveryOutcome) error {
	if (outcome.Delivered && outcome.Ambiguous) || now.IsZero() || (!outcome.Delivered && !deliveryErrorClass.MatchString(outcome.ErrorClass)) {
		return errors.New("invalid delivery outcome")
	}
	now = now.UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE notification_deliveries SET updated_at=updated_at WHERE id=?", id); err != nil {
		return err
	}
	d, err := scanDelivery(tx.QueryRowContext(ctx, "SELECT "+deliveryColumns+" FROM notification_deliveries WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrDeliveryLeaseLost
	}
	if err != nil {
		return err
	}
	if d.AggregateID != "" {
		return ErrDeliveryLeaseLost
	}
	if err := completeDeliveryInTransaction(ctx, tx, d, token, now, outcome); err != nil {
		return err
	}
	if d.Status == "inflight" {
		var canceled bool
		if err := tx.QueryRowContext(ctx, `SELECT status='canceled' FROM notification_deliveries WHERE id=?`, d.ID).Scan(&canceled); err != nil {
			return err
		}
		if !canceled {
			if err := recordChannelPause(ctx, tx, d.ChannelID, outcome, now); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func completeDeliveryInTransaction(ctx context.Context, tx *sql.Tx, d NotificationDelivery, token string, now time.Time, outcome DeliveryOutcome) error {
	return completeDeliveryWithFence(ctx, tx, d, token, now, outcome, false)
}

// Aggregate callers validate the parent epoch under this same writer. They may
// complete its still-current child after wall-clock expiry, before any takeover.
func completeDeliveryWithFence(ctx context.Context, tx *sql.Tx, d NotificationDelivery, token string, now time.Time, outcome DeliveryOutcome, parentFenced bool) error {
	id := d.ID
	if d.Status == "canceled" {
		return completeCanceledAttempt(ctx, tx, d, token, now, outcome)
	}
	if d.Status != "inflight" || d.LeaseToken != token || d.LeaseUntil == nil || (!parentFenced && !now.Before(*d.LeaseUntil)) {
		return ErrDeliveryLeaseLost
	}
	// A management write may have revoked this job since final authorization,
	// before the evaluator next materializes it. Preserve the real receipt but
	// revoke retry and repeat-clock authority under this same writer snapshot.
	valid, err := validDeliveryState(ctx, tx, d, now)
	if err != nil {
		return err
	}
	if !valid {
		if err := cancelDelivery(ctx, tx, id, "configuration_changed", now); err != nil {
			return err
		}
		d.Status, d.ErrorClass = "canceled", "configuration_changed"
		return completeCanceledAttempt(ctx, tx, d, token, now, outcome)
	}
	status, attemptStatus := "pending", "failed"
	if outcome.Ambiguous {
		attemptStatus = "unknown"
	}
	var finished any
	errorClass := outcome.ErrorClass
	available := now.UnixMilli()
	if outcome.Delivered {
		status = "delivered"
		attemptStatus = "delivered"
		errorClass = ""
		finished = formatTime(now)
	} else if outcome.Permanent || d.AttemptCount >= 5 {
		status = "failed"
		finished = formatTime(now)
	} else {
		if !outcome.RetryAt.After(now) {
			return errors.New("retry must be in the future")
		}
		available = outcome.RetryAt.UnixMilli()
		// A retry deadline is a lower bound. Round up to storage precision so
		// truncation cannot make a provider's Retry-After eligible early.
		if time.UnixMilli(available).Before(outcome.RetryAt) {
			available++
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE delivery_attempts SET outcome=?,completed_at=?,error_class=? WHERE delivery_id=? AND lease_token=? AND outcome='started'`, attemptStatus, formatTime(now), errorClass, id, token); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE notification_deliveries SET status=?,available_at_ms=?,lease_token=NULL,lease_until_ms=NULL,updated_at=?,finished_at=?,error_class=? WHERE id=?`, status, available, formatTime(now), finished, errorClass, id); err != nil {
		return err
	}
	if status == "delivered" || status == "failed" {
		// Repeats start from the completed logical delivery, so a slow series of
		// retries is not immediately followed by another identical reminder.
		if _, err := tx.ExecContext(ctx, "UPDATE alert_incidents SET last_enqueued_at=? WHERE id=?", formatTime(now), d.IncidentID); err != nil {
			return err
		}
	}
	// Preserve the legacy delivery-history endpoint without making alert_states
	// an evaluation authority again. Deleted rules simply have no legacy entry.
	if err := recordDeliveryHistory(ctx, tx, d, now, outcome.Delivered, errorClass); err != nil {
		return err
	}
	return nil
}

// A cancellation revokes retry authority, but a still-running sender can obtain
// a real provider result. Only the canceled latest attempt's token may refine
// its history; it never changes the logical job or incident repeat clock.
func completeCanceledAttempt(ctx context.Context, tx *sql.Tx, d NotificationDelivery, token string, now time.Time, outcome DeliveryOutcome) error {
	var number int
	var previous, previousError, started string
	err := tx.QueryRowContext(ctx, `SELECT attempt_number,outcome,error_class,started_at FROM delivery_attempts WHERE delivery_id=? AND lease_token=?`, d.ID, token).Scan(&number, &previous, &previousError, &started)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrDeliveryLeaseLost
	}
	if err != nil {
		return err
	}
	if number != d.AttemptSequence {
		return ErrDeliveryLeaseLost
	}
	startedAt, err := parseTime(started)
	if err != nil {
		return err
	}
	if now.Before(startedAt) {
		return ErrDeliveryLeaseLost
	}
	status, errorClass := "failed", outcome.ErrorClass
	if outcome.Delivered {
		status, errorClass = "delivered", ""
	} else if outcome.Ambiguous {
		status = "unknown"
	}
	// Terminal/identical refinements may be retried after a lost database result.
	if previous == status && previousError == errorClass {
		return nil
	}
	if previous != "unknown" || previousError != d.ErrorClass {
		return ErrDeliveryLeaseLost
	}
	if _, err := tx.ExecContext(ctx, `UPDATE delivery_attempts SET outcome=?,completed_at=?,error_class=? WHERE delivery_id=? AND lease_token=?`, status, formatTime(now), errorClass, d.ID, token); err != nil {
		return err
	}
	if err := recordDeliveryHistory(ctx, tx, d, now, outcome.Delivered, errorClass); err != nil {
		return err
	}
	return nil
}

func recordDeliveryHistory(ctx context.Context, tx *sql.Tx, d NotificationDelivery, now time.Time, delivered bool, errorClass string) error {
	var payload DeliveryNotification
	if err := json.Unmarshal(d.Payload, &payload); err != nil {
		return err
	}
	legacyState := "failed"
	var deliveredAt any
	if delivered {
		legacyState = d.NotificationType
		deliveredAt = formatTime(now)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO alert_events(id,rule_id,node_id,state,fingerprint,message,delivery_error,created_at,delivered_at) SELECT ?,r.id,r.node_id,?,?,?, ?,?,? FROM alert_rules r WHERE r.id=?`, randomID(), legacyState, payload.Kind+":"+payload.RuleID+":"+payload.NodeID, payload.Message, errorClass, formatTime(now), deliveredAt, payload.RuleID); err != nil {
		return err
	}
	return nil
}

func (s *Store) Delivery(ctx context.Context, id string) (NotificationDelivery, error) {
	item, err := scanDelivery(s.db.QueryRowContext(ctx, "SELECT "+deliveryColumns+" FROM notification_deliveries WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return item, err
}
