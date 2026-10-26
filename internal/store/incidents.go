package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"
)

var ErrObservationObsolete = errors.New("alert rule changed during evaluation")

type Incident struct {
	Seq                  int64           `json:"seq"`
	ID                   string          `json:"id"`
	Fingerprint          string          `json:"-"`
	RuleID               string          `json:"rule_id"`
	NodeID               string          `json:"node_id"`
	NodeName             string          `json:"node_name"`
	Kind                 string          `json:"kind"`
	RuleSnapshot         json.RawMessage `json:"-"`
	State                string          `json:"state"`
	StartedAt            time.Time       `json:"started_at"`
	FiredAt              *time.Time      `json:"fired_at,omitempty"`
	ResolvedAt           *time.Time      `json:"resolved_at,omitempty"`
	ObservedAt           *time.Time      `json:"observed_at,omitempty"`
	UpdatedAt            time.Time       `json:"updated_at"`
	TriggerSince         *time.Time      `json:"trigger_since,omitempty"`
	RecoverySince        *time.Time      `json:"recovery_since,omitempty"`
	ObservationStale     bool            `json:"observation_stale"`
	LastMessage          string          `json:"message"`
	ResolutionReason     string          `json:"resolution_reason,omitempty"`
	Origin               string          `json:"origin"`
	ApproximateStart     bool            `json:"approximate_start"`
	LastEnqueuedAt       *time.Time      `json:"-"`
	NotificationSequence int             `json:"-"`
}

type AlertObservation struct {
	At                  time.Time
	Known               bool
	Active              bool
	Message             string
	NotificationTitle   string
	NotificationMessage string
}

// DeliveryNotification is a durable rendered payload, never channel credentials.
type DeliveryNotification struct {
	Title      string    `json:"title"`
	Message    string    `json:"message"`
	State      string    `json:"state"`
	Kind       string    `json:"kind"`
	NodeID     string    `json:"node_id"`
	NodeName   string    `json:"node_name"`
	RuleID     string    `json:"rule_id"`
	IncidentID string    `json:"incident_id"`
	Timestamp  time.Time `json:"timestamp"`
}

type incidentRuleSnapshot struct {
	NodeID          string          `json:"node_id"`
	ChannelID       string          `json:"channel_id"`
	Kind            string          `json:"kind"`
	Config          json.RawMessage `json:"config"`
	CooldownSeconds int             `json:"cooldown_seconds"`
}

func snapshotRule(rule AlertRule) json.RawMessage {
	raw, _ := json.Marshal(incidentRuleSnapshot{rule.NodeID, rule.ChannelID, rule.Kind, rule.Config, rule.CooldownSeconds})
	return raw
}
func sameSnapshot(a, b json.RawMessage) bool {
	var left, right any
	return json.Unmarshal(a, &left) == nil && json.Unmarshal(b, &right) == nil && reflect.DeepEqual(left, right)
}

type rowScanner interface{ Scan(...any) error }

const incidentColumns = `seq,id,fingerprint,rule_id,node_id,node_name,kind,rule_snapshot_json,state,started_at,fired_at,resolved_at,observed_at,updated_at,trigger_since,recovery_since,observation_stale,last_message,resolution_reason,origin,approximate_start,last_enqueued_at,notification_sequence`

