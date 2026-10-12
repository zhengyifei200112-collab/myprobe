package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const AggregateMemberLimit = 10
const AggregateMessageByteLimit = 1600
const AggregatePayloadByteLimit = 16 << 10

type NotificationAggregate struct {
	Seq              int64      `json:"seq"`
	ID               string     `json:"id"`
	PolicyID         string     `json:"policy_id"`
	PolicyRevision   int64      `json:"policy_revision"`
	ChannelID        string     `json:"channel_id"`
	Provider         string     `json:"provider"`
	NotificationType string     `json:"notification_type"`
	StartsAt         time.Time  `json:"starts_at"`
	EndsAt           time.Time  `json:"ends_at"`
	Status           string     `json:"status"`
	Sealed           bool       `json:"sealed"`
	AttemptCount     int        `json:"attempt_count"`
	AvailableAt      time.Time  `json:"available_at"`
	LeaseToken       string     `json:"-"`
	LeaseUntil       *time.Time `json:"-"`
}

const aggregateColumns = `seq,id,policy_id,policy_revision,channel_id,provider,notification_type,starts_at_ms,ends_at_ms,status,sealed,attempt_count,available_at_ms,lease_token,lease_until_ms`

func scanAggregate(row rowScanner) (NotificationAggregate, error) {
	var g NotificationAggregate
	var start, end, available int64
	var token sql.NullString
	var until sql.NullInt64
	err := row.Scan(&g.Seq, &g.ID, &g.PolicyID, &g.PolicyRevision, &g.ChannelID, &g.Provider, &g.NotificationType, &start, &end, &g.Status, &g.Sealed, &g.AttemptCount, &available, &token, &until)
	g.StartsAt, g.EndsAt, g.AvailableAt = time.UnixMilli(start).UTC(), time.UnixMilli(end).UTC(), time.UnixMilli(available).UTC()
	g.LeaseToken = token.String
	if until.Valid {
		at := time.UnixMilli(until.Int64).UTC()
		g.LeaseUntil = &at
	}
	return g, err
}
func ceilMilliseconds(at time.Time) time.Time {
	n := at.UnixMilli()
	if time.UnixMilli(n).Before(at) {
		n++
	}
	return time.UnixMilli(n).UTC()
}

// Explicit zero/omission preserves existing policy timing. Only policy-bound
// deliveries aggregate; a direct legacy rule never groups with an unrelated rule.
func aggregateForNotification(ctx context.Context, tx *sql.Tx, ruleID, channelID, provider, kind string, at time.Time) (string, time.Time, error) {
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT p.definition_json FROM alert_policies p JOIN alert_policy_rule_bindings b ON b.policy_id=p.id WHERE b.rule_id=?`, ruleID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", at, nil
	}
	if err != nil {
		return "", at, err
	}
	var p AlertPolicy
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return "", at, err
	}
	var config struct {
		Seconds *int `json:"aggregation_seconds"`
	}
	if err := json.Unmarshal(p.Config, &config); err != nil {
		return "", at, err
	}
	if config.Seconds == nil || *config.Seconds == 0 {
		return "", at, nil
	}
	if *config.Seconds < 5 || *config.Seconds > 300 {
		return "", at, ErrInvalidAlertPolicy
	}
	var id string
	var end int64
	err = tx.QueryRowContext(ctx, `SELECT g.id,g.ends_at_ms FROM notification_aggregates g WHERE g.policy_id=? AND g.policy_revision=? AND g.channel_id=? AND g.provider=? AND g.notification_type=? AND g.status='pending' AND g.sealed=0 AND g.starts_at_ms<=? AND g.ends_at_ms>? AND (SELECT count(*) FROM notification_deliveries d WHERE d.aggregate_id=g.id)<? ORDER BY g.seq DESC LIMIT 1`, p.ID, p.Revision, channelID, provider, kind, at.UnixMilli(), at.UnixMilli(), AggregateMemberLimit).Scan(&id, &end)
	if err == nil {
		return id, time.UnixMilli(end).UTC(), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", at, err
	}
	id = randomID()
	until := ceilMilliseconds(at.Add(time.Duration(*config.Seconds) * time.Second))
	_, err = tx.ExecContext(ctx, `INSERT INTO notification_aggregates(id,policy_id,policy_revision,channel_id,provider,notification_type,starts_at_ms,ends_at_ms,available_at_ms,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, id, p.ID, p.Revision, channelID, provider, kind, at.UnixMilli(), until.UnixMilli(), until.UnixMilli(), formatTime(at), formatTime(at))
	return id, until, err
}
func aggregateDeliveries(ctx context.Context, tx *sql.Tx, id string) ([]NotificationDelivery, error) {
	rows, err := tx.QueryContext(ctx, `SELECT `+deliveryColumns+` FROM notification_deliveries WHERE aggregate_id=? ORDER BY seq LIMIT ?`, id, AggregateMemberLimit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []NotificationDelivery{}
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, d)
	}
	if len(items) > AggregateMemberLimit {
		return nil, errors.New("aggregate member limit exceeded")
	}
	return items, rows.Err()
}
func aggregateApplicable(ctx context.Context, tx *sql.Tx, d NotificationDelivery, at time.Time) (bool, error) {
	valid, err := validDeliveryState(ctx, tx, d)
	if err != nil {
		return false, err
	}
	if !valid {
		return false, cancelDelivery(ctx, tx, d.ID, "not_applicable", at)
	}
	if muted, err := suppressNotificationDelivery(ctx, tx, d, at); muted || err != nil {
		return false, err
	}
	var stale bool
	if err := tx.QueryRowContext(ctx, `SELECT observation_stale FROM alert_incidents WHERE id=?`, d.IncidentID).Scan(&stale); err != nil {
		return false, err
	}
	return !stale || d.NotificationType != "firing", nil
}

