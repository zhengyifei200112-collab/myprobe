package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

const (
	ChannelKindWebhook  = "webhook"
	ChannelKindTelegram = "telegram"
	ChannelKindDiscord  = "discord"
	ChannelKindSMTP     = "smtp"
)

func validChannelKind(kind string) bool {
	return kind == ChannelKindWebhook || kind == ChannelKindTelegram || kind == ChannelKindDiscord || kind == ChannelKindSMTP
}
func storageChannelKind(kind string) string {
	if kind == ChannelKindTelegram {
		return ChannelKindTelegram
	}
	return ChannelKindWebhook
}

func (s *Store) CreateNotificationChannel(ctx context.Context, name, kind, encrypted string) (NotificationChannel, error) {
	name = strings.TrimSpace(name)
	if name == "" || encrypted == "" || !validChannelKind(kind) {
		return NotificationChannel{}, errors.New("invalid notification channel")
	}
	now := time.Now().UTC()
	item := NotificationChannel{ID: randomID(), Name: name, Kind: kind, ConfigEncrypted: encrypted, Enabled: true, CreatedAt: now, UpdatedAt: now}
	_, err := s.db.ExecContext(ctx, `INSERT INTO notification_channels(id,name,kind,provider,config_encrypted,enabled,created_at,updated_at) VALUES(?,?,?,?,?,1,?,?)`, item.ID, item.Name, storageChannelKind(kind), kind, item.ConfigEncrypted, formatTime(now), formatTime(now))
	return item, err
}

func (s *Store) UpdateNotificationChannel(ctx context.Context, id, name, kind string, encrypted *string, enabled bool) (NotificationChannel, error) {
	name = strings.TrimSpace(name)
	if name == "" || !validChannelKind(kind) {
		return NotificationChannel{}, errors.New("invalid notification channel")
	}
	var result sql.Result
	var err error
	if encrypted == nil {
		result, err = s.db.ExecContext(ctx, `UPDATE notification_channels SET name=?,kind=?,provider=?,enabled=?,updated_at=? WHERE id=?`, name, storageChannelKind(kind), kind, enabled, nowText(), id)
	} else {
		result, err = s.db.ExecContext(ctx, `UPDATE notification_channels SET name=?,kind=?,provider=?,config_encrypted=?,enabled=?,updated_at=? WHERE id=?`, name, storageChannelKind(kind), kind, *encrypted, enabled, nowText(), id)
	}
	if err != nil {
		return NotificationChannel{}, err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return NotificationChannel{}, ErrNotFound
	}
	return s.NotificationChannel(ctx, id)
}

