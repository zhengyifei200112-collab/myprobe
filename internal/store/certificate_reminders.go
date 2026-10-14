package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
	"github.com/zhengyifei200112-collab/myprobe/internal/servicehealth"
)

type ServiceCertificateRuleConfig struct {
	ServiceRuleConfig
	ServiceUpdatedAt  time.Time `json:"service_updated_at"`
	CertificateSHA256 string    `json:"certificate_sha256"`
	DaysBefore        int       `json:"days_before"`
	NotBefore         time.Time `json:"not_before"`
	NotAfter          time.Time `json:"not_after"`
}
type CertificateAlertTarget struct {
	ServiceID                      string
	ServiceRevision, AlertRevision int64
}
type QualifiedCertificate struct {
	Certificate httpcheck.Certificate
	NodeID      string
}
type CertificatePool struct {
	ServiceRevision, AlertRevision int64
	Complete                       bool
	Certificates                   map[string]QualifiedCertificate
}
type certificateLedger struct {
	ServiceID, SHA256, RuleID   string
	DaysBefore                  int
	NotBefore, NotAfter         time.Time
	Queued, Authorized, Skipped bool
	IncidentID, DeliveryID      string
}

func (s *Store) ListCertificateAlertTargets(ctx context.Context) ([]CertificateAlertTarget, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT s.id,s.revision,a.revision FROM http_services s JOIN http_service_alerts a ON a.service_id=s.id WHERE s.enabled=1 AND a.certificate_reminders_enabled=1 ORDER BY s.id LIMIT 1001`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []CertificateAlertTarget{}
	for rows.Next() {
		var item CertificateAlertTarget
		if err := rows.Scan(&item.ServiceID, &item.ServiceRevision, &item.AlertRevision); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if len(items) > 1000 {
		return nil, errors.New("certificate service limit exceeded")
	}
	return items, rows.Err()
}

func certificatePool(snapshot HTTPHealthSnapshot, available map[string]bool, at time.Time) CertificatePool {
	pool := CertificatePool{ServiceRevision: snapshot.Service.Revision, Complete: len(snapshot.Observers) > 0, Certificates: map[string]QualifiedCertificate{}}
	conflicts := map[string]bool{}
	for _, o := range snapshot.Observers {
		input := servicehealth.Input{ServiceEnabled: snapshot.Service.Enabled, ExecutionEnabled: true, AgentAvailable: available[o.NodeID], LastSeenAt: o.LastSeenAt, ReportSeconds: o.ReportSeconds, Capabilities: o.Capabilities, IntervalSeconds: snapshot.Service.IntervalSeconds}
		if o.Observation != nil {
			input.Observation = &servicehealth.Observation{ScheduledAt: o.Observation.ScheduledAt, ExpiresAt: o.Observation.ExpiresAt, Result: o.Observation.Result}
		}
		a := servicehealth.Evaluate(input, at)
		if a.Status != "success" {
			pool.Complete = false
		}
		if a.Status == "unknown" {
			continue
		}
		for _, c := range o.Observation.Result.Certificates {
			c, valid := servicehealth.VerifiedCertificate(c, at)
			if !valid {
				pool.Complete = false
				continue
			}
			if old, exists := pool.Certificates[c.SHA256]; exists {
				if !old.Certificate.NotBefore.Equal(c.NotBefore) || !old.Certificate.NotAfter.Equal(c.NotAfter) {
					conflicts[c.SHA256] = true
					pool.Complete = false
				}
				continue
			}
			if !conflicts[c.SHA256] {
				pool.Certificates[c.SHA256] = QualifiedCertificate{Certificate: c, NodeID: o.NodeID}
			}
		}
	}
	for sha := range conflicts {
		delete(pool.Certificates, sha)
	}
	return pool
}

func loadCertificateLedgers(ctx context.Context, tx *sql.Tx, serviceID string) ([]certificateLedger, error) {
	rows, err := tx.QueryContext(ctx, `SELECT service_id,certificate_sha256,days_before,not_before,not_after,rule_id,queued_once,authorized_once,skipped,COALESCE(incident_id,''),COALESCE(delivery_id,'') FROM http_certificate_reminders WHERE service_id=? ORDER BY seq LIMIT 100001`, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []certificateLedger{}
	for rows.Next() {
		var item certificateLedger
		var before, after string
		if err := rows.Scan(&item.ServiceID, &item.SHA256, &item.DaysBefore, &before, &after, &item.RuleID, &item.Queued, &item.Authorized, &item.Skipped, &item.IncidentID, &item.DeliveryID); err != nil {
			return nil, err
		}
		if item.NotBefore, err = parseTime(before); err != nil {
			return nil, err
		}
		if item.NotAfter, err = parseTime(after); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if len(items) > 100000 {
		return nil, errors.New("certificate reminder limit exceeded")
	}
	return items, rows.Err()
}

func certificateConfig(service HTTPServiceSummary, alertRevision int64, record certificateLedger) ServiceCertificateRuleConfig {
	return ServiceCertificateRuleConfig{ServiceRuleConfig: ServiceRuleConfig{ServiceID: service.ID, ServiceName: service.Name, ServiceRevision: service.Revision, AlertRevision: alertRevision, RepeatSeconds: 2592000}, ServiceUpdatedAt: service.UpdatedAt, CertificateSHA256: record.SHA256, DaysBefore: record.DaysBefore, NotBefore: record.NotBefore, NotAfter: record.NotAfter}
}

func upsertCertificateRule(ctx context.Context, tx *sql.Tx, record certificateLedger, config ServiceCertificateRuleConfig, nodeID, channelID string, enabled bool, at time.Time) error {
	raw, err := json.Marshal(config)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO alert_rules(id,node_id,channel_id,kind,config_json,enabled,cooldown_seconds,created_at,updated_at) VALUES(?,?,?,'service_certificate',?,?,2592000,?,?) ON CONFLICT(id) DO UPDATE SET node_id=excluded.node_id,channel_id=excluded.channel_id,config_json=excluded.config_json,enabled=excluded.enabled,updated_at=excluded.updated_at WHERE alert_rules.node_id<>excluded.node_id OR alert_rules.channel_id<>excluded.channel_id OR alert_rules.config_json<>excluded.config_json OR alert_rules.enabled<>excluded.enabled`, record.RuleID, nodeID, channelID, string(raw), enabled, formatTime(at), formatTime(at))
	return err
}