// Schedule remaining leaf work, without reviving canceled members. Transport
// history may be mixed, so finished is deliberately not called delivered.
func scheduleAggregate(ctx context.Context, tx *sql.Tx, id string, at time.Time) error {
	var due sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT min(available_at_ms) FROM notification_deliveries WHERE aggregate_id=? AND status='pending'`, id).Scan(&due); err != nil {
		return err
	}
	status := "finished"
	var finished any = formatTime(at)
	available := at.UnixMilli()
	if due.Valid {
		status = "pending"
		finished = nil
		available = due.Int64
	}
	_, err := tx.ExecContext(ctx, `UPDATE notification_aggregates SET status=?,available_at_ms=?,lease_token=NULL,lease_until_ms=NULL,updated_at=?,finished_at=? WHERE id=?`, status, available, formatTime(at), finished, id)
	return err
}

type AggregateNotificationMember struct {
	IncidentID string    `json:"incident_id"`
	NodeID     string    `json:"node_id"`
	NodeName   string    `json:"node_name"`
	EventPath  string    `json:"event_path"`
	ObservedAt time.Time `json:"observed_at"`
}
type AggregateNotification struct {
	Title       string                        `json:"title"`
	Message     string                        `json:"message"`
	State       string                        `json:"state"`
	Kind        string                        `json:"kind"`
	AggregateID string                        `json:"aggregate_id"`
	Timestamp   time.Time                     `json:"timestamp"`
	Members     []AggregateNotificationMember `json:"members"`
}

func briefAggregateText(s string, limit int) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s))
	if len(s) <= limit {
		return s
	}
	n := limit - 3
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}
func renderAggregate(g NotificationAggregate, members []NotificationDelivery) (json.RawMessage, string, error) {
	label := "故障持续"
	if g.NotificationType == "resolved" {
		label = "已恢复"
	}
	n := AggregateNotification{Title: fmt.Sprintf("MyProbe %s汇总（%d 个事件）", label, len(members)), State: g.NotificationType, AggregateID: g.ID, Timestamp: g.EndsAt, Members: []AggregateNotificationMember{}}
	lines := []string{}
	for _, d := range members {
		var p DeliveryNotification
		if err := json.Unmarshal(d.Payload, &p); err != nil {
			return nil, "", err
		}
		if n.Kind == "" {
			n.Kind = p.Kind
		}
		path := "/admin?incident=" + url.QueryEscape(d.IncidentID)
		n.Members = append(n.Members, AggregateNotificationMember{d.IncidentID, p.NodeID, briefAggregateText(p.NodeName, 80), path, p.Timestamp})
		lines = append(lines, briefAggregateText(p.NodeName, 28)+" · "+briefAggregateText(p.Message, 38)+" · "+path)
	}
	n.Message = strings.Join(lines, "\n") + "\n摘要已截短，详情及观测时间见事件入口。"
	if len(n.Message) > AggregateMessageByteLimit {
		return nil, "", errors.New("aggregate message limit exceeded")
	}
	raw, err := json.Marshal(n)
	if err != nil {
		return nil, "", err
	}
	if len(raw) > AggregatePayloadByteLimit {
		return nil, "", errors.New("aggregate payload limit exceeded")
	}
	sum := sha256.Sum256(raw)
	return raw, "aggregate:" + g.ID + ":" + hex.EncodeToString(sum[:]), nil
}