func (s *Store) NotificationChannel(ctx context.Context, id string) (NotificationChannel, error) {
	var item NotificationChannel
	var enabled int
	var created, updated string
	var tested sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id,name,COALESCE(NULLIF(provider,''),kind),config_encrypted,enabled,last_test_status,last_test_error,last_test_at,created_at,updated_at FROM notification_channels WHERE id=?`, id).Scan(&item.ID, &item.Name, &item.Kind, &item.ConfigEncrypted, &enabled, &item.LastTestStatus, &item.LastTestError, &tested, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return NotificationChannel{}, ErrNotFound
	}
	if err != nil {
		return NotificationChannel{}, err
	}
	item.Enabled = enabled != 0
	item.CreatedAt, _ = parseTime(created)
	item.UpdatedAt, _ = parseTime(updated)
	if tested.Valid {
		value, _ := parseTime(tested.String)
		item.LastTestAt = &value
	}
	return item, nil
}

func (s *Store) ListNotificationChannels(ctx context.Context) ([]NotificationChannel, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,COALESCE(NULLIF(provider,''),kind),config_encrypted,enabled,last_test_status,last_test_error,last_test_at,created_at,updated_at FROM notification_channels ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]NotificationChannel, 0)
	for rows.Next() {
		var item NotificationChannel
		var enabled int
		var created, updated string
		var tested sql.NullString
		if err := rows.Scan(&item.ID, &item.Name, &item.Kind, &item.ConfigEncrypted, &enabled, &item.LastTestStatus, &item.LastTestError, &tested, &created, &updated); err != nil {
			return nil, err
		}
		item.Enabled = enabled != 0
		item.CreatedAt, _ = parseTime(created)
		item.UpdatedAt, _ = parseTime(updated)
		if tested.Valid {
			value, _ := parseTime(tested.String)
			item.LastTestAt = &value
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) RecordChannelTest(ctx context.Context, id string, success bool, message string, now time.Time) error {
	status := "success"
	if !success {
		status = "failed"
	}
	_, err := s.db.ExecContext(ctx, `UPDATE notification_channels SET last_test_status=?,last_test_error=?,last_test_at=?,updated_at=? WHERE id=?`, status, message, formatTime(now), formatTime(now), id)
	return err
}

func (s *Store) ListNotificationTemplates(ctx context.Context) ([]NotificationTemplate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,event_kind,title_template,body_template,is_default,created_at,updated_at FROM notification_templates ORDER BY is_default DESC,name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []NotificationTemplate{}
	for rows.Next() {
		var i NotificationTemplate
		var def int
		var created, updated string
		if err := rows.Scan(&i.ID, &i.Name, &i.EventKind, &i.TitleTemplate, &i.BodyTemplate, &def, &created, &updated); err != nil {
			return nil, err
		}
		i.IsDefault = def != 0
		i.CreatedAt, _ = parseTime(created)
		i.UpdatedAt, _ = parseTime(updated)
		items = append(items, i)
	}
	return items, rows.Err()
}

func (s *Store) SaveNotificationTemplate(ctx context.Context, id, name, eventKind, title, body string) (NotificationTemplate, error) {
	name = strings.TrimSpace(name)
	eventKind = strings.TrimSpace(eventKind)
	title = strings.TrimSpace(title)
	body = strings.TrimSpace(body)
	if name == "" || (eventKind != "firing" && eventKind != "resolved" && eventKind != "all") || title == "" || body == "" || len(title) > 300 || len(body) > 8000 || !validTemplateVariables(title+body) {
		return NotificationTemplate{}, errors.New("invalid notification template")
	}
	now := time.Now().UTC()
	if id == "" {
		id = randomID()
		_, err := s.db.ExecContext(ctx, `INSERT INTO notification_templates(id,name,event_kind,title_template,body_template,is_default,created_at,updated_at) VALUES(?,?,?,?,?,0,?,?)`, id, name, eventKind, title, body, formatTime(now), formatTime(now))
		if err != nil {
			return NotificationTemplate{}, err
		}
	} else {
		result, err := s.db.ExecContext(ctx, `UPDATE notification_templates SET name=?,event_kind=?,title_template=?,body_template=?,updated_at=? WHERE id=? AND is_default=0`, name, eventKind, title, body, formatTime(now), id)
		if err != nil {
			return NotificationTemplate{}, err
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return NotificationTemplate{}, ErrNotFound
		}
	}
	return s.NotificationTemplate(ctx, id)
}

var templateVariablePattern = regexp.MustCompile(`\{\{[^{}]+\}\}`)

func validTemplateVariables(value string) bool {
	allowed := map[string]bool{"{{title}}": true, "{{message}}": true, "{{state}}": true, "{{event}}": true, "{{event.type}}": true, "{{event.status}}": true, "{{event.time}}": true, "{{node.id}}": true, "{{node.name}}": true, "{{rule.id}}": true}
	for _, match := range templateVariablePattern.FindAllString(value, -1) {
		if !allowed[match] {
			return false
		}
	}
	return true
}

func (s *Store) NotificationTemplate(ctx context.Context, id string) (NotificationTemplate, error) {
	var i NotificationTemplate
	var def int
	var created, updated string
	err := s.db.QueryRowContext(ctx, `SELECT id,name,event_kind,title_template,body_template,is_default,created_at,updated_at FROM notification_templates WHERE id=?`, id).Scan(&i.ID, &i.Name, &i.EventKind, &i.TitleTemplate, &i.BodyTemplate, &def, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return i, ErrNotFound
	}
	if err != nil {
		return i, err
	}
	i.IsDefault = def != 0
	i.CreatedAt, _ = parseTime(created)
	i.UpdatedAt, _ = parseTime(updated)
	return i, nil
}
func (s *Store) DeleteNotificationTemplate(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM notification_templates WHERE id=? AND is_default=0`, id)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteNotificationChannel(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "notification_channels", id)
}

