package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
)

type ServiceAlertSettings struct {
	CertificateRemindersEnabled bool   `json:"certificate_reminders_enabled"`
	ServiceRevision             int64  `json:"service_revision"`
	ServiceID                   string `json:"service_id"`
	Revision                    int64  `json:"revision"`
	ChannelID                   string `json:"channel_id"`
	Enabled                     bool   `json:"enabled"`
	DurationSeconds             int    `json:"duration_seconds"`
	RecoverySeconds             int    `json:"recovery_seconds"`
	RepeatSeconds               int    `json:"repeat_seconds"`
	CooldownSeconds             int    `json:"cooldown_seconds"`
}
type ServiceRuleConfig struct {
	ServiceID       string `json:"service_id"`
	ServiceName     string `json:"service_name"`
	ServiceRevision int64  `json:"service_revision"`
	AlertRevision   int64  `json:"alert_revision"`
	DurationSeconds int    `json:"duration_seconds"`
	RecoverySeconds int    `json:"recovery_seconds"`
	RepeatSeconds   int    `json:"repeat_seconds"`
}

func (s *Store) ServiceAlertSettings(ctx context.Context, id string) (ServiceAlertSettings, error) {
	v := ServiceAlertSettings{ServiceID: id, CooldownSeconds: 300}
	// Read both revisions from one snapshot, including the disabled default.
	err := s.db.QueryRowContext(ctx, `SELECT s.revision,COALESCE(a.revision,0),COALESCE(a.channel_id,''),COALESCE(a.enabled,0),COALESCE(a.duration_seconds,0),COALESCE(a.recovery_seconds,0),COALESCE(a.repeat_seconds,0),COALESCE(a.cooldown_seconds,300),COALESCE(a.certificate_reminders_enabled,0) FROM http_services s LEFT JOIN http_service_alerts a ON a.service_id=s.id WHERE s.id=?`, id).Scan(&v.ServiceRevision, &v.Revision, &v.ChannelID, &v.Enabled, &v.DurationSeconds, &v.RecoverySeconds, &v.RepeatSeconds, &v.CooldownSeconds, &v.CertificateRemindersEnabled)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	return v, err
}
func (s *Store) SaveServiceAlertSettings(ctx context.Context, v ServiceAlertSettings) (ServiceAlertSettings, error) {
	if v.ServiceID == "" || len(v.ServiceID) > 128 || v.ServiceRevision < 1 || v.ChannelID == "" || len(v.ChannelID) > 128 || v.Revision < 0 || v.Revision >= 1<<63-1 || v.DurationSeconds < 0 || v.DurationSeconds > 2592000 || v.RecoverySeconds < 0 || v.RecoverySeconds > 2592000 || v.RepeatSeconds < 0 || (v.RepeatSeconds > 0 && v.RepeatSeconds < 30) || v.RepeatSeconds > 2592000 || v.CooldownSeconds < 30 || v.CooldownSeconds > 2592000 {
		return v, ErrInvalidHTTPService
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return v, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE alert_policy_writer SET version=version WHERE id=1`); err != nil {
		return v, err
	}
	var valid bool
	var currentRevision int64
	if err = tx.QueryRowContext(ctx, `SELECT revision FROM http_services WHERE id=?`, v.ServiceID).Scan(&currentRevision); errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	} else if err != nil {
		return v, err
	}
	if currentRevision != v.ServiceRevision {
		return v, ErrServiceConflict
	}
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM notification_channels WHERE id=?)`, v.ChannelID).Scan(&valid); err != nil {
		return v, err
	}
	if !valid {
		return v, ErrInvalidHTTPService
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO http_service_alerts(service_id,revision,channel_id,enabled,duration_seconds,recovery_seconds,repeat_seconds,cooldown_seconds,updated_at,certificate_reminders_enabled) VALUES(?,1,?,?,?,?,?,?,?,?) ON CONFLICT(service_id) DO UPDATE SET revision=revision+1,channel_id=excluded.channel_id,enabled=excluded.enabled,duration_seconds=excluded.duration_seconds,recovery_seconds=excluded.recovery_seconds,repeat_seconds=excluded.repeat_seconds,cooldown_seconds=excluded.cooldown_seconds,certificate_reminders_enabled=excluded.certificate_reminders_enabled,updated_at=excluded.updated_at WHERE http_service_alerts.revision=?`, v.ServiceID, v.ChannelID, v.Enabled, v.DurationSeconds, v.RecoverySeconds, v.RepeatSeconds, v.CooldownSeconds, nowText(), v.CertificateRemindersEnabled, v.Revision)
	if err != nil {
		return v, err
	}
	if n, err := result.RowsAffected(); err != nil {
		return v, err
	} else if n != 1 {
		return v, ErrServiceConflict
	}
	// Creation with a stale positive revision must not manufacture a new setting.
	if v.Revision > 0 {
		var revision int64
		if err = tx.QueryRowContext(ctx, `SELECT revision FROM http_service_alerts WHERE service_id=?`, v.ServiceID).Scan(&revision); err != nil {
			return v, err
		}
		if revision != v.Revision+1 {
			return v, ErrServiceConflict
		}
	}
	if err = tx.Commit(); err != nil {
		return v, err
	}
	v.Revision++
	return v, nil
}

func syncServiceAlertRules(ctx context.Context, tx *sql.Tx, now time.Time) error {
	type selected struct {
		id, node, channel, name              string
		serviceRev, alertRev                 int64
		duration, recovery, repeat, cooldown int
	}
	rows, err := tx.QueryContext(ctx, `SELECT s.id,a.node_id,c.channel_id,s.name,s.revision,c.revision,c.duration_seconds,c.recovery_seconds,c.repeat_seconds,c.cooldown_seconds FROM http_services s JOIN http_service_nodes a ON a.service_id=s.id JOIN http_service_alerts c ON c.service_id=s.id WHERE s.enabled=1 AND c.enabled=1 ORDER BY s.id,a.node_id LIMIT 100001`)
	if err != nil {
		return err
	}
	var items []selected
	for rows.Next() {
		var v selected
		if err := rows.Scan(&v.id, &v.node, &v.channel, &v.name, &v.serviceRev, &v.alertRev, &v.duration, &v.recovery, &v.repeat, &v.cooldown); err != nil {
			rows.Close()
			return err
		}
		items = append(items, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(items) > 100000 {
		return errors.New("service alert rule limit exceeded")
	}
	// Disable retained snapshot bindings first; select the current assignments only.
	var bindingCount int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM http_service_rule_bindings`).Scan(&bindingCount); err != nil {
		return err
	}
	if bindingCount > 100000 {
		return errors.New("service alert binding limit exceeded")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE alert_rules SET enabled=0,updated_at=? WHERE enabled<>0 AND id IN (SELECT b.rule_id FROM http_service_rule_bindings b WHERE NOT EXISTS(SELECT 1 FROM http_services s JOIN http_service_nodes n ON n.service_id=s.id JOIN http_service_alerts a ON a.service_id=s.id WHERE s.id=b.service_id AND n.node_id=b.node_id AND s.enabled=1 AND a.enabled=1))`, formatTime(now)); err != nil {
		return err
	}
	for _, v := range items {
		var id string
		err := tx.QueryRowContext(ctx, `SELECT rule_id FROM http_service_rule_bindings WHERE service_id=? AND node_id=?`, v.id, v.node).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			bindingCount++
			if bindingCount > 100000 {
				return errors.New("service alert binding limit exceeded")
			}
			id = randomID()
			if _, err = tx.ExecContext(ctx, `INSERT INTO http_service_rule_bindings(service_id,node_id,rule_id) VALUES(?,?,?)`, v.id, v.node, id); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		raw, err := json.Marshal(ServiceRuleConfig{v.id, v.name, v.serviceRev, v.alertRev, v.duration, v.recovery, v.repeat})
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO alert_rules(id,node_id,channel_id,kind,config_json,enabled,cooldown_seconds,created_at,updated_at) VALUES(?,?,?,'service_http',?,1,?,?,?) ON CONFLICT(id) DO UPDATE SET channel_id=excluded.channel_id,config_json=excluded.config_json,enabled=1,cooldown_seconds=excluded.cooldown_seconds,updated_at=excluded.updated_at WHERE alert_rules.channel_id<>excluded.channel_id OR alert_rules.config_json<>excluded.config_json OR alert_rules.enabled<>1 OR alert_rules.cooldown_seconds<>excluded.cooldown_seconds`, id, v.node, v.channel, string(raw), v.cooldown, formatTime(now), formatTime(now))
		if err != nil {
			return err
		}
	}
	return nil
}