func scanIncident(row rowScanner) (Incident, error) {
	var i Incident
	var snapshot, started, updated string
	var fired, resolved, observed, trigger, recovery, enqueued sql.NullString
	err := row.Scan(&i.Seq, &i.ID, &i.Fingerprint, &i.RuleID, &i.NodeID, &i.NodeName, &i.Kind, &snapshot, &i.State, &started, &fired, &resolved, &observed, &updated, &trigger, &recovery, &i.ObservationStale, &i.LastMessage, &i.ResolutionReason, &i.Origin, &i.ApproximateStart, &enqueued, &i.NotificationSequence)
	if err != nil {
		return i, err
	}
	i.RuleSnapshot = json.RawMessage(snapshot)
	if i.StartedAt, err = parseTime(started); err != nil {
		return i, err
	}
	if i.UpdatedAt, err = parseTime(updated); err != nil {
		return i, err
	}
	for _, field := range []struct {
		raw    sql.NullString
		target **time.Time
	}{{fired, &i.FiredAt}, {resolved, &i.ResolvedAt}, {observed, &i.ObservedAt}, {trigger, &i.TriggerSince}, {recovery, &i.RecoverySince}, {enqueued, &i.LastEnqueuedAt}} {
		if field.raw.Valid {
			at, err := parseTime(field.raw.String)
			if err != nil {
				return i, err
			}
			*field.target = &at
		}
	}
	return i, nil
}
func ruleInTransaction(ctx context.Context, tx *sql.Tx, id string) (AlertRule, error) {
	var rule AlertRule
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT id,node_id,channel_id,kind,config_json,enabled,cooldown_seconds FROM alert_rules WHERE id=?`, id).Scan(&rule.ID, &rule.NodeID, &rule.ChannelID, &rule.Kind, &raw, &rule.Enabled, &rule.CooldownSeconds)
	rule.Config = json.RawMessage(raw)
	return rule, err
}
func saveIncident(ctx context.Context, tx *sql.Tx, i *Incident) error {
	_, err := tx.ExecContext(ctx, `UPDATE alert_incidents SET state=?,fired_at=?,resolved_at=?,observed_at=?,updated_at=?,trigger_since=?,recovery_since=?,observation_stale=?,last_message=?,resolution_reason=?,last_enqueued_at=?,notification_sequence=? WHERE id=?`, i.State, nullableTime(i.FiredAt), nullableTime(i.ResolvedAt), nullableTime(i.ObservedAt), formatTime(i.UpdatedAt), nullableTime(i.TriggerSince), nullableTime(i.RecoverySince), i.ObservationStale, i.LastMessage, i.ResolutionReason, nullableTime(i.LastEnqueuedAt), i.NotificationSequence, i.ID)
	return err
}

func cancelIncidentDeliveries(ctx context.Context, tx *sql.Tx, id, reason string, now time.Time) error {
	// An in-flight request may already have reached the receiver. Preserve that
	// ambiguity instead of claiming that it was definitely never sent.
	if _, err := tx.ExecContext(ctx, `UPDATE delivery_attempts SET outcome='unknown',completed_at=?,error_class=? WHERE outcome='started' AND delivery_id IN (SELECT id FROM notification_deliveries WHERE incident_id=? AND status='inflight')`, formatTime(now), reason, id); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE notification_deliveries SET status='canceled',lease_token=NULL,lease_until_ms=NULL,finished_at=?,updated_at=?,error_class=? WHERE incident_id=? AND status IN ('pending','inflight')`, formatTime(now), formatTime(now), reason, id)
	return err
}
func endIncident(ctx context.Context, tx *sql.Tx, i *Incident, reason string, now time.Time) error {
	i.State = "resolved"
	i.ResolvedAt = &now
	i.UpdatedAt = now
	i.ResolutionReason = reason
	i.RecoverySince = nil
	i.TriggerSince = nil
	if err := saveIncident(ctx, tx, i); err != nil {
		return err
	}
	return cancelIncidentDeliveries(ctx, tx, i.ID, reason, now)
}