func (s *Store) CreateAlertRule(ctx context.Context, nodeID, channelID, kind string, config json.RawMessage, cooldown int) (AlertRule, error) {
	if nodeID == "" || channelID == "" || !validAlertKind(kind) || !json.Valid(config) || cooldown < 30 || cooldown > 86400*30 {
		return AlertRule{}, errors.New("invalid alert rule")
	}
	now := time.Now().UTC()
	item := AlertRule{ID: randomID(), NodeID: nodeID, ChannelID: channelID, Kind: kind, Config: append(json.RawMessage(nil), config...), Enabled: true, CooldownSeconds: cooldown, CreatedAt: now, UpdatedAt: now}
	result, err := s.db.ExecContext(ctx, `INSERT INTO alert_rules(id,node_id,channel_id,kind,config_json,enabled,cooldown_seconds,created_at,updated_at)
		SELECT ?,n.id,c.id,?,?,1,?,?,? FROM nodes n CROSS JOIN notification_channels c WHERE n.id=? AND c.id=?`, item.ID, item.Kind, string(item.Config), item.CooldownSeconds, formatTime(now), formatTime(now), nodeID, channelID)
	if err != nil {
		return AlertRule{}, err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return AlertRule{}, ErrNotFound
	}
	return item, nil
}

func (s *Store) UpdateAlertRule(ctx context.Context, id, nodeID, channelID, kind string, config json.RawMessage, enabled bool, cooldown int) (AlertRule, error) {
	if nodeID == "" || channelID == "" || !validAlertKind(kind) || !json.Valid(config) || cooldown < 30 || cooldown > 86400*30 {
		return AlertRule{}, errors.New("invalid alert rule")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE alert_rules SET node_id=?,channel_id=?,kind=?,config_json=?,enabled=?,cooldown_seconds=?,updated_at=?
		WHERE id=? AND EXISTS(SELECT 1 FROM nodes WHERE id=?) AND EXISTS(SELECT 1 FROM notification_channels WHERE id=?)`, nodeID, channelID, kind, string(config), enabled, cooldown, nowText(), id, nodeID, channelID)
	if err != nil {
		return AlertRule{}, err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return AlertRule{}, ErrNotFound
	}
	return s.AlertRule(ctx, id)
}

func (s *Store) AlertRule(ctx context.Context, id string) (AlertRule, error) {
	var item AlertRule
	var raw, created, updated string
	var enabled int
	err := s.db.QueryRowContext(ctx, `SELECT id,node_id,channel_id,kind,config_json,enabled,cooldown_seconds,created_at,updated_at FROM alert_rules WHERE id=?`, id).Scan(&item.ID, &item.NodeID, &item.ChannelID, &item.Kind, &raw, &enabled, &item.CooldownSeconds, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return AlertRule{}, ErrNotFound
	}
	if err != nil {
		return AlertRule{}, err
	}
	item.Config = json.RawMessage(raw)
	item.Enabled = enabled != 0
	item.CreatedAt, _ = parseTime(created)
	item.UpdatedAt, _ = parseTime(updated)
	return item, nil
}

func (s *Store) ListAlertRules(ctx context.Context) ([]AlertRule, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,node_id,channel_id,kind,config_json,enabled,cooldown_seconds,created_at,updated_at FROM alert_rules ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]AlertRule, 0)
	for rows.Next() {
		var item AlertRule
		var raw, created, updated string
		var enabled int
		if err := rows.Scan(&item.ID, &item.NodeID, &item.ChannelID, &item.Kind, &raw, &enabled, &item.CooldownSeconds, &created, &updated); err != nil {
			return nil, err
		}
		item.Config = json.RawMessage(raw)
		item.Enabled = enabled != 0
		item.CreatedAt, _ = parseTime(created)
		item.UpdatedAt, _ = parseTime(updated)
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) DeleteAlertRule(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "alert_rules", id)
}

func validAlertKind(kind string) bool {
	switch kind {
	case "offline", "cpu", "memory", "disk", "latency", "bandwidth", "cycle_traffic", "expiry":
		return true
	default:
		return false
	}
}

func (s *Store) LatestReport(ctx context.Context, nodeID string) (*protocol.Report, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT report_json FROM metric_latest WHERE node_id=?`, nodeID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var report protocol.Report
	if err := json.Unmarshal([]byte(raw), &report); err != nil {
		return nil, err
	}
	return &report, nil
}

func (s *Store) AlertState(ctx context.Context, fingerprint string) (AlertState, bool, error) {
	var item AlertState
	var active int
	var attempt, delivered, updated string
	var deliveredValue sql.NullString
	var pending sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT fingerprint,rule_id,node_id,active,last_message,last_attempt_at,last_delivered_at,last_error,updated_at,pending_since FROM alert_states WHERE fingerprint=?`, fingerprint).Scan(&item.Fingerprint, &item.RuleID, &item.NodeID, &active, &item.LastMessage, &attempt, &deliveredValue, &item.LastError, &updated, &pending)
	if errors.Is(err, sql.ErrNoRows) {
		return AlertState{}, false, nil
	}
	if err != nil {
		return AlertState{}, false, err
	}
	item.Active = active != 0
	item.LastAttemptAt, _ = parseTime(attempt)
	if deliveredValue.Valid {
		delivered = deliveredValue.String
		value, _ := parseTime(delivered)
		item.LastDeliveredAt = &value
	}
	item.UpdatedAt, _ = parseTime(updated)
	if pending.Valid {
		value, _ := parseTime(pending.String)
		item.PendingSince = &value
	}
	return item, true, nil
}

func (s *Store) SetAlertPending(ctx context.Context, ruleID, nodeID, fingerprint, message string, since *time.Time, now time.Time) error {
	var pending any
	if since != nil {
		pending = formatTime(*since)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO alert_states(fingerprint,rule_id,node_id,active,last_message,last_attempt_at,last_error,updated_at,pending_since)
		VALUES(?,?,?,0,?,?,'',?,?) ON CONFLICT(fingerprint) DO UPDATE SET last_message=excluded.last_message,updated_at=excluded.updated_at,pending_since=excluded.pending_since`,
		fingerprint, ruleID, nodeID, message, formatTime(now), formatTime(now), pending)
	return err
}

