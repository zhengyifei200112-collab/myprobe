package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/zhengyifei200112-collab/myprobe/internal/alertpolicy"
)

// MigrateLegacyAlertPolicies prepares an idempotent identity-preserving mapping.
// It does not switch evaluation authority or modify rules/incidents/deliveries.
// Runtime activation must call this in its coordinated cutover before enabling
// policy writes/evaluation; it is deliberately not invoked by schema migration.
func (s *Store) MigrateLegacyAlertPolicies(ctx context.Context) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE alert_policy_writer SET version=version WHERE id=1`); err != nil {
		return 0, err
	}
	count, err := migrateLegacyAlertPolicies(ctx, tx)
	if err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}

func migrateLegacyAlertPolicies(ctx context.Context, tx *sql.Tx) (int, error) {
	items, err := listAlertPolicies(ctx, tx)
	if err != nil {
		return 0, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,node_id,channel_id,kind,config_json,enabled,cooldown_seconds,created_at,updated_at FROM alert_rules WHERE NOT EXISTS(SELECT 1 FROM alert_policy_rule_bindings b WHERE b.rule_id=alert_rules.id) ORDER BY id LIMIT 1001`)
	if err != nil {
		return 0, err
	}
	var legacy []AlertPolicy
	for rows.Next() {
		var p AlertPolicy
		var nodeID, raw, created, updated string
		if err = rows.Scan(&p.ID, &nodeID, &p.ChannelID, &p.Kind, &raw, &p.Enabled, &p.CooldownSeconds, &created, &updated); err != nil {
			rows.Close()
			return 0, err
		}
		p.Key = "legacy:" + p.ID
		p.Name = "Legacy " + p.Kind + " " + p.ID
		p.Revision = 1
		p.Scope = alertpolicy.Scope{Kind: "nodes", NodeIDs: []string{nodeID}}
		p.Config = json.RawMessage(raw)
		if p.CreatedAt, err = parseTime(created); err != nil {
			rows.Close()
			return 0, err
		}
		if p.UpdatedAt, err = parseTime(updated); err != nil {
			rows.Close()
			return 0, err
		}
		legacy = append(legacy, p)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	if len(items)+len(legacy) > 1000 {
		return 0, errors.New("legacy policy migration exceeds the 1000-policy limit; no changes applied")
	}
	policies := make([]alertpolicy.Policy, 0, len(items)+len(legacy))
	for _, p := range items {
		policies = append(policies, p.Policy)
	}
	for _, p := range legacy {
		policies = append(policies, p.Policy)
	}
	if err = alertpolicy.ValidateSet(policies); err != nil {
		return 0, ErrAlertPolicyConflict
	}
	for _, p := range legacy {
		raw, err := json.Marshal(p)
		if err != nil {
			return 0, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO alert_policies(id,revision,channel_id,definition_json,created_at,updated_at) VALUES(?,1,?,?,?,?)`, p.ID, p.ChannelID, string(raw), formatTime(p.CreatedAt), formatTime(p.UpdatedAt)); err != nil {
			return 0, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO alert_policy_rule_bindings(policy_id,node_id,rule_id,origin) VALUES(?,?,?,'legacy')`, p.ID, p.Scope.NodeIDs[0], p.ID); err != nil {
			return 0, err
		}
	}
	return len(legacy), nil
}