// ReconcileIncidents closes administrative changes without technical recovery
// notifications, even when no rule remains to drive normal evaluation.
func (s *Store) ReconcileIncidents(ctx context.Context, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE alert_incidents SET updated_at=updated_at WHERE seq=-1"); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, "SELECT "+incidentColumns+" FROM alert_incidents WHERE state IN ('pending','firing')")
	if err != nil {
		return err
	}
	items := []Incident{}
	for rows.Next() {
		i, scanErr := scanIncident(rows)
		if scanErr != nil {
			rows.Close()
			return scanErr
		}
		items = append(items, i)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for index := range items {
		i := &items[index]
		rule, err := ruleInTransaction(ctx, tx, i.RuleID)
		reason := ""
		switch {
		case errors.Is(err, sql.ErrNoRows):
			reason = "rule_deleted"
		case err != nil:
			return err
		case !rule.Enabled:
			reason = "rule_disabled"
		case !sameSnapshot(i.RuleSnapshot, snapshotRule(rule)):
			reason = "rule_changed"
		}
		if reason != "" {
			at := now.UTC()
			if at.Before(i.UpdatedAt) {
				at = i.UpdatedAt
			}
			if err := endIncident(ctx, tx, i, reason, at); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// ObserveAlert commits the fault state and notification intent together. It does
// not perform network I/O and does not depend on encryption/delivery availability.
func (s *Store) ObserveAlert(ctx context.Context, rule AlertRule, node Node, o AlertObservation) (*Incident, error) {
	if o.At.IsZero() || node.ID != rule.NodeID || len(o.Message) > 16000 || len(o.NotificationMessage) > 16000 || len(o.NotificationTitle) > 1000 {
		return nil, errors.New("invalid alert observation")
	}
	var timing struct {
		Duration int  `json:"duration_seconds"`
		Recovery *int `json:"recovery_seconds"`
		Repeat   int  `json:"repeat_seconds"`
	}
	if err := json.Unmarshal(rule.Config, &timing); err != nil {
		return nil, err
	}
	recovery := timing.Duration
	if timing.Recovery != nil {
		recovery = *timing.Recovery
	}
	repeat := rule.CooldownSeconds
	if timing.Repeat > 0 {
		repeat = timing.Repeat
	}
	if timing.Duration < 0 || timing.Duration > 2592000 || recovery < 0 || recovery > 2592000 || repeat < 30 || repeat > 2592000 {
		return nil, errors.New("invalid alert timing")
	}
	o.At = o.At.UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "UPDATE alert_rules SET updated_at=updated_at WHERE id=?", rule.ID); err != nil {
		return nil, err
	}
	current, err := ruleInTransaction(ctx, tx, rule.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrObservationObsolete
	}
	if err != nil {
		return nil, err
	}
	if !current.Enabled || !rule.Enabled || !sameSnapshot(snapshotRule(current), snapshotRule(rule)) {
		return nil, ErrObservationObsolete
	}
	if valid, err := policyRuleCurrent(ctx, tx, current); err != nil {
		return nil, err
	} else if !valid {
		return nil, ErrObservationObsolete
	}
	fingerprint := rule.Kind + ":" + rule.ID + ":" + node.ID
	i, err := scanIncident(tx.QueryRowContext(ctx, "SELECT "+incidentColumns+" FROM alert_incidents WHERE fingerprint=? ORDER BY seq DESC LIMIT 1", fingerprint))
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if exists && !o.At.After(i.UpdatedAt) {
		return &i, tx.Commit()
	}
	if exists && i.State != "resolved" && !sameSnapshot(i.RuleSnapshot, snapshotRule(rule)) {
		if err := endIncident(ctx, tx, &i, "rule_changed", o.At); err != nil {
			return nil, err
		}
	}
	if !exists || i.State == "resolved" {
		if !o.Known || !o.Active {
			return nil, tx.Commit()
		}
		i = Incident{ID: randomID(), Fingerprint: fingerprint, RuleID: rule.ID, NodeID: node.ID, NodeName: node.Name, Kind: rule.Kind, RuleSnapshot: snapshotRule(rule), State: "pending", StartedAt: o.At, UpdatedAt: o.At, TriggerSince: &o.At, Origin: "live"}
		result, err := tx.ExecContext(ctx, `INSERT INTO alert_incidents(id,fingerprint,rule_id,node_id,node_name,kind,rule_snapshot_json,state,started_at,updated_at,trigger_since) VALUES(?,?,?,?,?,?,?,'pending',?,?,?)`, i.ID, i.Fingerprint, i.RuleID, i.NodeID, i.NodeName, i.Kind, string(i.RuleSnapshot), formatTime(o.At), formatTime(o.At), formatTime(o.At))
		if err != nil {
			return nil, err
		}
		i.Seq, _ = result.LastInsertId()
	}
	i.UpdatedAt = o.At
	i.ObservationStale = !o.Known
	if !o.Known {
		i.TriggerSince = nil
		i.RecoverySince = nil
		if err := saveIncident(ctx, tx, &i); err != nil {
			return nil, err
		}
		return &i, tx.Commit()
	}
	i.ObservedAt = &o.At
	i.LastMessage = o.Message
	justResolved := false
	switch i.State {
	case "pending":
		if !o.Active {
			if err := endIncident(ctx, tx, &i, "condition_cleared", o.At); err != nil {
				return nil, err
			}
			return &i, tx.Commit()
		}
		if i.TriggerSince == nil {
			i.TriggerSince = &o.At
		}
		if o.At.Sub(*i.TriggerSince) >= time.Duration(timing.Duration)*time.Second {
			i.State = "firing"
			i.FiredAt = &o.At
			i.TriggerSince = nil
		}
	case "firing":
		if o.Active {
			i.RecoverySince = nil
		} else {
			if i.RecoverySince == nil {
				i.RecoverySince = &o.At
			}
			if o.At.Sub(*i.RecoverySince) >= time.Duration(recovery)*time.Second {
				if err := endIncident(ctx, tx, &i, "recovered", o.At); err != nil {
					return nil, err
				}
				justResolved = true
			}
		}
	}
	notify := i.State == "firing" && o.Active && (i.LastEnqueuedAt == nil || o.At.Sub(*i.LastEnqueuedAt) >= time.Duration(repeat)*time.Second)
	if justResolved {
		var attempted int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM notification_deliveries WHERE incident_id=? AND notification_type='firing' AND attempt_count>0", i.ID).Scan(&attempted); err != nil {
			return nil, err
		}
		notify = attempted > 0 || (i.Origin == "legacy" && i.NotificationSequence > 0)
	}
	if notify {
		if err := queueIncidentNotification(ctx, tx, &i, rule.ChannelID, o); err != nil {
			return nil, err
		}
	}
	if err := saveIncident(ctx, tx, &i); err != nil {
		return nil, err
	}
	return &i, tx.Commit()
}

func queueIncidentNotification(ctx context.Context, tx *sql.Tx, i *Incident, channelID string, o AlertObservation) error {
	var outstanding int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM notification_deliveries WHERE incident_id=? AND status IN ('pending','inflight')", i.ID).Scan(&outstanding); err != nil {
		return err
	}
	if outstanding > 0 {
		return nil
	}
	var name, provider string
	var enabled bool
	err := tx.QueryRowContext(ctx, "SELECT name,COALESCE(NULLIF(provider,''),kind),enabled FROM notification_channels WHERE id=?", channelID).Scan(&name, &provider, &enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	title, message := o.NotificationTitle, o.NotificationMessage
	if title == "" {
		title = "MyProbe 告警"
		if i.State == "resolved" {
			title = "MyProbe 告警恢复"
		}
	}
	if message == "" {
		message = o.Message
	}
	payload, err := json.Marshal(DeliveryNotification{title, message, i.State, i.Kind, i.NodeID, i.NodeName, i.RuleID, i.ID, o.At})
	if err != nil {
		return err
	}
	i.NotificationSequence++
	key := fmt.Sprintf("%s:%s:%d:%s", i.ID, i.State, i.NotificationSequence, channelID)
	_, err = tx.ExecContext(ctx, `INSERT INTO notification_deliveries(id,incident_id,channel_id,channel_name,provider,notification_type,idempotency_key,payload_json,status,available_at_ms,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,'pending',?,?,?)`, randomID(), i.ID, channelID, name, provider, i.State, key, string(payload), o.At.UnixMilli(), formatTime(o.At), formatTime(o.At))
	if err == nil {
		i.LastEnqueuedAt = &o.At
	}
	return err
}

func (s *Store) Incident(ctx context.Context, id string) (Incident, error) {
	item, err := scanIncident(s.db.QueryRowContext(ctx, "SELECT "+incidentColumns+" FROM alert_incidents WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return item, err
}