func (s *Store) RecordAlertAttempt(ctx context.Context, ruleID, nodeID, fingerprint, message string, active, delivered bool, deliveryError string, now time.Time) error {
	state := "failed"
	var deliveredAt any
	if delivered {
		if active {
			state = "firing"
		} else {
			state = "resolved"
		}
		deliveredAt = formatTime(now)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM alert_events WHERE created_at < ?`, formatTime(now.Add(-90*24*time.Hour))); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO alert_events(id,rule_id,node_id,state,fingerprint,message,delivery_error,created_at,delivered_at) VALUES(?,?,?,?,?,?,?,?,?)`, randomID(), ruleID, nodeID, state, fingerprint, message, deliveryError, formatTime(now), deliveredAt)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO alert_states(fingerprint,rule_id,node_id,active,last_message,last_attempt_at,last_delivered_at,last_error,updated_at,pending_since)
		VALUES(?,?,?,?,?,?,?,?,?,NULL) ON CONFLICT(fingerprint) DO UPDATE SET active=excluded.active,last_message=excluded.last_message,
		last_attempt_at=excluded.last_attempt_at,last_delivered_at=COALESCE(excluded.last_delivered_at,alert_states.last_delivered_at),
		last_error=excluded.last_error,updated_at=excluded.updated_at,pending_since=NULL`, fingerprint, ruleID, nodeID, active, message, formatTime(now), deliveredAt, deliveryError, formatTime(now))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListAlertEvents(ctx context.Context, limit int) ([]AlertEvent, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT e.id,e.rule_id,e.node_id,e.state,e.fingerprint,e.message,e.delivery_error,e.created_at,e.delivered_at,c.id,c.name,COALESCE(NULLIF(c.provider,''),c.kind)
		FROM alert_events e JOIN alert_rules r ON r.id=e.rule_id JOIN notification_channels c ON c.id=r.channel_id ORDER BY e.created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]AlertEvent, 0)
	for rows.Next() {
		var item AlertEvent
		var nodeID, delivered sql.NullString
		var created string
		if err := rows.Scan(&item.ID, &item.RuleID, &nodeID, &item.State, &item.Fingerprint, &item.Message, &item.DeliveryError, &created, &delivered, &item.ChannelID, &item.ChannelName, &item.Provider); err != nil {
			return nil, err
		}
		item.NodeID = nodeID.String
		item.CreatedAt, _ = parseTime(created)
		if delivered.Valid {
			value, _ := parseTime(delivered.String)
			item.DeliveredAt = &value
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
