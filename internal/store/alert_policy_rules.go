package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/alertpolicy"
)

// SyncAlertPolicyRules materializes the current selection under one writer
// transaction. Runtime must coordinate legacy writes before enabling this path.
func (s *Store) SyncAlertPolicyRules(ctx context.Context, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE alert_policy_writer SET version=version WHERE id=1`); err != nil {
		return err
	}
	items, err := listAlertPolicies(ctx, tx)
	if err != nil {
		return err
	}
	policies := make([]alertpolicy.Policy, 0, len(items))
	byID := make(map[string]AlertPolicy, len(items))
	for _, p := range items {
		policies = append(policies, p.Policy)
		byID[p.ID] = p
	}
	if err = alertpolicy.ValidateSet(policies); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,tags_json FROM nodes ORDER BY id LIMIT 10001`)
	if err != nil {
		return err
	}
	var nodes []alertpolicy.Node
	for rows.Next() {
		var n alertpolicy.Node
		var raw string
		if err = rows.Scan(&n.ID, &raw); err != nil {
			rows.Close()
			return err
		}
		if err = json.Unmarshal([]byte(raw), &n.Tags); err != nil {
			rows.Close()
			return err
		}
		nodes = append(nodes, n)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if len(nodes) > 10000 {
		return errors.New("policy synchronization node limit exceeded")
	}
	type key struct{ policy, node string }
	selected := make(map[key]bool)
	for _, node := range nodes {
		decisions, err := alertpolicy.Resolve(policies, node)
		if err != nil {
			return err
		}
		for _, d := range decisions {
			selected[key{d.SelectedID, node.ID}] = true
		}
		if len(selected) > 100000 {
			return errors.New("policy synchronization rule limit exceeded")
		}
	}
	rows, err = tx.QueryContext(ctx, `SELECT policy_id,node_id,rule_id FROM alert_policy_rule_bindings LIMIT 100001`)
	if err != nil {
		return err
	}
	bindings := make(map[key]string)
	for rows.Next() {
		var k key
		var id string
		if err = rows.Scan(&k.policy, &k.node, &id); err != nil {
			rows.Close()
			return err
		}
		bindings[k] = id
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if len(bindings) > 100000 {
		return errors.New("policy binding limit exceeded")
	}
	newBindings := 0
	for k := range selected {
		if bindings[k] == "" {
			newBindings++
		}
	}
	if len(bindings)+newBindings > 100000 {
		return errors.New("policy binding limit exceeded")
	}
	stamp := formatTime(now.UTC())
	for k, id := range bindings {
		if selected[k] {
			continue
		}
		if _, err = tx.ExecContext(ctx, `UPDATE alert_rules SET enabled=0,updated_at=? WHERE id=? AND enabled<>0`, stamp, id); err != nil {
			return err
		}
	}
	for k := range selected {
		p := byID[k.policy]
		id := bindings[k]
		if id == "" {
			id = randomID()
			if _, err = tx.ExecContext(ctx, `INSERT INTO alert_policy_rule_bindings(policy_id,node_id,rule_id,origin) VALUES(?,?,?,'materialized')`, k.policy, k.node, id); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO alert_rules(id,node_id,channel_id,kind,config_json,enabled,cooldown_seconds,created_at,updated_at)
		VALUES(?,?,?,?,?,1,?,?,?) ON CONFLICT(id) DO UPDATE SET channel_id=excluded.channel_id,kind=excluded.kind,config_json=excluded.config_json,enabled=1,cooldown_seconds=excluded.cooldown_seconds,updated_at=excluded.updated_at
		WHERE alert_rules.node_id=excluded.node_id AND (alert_rules.channel_id<>excluded.channel_id OR alert_rules.kind<>excluded.kind OR alert_rules.config_json<>excluded.config_json OR alert_rules.enabled<>1 OR alert_rules.cooldown_seconds<>excluded.cooldown_seconds)`, id, k.node, p.ChannelID, p.Kind, string(p.Config), p.CooldownSeconds, stamp, stamp)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
