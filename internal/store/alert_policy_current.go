package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/zhengyifei200112-collab/myprobe/internal/alertpolicy"
)

// policyRuleCurrent checks definitions and node tags in the caller's transaction,
// not just the materialized rule, which may await the next synchronization.
func policyRuleCurrent(ctx context.Context, tx *sql.Tx, rule AlertRule) (bool, error) {
	var policyID, nodeID string
	err := tx.QueryRowContext(ctx, `SELECT policy_id,node_id FROM alert_policy_rule_bindings WHERE rule_id=?`, rule.ID).Scan(&policyID, &nodeID)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil // Unbound legacy rules retain their existing semantics.
	}
	if err != nil {
		return false, err
	}
	if nodeID != rule.NodeID {
		return false, nil
	}
	var raw string
	err = tx.QueryRowContext(ctx, `SELECT tags_json FROM nodes WHERE id=?`, nodeID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	node := alertpolicy.Node{ID: nodeID}
	if err = json.Unmarshal([]byte(raw), &node.Tags); err != nil {
		return false, err
	}
	items, err := listAlertPolicies(ctx, tx)
	if err != nil {
		return false, err
	}
	policies := make([]alertpolicy.Policy, 0, len(items))
	var owner *AlertPolicy
	for index := range items {
		policies = append(policies, items[index].Policy)
		if items[index].ID == policyID {
			owner = &items[index]
		}
	}
	if owner == nil || !owner.Enabled {
		return false, nil
	}
	expected := AlertRule{NodeID: nodeID, ChannelID: owner.ChannelID, Kind: owner.Kind, Config: owner.Config, CooldownSeconds: owner.CooldownSeconds}
	if !sameSnapshot(snapshotRule(rule), snapshotRule(expected)) {
		return false, nil
	}
	decisions, err := alertpolicy.Resolve(policies, node)
	if err != nil {
		return false, err
	}
	for _, decision := range decisions {
		if decision.SelectedID == policyID {
			return true, nil
		}
	}
	return false, nil
}