// synchronizeCertificateDefinitions updates existing owned rules even when HTTP
// execution is disabled; no stale definition retains send authority.
func synchronizeCertificateDefinitions(ctx context.Context, tx *sql.Tx, at time.Time) error {
	rows, err := tx.QueryContext(ctx, `SELECT l.service_id,l.certificate_sha256,l.days_before,l.not_before,l.not_after,l.rule_id,l.skipped,r.node_id,COALESCE(s.name,''),COALESCE(s.revision,0),COALESCE(s.enabled,0),COALESCE(a.revision,0),COALESCE(a.channel_id,r.channel_id),COALESCE(a.certificate_reminders_enabled,0),EXISTS(SELECT 1 FROM http_service_nodes n WHERE n.service_id=l.service_id AND n.node_id=r.node_id),COALESCE(s.updated_at,'') FROM http_certificate_reminders l JOIN alert_rules r ON r.id=l.rule_id LEFT JOIN http_services s ON s.id=l.service_id LEFT JOIN http_service_alerts a ON a.service_id=s.id LIMIT 100001`)
	if err != nil {
		return err
	}
	type selected struct {
		record                                     certificateLedger
		service                                    HTTPServiceSummary
		node, channel                              string
		alertRevision                              int64
		serviceEnabled, remindersEnabled, assigned bool
	}
	var items []selected
	for rows.Next() {
		var v selected
		var before, after, updated string
		if err := rows.Scan(&v.record.ServiceID, &v.record.SHA256, &v.record.DaysBefore, &before, &after, &v.record.RuleID, &v.record.Skipped, &v.node, &v.service.Name, &v.service.Revision, &v.serviceEnabled, &v.alertRevision, &v.channel, &v.remindersEnabled, &v.assigned, &updated); err != nil {
			rows.Close()
			return err
		}
		v.service.ID = v.record.ServiceID
		if updated != "" {
			if v.service.UpdatedAt, err = parseTime(updated); err != nil {
				rows.Close()
				return err
			}
		}
		if v.record.NotBefore, err = parseTime(before); err != nil {
			rows.Close()
			return err
		}
		if v.record.NotAfter, err = parseTime(after); err != nil {
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
		return errors.New("certificate rule limit exceeded")
	}
	for _, v := range items {
		enabled := v.serviceEnabled && v.remindersEnabled && v.assigned && !v.record.Skipped
		if err := upsertCertificateRule(ctx, tx, v.record, certificateConfig(v.service, v.alertRevision, v.record), v.node, v.channel, enabled, at); err != nil {
			return err
		}
	}
	return nil
}

// PrepareCertificateReminders validates latest admitted observations inside the
// writer transaction. The supplied map captures current Gateway availability;
// service/alert versions and assignments are independently fenced here.
func (s *Store) PrepareCertificateReminders(ctx context.Context, target CertificateAlertTarget, available map[string]bool, at time.Time) (*CertificatePool, error) {
	if at.IsZero() {
		return nil, ErrInvalidHTTPService
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE alert_policy_writer SET version=version WHERE id=1`); err != nil {
		return nil, err
	}
	snapshots, err := readHTTPHealthSnapshots(ctx, tx, []string{target.ServiceID}, at)
	if err != nil {
		return nil, err
	}
	if len(snapshots) != 1 || !snapshots[0].Service.Enabled || snapshots[0].Service.Revision != target.ServiceRevision {
		return nil, ErrObservationObsolete
	}
	var revision int64
	var channel string
	var enabled bool
	if err = tx.QueryRowContext(ctx, `SELECT revision,channel_id,certificate_reminders_enabled FROM http_service_alerts WHERE service_id=?`, target.ServiceID).Scan(&revision, &channel, &enabled); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrObservationObsolete
	} else if err != nil {
		return nil, err
	}
	if !enabled || revision != target.AlertRevision {
		return nil, ErrObservationObsolete
	}
	pool := certificatePool(snapshots[0], available, at)
	pool.AlertRevision = revision
	records, err := loadCertificateLedgers(ctx, tx, target.ServiceID)
	if err != nil {
		return nil, err
	}
	byKey := map[string]certificateLedger{}
	for _, record := range records {
		byKey[certificateLedgerKey(record.SHA256, record.DaysBefore)] = record
		if candidate, ok := pool.Certificates[record.SHA256]; ok && (!candidate.Certificate.NotBefore.Equal(record.NotBefore) || !candidate.Certificate.NotAfter.Equal(record.NotAfter)) {
			delete(pool.Certificates, record.SHA256)
			pool.Complete = false
		}
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM http_certificate_reminders`).Scan(&count); err != nil {
		return nil, err
	}
	hashes := make([]string, 0, len(pool.Certificates))
	for sha := range pool.Certificates {
		hashes = append(hashes, sha)
	}
	sort.Strings(hashes)
	for _, sha := range hashes {
		candidate := pool.Certificates[sha]
		due := servicehealth.CertificateDueDays(candidate.Certificate.NotAfter, at)
		if due == 0 {
			continue
		}
		for _, days := range []int{14, 7, 1} {
			if days < due {
				continue
			}
			key := certificateLedgerKey(sha, days)
			record, exists := byKey[key]
			if !exists {
				count++
				if count > 100000 {
					return nil, errors.New("certificate reminder limit exceeded")
				}
				record = certificateLedger{ServiceID: target.ServiceID, SHA256: sha, DaysBefore: days, NotBefore: candidate.Certificate.NotBefore, NotAfter: candidate.Certificate.NotAfter, RuleID: randomID(), Skipped: days > due}
				if _, err := tx.ExecContext(ctx, `INSERT INTO http_certificate_reminders(service_id,certificate_sha256,days_before,not_before,not_after,rule_id,skipped,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, record.ServiceID, record.SHA256, days, formatTime(record.NotBefore), formatTime(record.NotAfter), record.RuleID, record.Skipped, formatTime(at), formatTime(at)); err != nil {
					return nil, err
				}
				records = append(records, record)
				byKey[key] = record
			}
			if days != due || record.Skipped {
				continue
			}
			var node string
			err := tx.QueryRowContext(ctx, `SELECT r.node_id FROM alert_rules r JOIN http_service_nodes n ON n.node_id=r.node_id AND n.service_id=? WHERE r.id=?`, target.ServiceID, record.RuleID).Scan(&node)
			if errors.Is(err, sql.ErrNoRows) {
				if record.Authorized {
					continue
				}
				node = candidate.NodeID
			} else if err != nil {
				return nil, err
			}
			if err := upsertCertificateRule(ctx, tx, record, certificateConfig(snapshots[0].Service, revision, record), node, channel, true, at); err != nil {
				return nil, err
			}
		}
	}
	for _, record := range records {
		if record.Skipped {
			continue
		}
		reason := ""
		if !record.NotAfter.After(at) {
			reason = "certificate_expired"
		} else if candidate, ok := pool.Certificates[record.SHA256]; ok {
			if due := servicehealth.CertificateDueDays(candidate.Certificate.NotAfter, at); due > 0 && due < record.DaysBefore {
				reason = "certificate_threshold_advanced"
			}
		} else if pool.Complete {
			reason = "certificate_no_longer_observed"
		}
		if reason != "" {
			if err := endCertificateSubject(ctx, tx, record.RuleID, reason, at); err != nil {
				return nil, err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE http_certificate_reminders SET skipped=1,updated_at=? WHERE rule_id=?`, formatTime(at), record.RuleID); err != nil {
				return nil, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &pool, nil
}

func certificateLedgerKey(sha string, days int) string {
	switch days {
	case 14:
		return sha + ":14"
	case 7:
		return sha + ":7"
	default:
		return sha + ":1"
	}
}

func endCertificateSubject(ctx context.Context, tx *sql.Tx, ruleID, reason string, at time.Time) error {
	i, err := scanIncident(tx.QueryRowContext(ctx, "SELECT "+incidentColumns+" FROM alert_incidents WHERE rule_id=? AND state IN ('pending','firing') LIMIT 1", ruleID))
	if err == nil {
		if at.Before(i.UpdatedAt) {
			at = i.UpdatedAt
		}
		if err := endIncident(ctx, tx, &i, reason, at); err != nil {
			return err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE alert_rules SET enabled=0,updated_at=? WHERE id=? AND enabled<>0`, formatTime(at), ruleID)
	return err
}

func certificateRuleCurrent(ctx context.Context, tx *sql.Tx, rule AlertRule, at time.Time) (bool, error) {
	var cfg ServiceCertificateRuleConfig
	if json.Unmarshal(rule.Config, &cfg) != nil || servicehealth.CertificateDueDays(cfg.NotAfter, at) != cfg.DaysBefore {
		return false, nil
	}
	var current bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM http_certificate_reminders l JOIN http_services s ON s.id=l.service_id JOIN http_service_alerts a ON a.service_id=s.id JOIN http_service_nodes n ON n.service_id=s.id WHERE l.rule_id=? AND l.certificate_sha256=? AND l.days_before=? AND l.skipped=0 AND l.not_before=? AND l.not_after=? AND s.enabled=1 AND s.revision=? AND s.updated_at=? AND a.certificate_reminders_enabled=1 AND a.revision=? AND a.channel_id=? AND n.node_id=?)`, rule.ID, cfg.CertificateSHA256, cfg.DaysBefore, formatTime(cfg.NotBefore), formatTime(cfg.NotAfter), cfg.ServiceRevision, formatTime(cfg.ServiceUpdatedAt), cfg.AlertRevision, rule.ChannelID, rule.NodeID).Scan(&current)
	return current, err
}

func certificateCanQueue(ctx context.Context, tx *sql.Tx, ruleID string) (bool, error) {
	var queued, authorized, skipped bool
	var last sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT queued_once,authorized_once,skipped,delivery_id FROM http_certificate_reminders WHERE rule_id=?`, ruleID).Scan(&queued, &authorized, &skipped, &last)
	if err != nil {
		return false, err
	}
	if authorized || skipped {
		return false, nil
	}
	if !queued {
		return true, nil
	}
	var status string
	if err = tx.QueryRowContext(ctx, `SELECT status FROM notification_deliveries WHERE id=?`, last.String).Scan(&status); errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return status == "canceled", err
}