func serviceRuleCurrent(ctx context.Context, tx *sql.Tx, rule AlertRule) (bool, error) {
	var config ServiceRuleConfig
	if json.Unmarshal(rule.Config, &config) != nil || config.ServiceID == "" || config.ServiceRevision < 1 || config.AlertRevision < 1 {
		return false, nil
	}
	var current bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM http_services s JOIN http_service_nodes n ON n.service_id=s.id JOIN http_service_alerts a ON a.service_id=s.id JOIN http_service_rule_bindings b ON b.service_id=s.id AND b.node_id=n.node_id WHERE s.id=? AND s.revision=? AND s.enabled=1 AND a.enabled=1 AND a.revision=? AND a.channel_id=? AND n.node_id=? AND b.rule_id=?)`, config.ServiceID, config.ServiceRevision, config.AlertRevision, rule.ChannelID, rule.NodeID, rule.ID).Scan(&current)
	return current, err
}

// Latest current-revision planned task, including missing/unobserved results.
func (s *Store) LatestServiceAlertObservation(ctx context.Context, serviceID, nodeID string, revision int64, now time.Time) (*HTTPObservation, int, error) {
	var interval int
	if err := s.db.QueryRowContext(ctx, `SELECT interval_seconds FROM http_services WHERE id=? AND revision=? AND enabled=1`, serviceID, revision).Scan(&interval); errors.Is(err, sql.ErrNoRows) {
		return nil, 0, nil
	} else if err != nil {
		return nil, 0, err
	}
	const key = `substr(substr(scheduled_at,1,19)||'.'||rtrim(substr(scheduled_at,21),'Z')||'000000000',1,29)`
	v := &HTTPObservation{NodeID: nodeID, Revision: revision}
	var scheduled, expires string
	var raw sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id,scheduled_at,expires_at,result_json FROM http_tasks WHERE service_id=? AND node_id=? AND revision=? AND `+key+`<=? ORDER BY `+key+` DESC,id DESC LIMIT 1`, serviceID, nodeID, revision, now.UTC().Format("2006-01-02T15:04:05.000000000")).Scan(&v.TaskID, &scheduled, &expires, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, interval, nil
	}
	if err != nil {
		return nil, interval, err
	}
	v.ScheduledAt, err = parseTime(scheduled)
	if err != nil {
		return nil, interval, err
	}
	v.ExpiresAt, err = parseTime(expires)
	if err != nil {
		return nil, interval, err
	}
	if raw.Valid {
		v.Result = &httpcheck.Result{}
		if err = json.Unmarshal([]byte(raw.String), v.Result); err != nil {
			return nil, interval, err
		}
	}
	return v, interval, nil
}
