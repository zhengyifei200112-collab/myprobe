package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/alertpolicy"
)

func TestLegacyPolicyMappingPreservesIncidentAndDelivery(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "mapping.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	node, _, err := db.CreateNode(ctx, CreateNodeParams{Name: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	channel, err := db.CreateNotificationChannel(ctx, "fixture", ChannelKindWebhook, "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	rule, err := db.CreateAlertRule(ctx, node.ID, channel.ID, "cpu", json.RawMessage(`{"threshold_percent":80,"recovery_seconds":0}`), 900)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err = db.ObserveAlert(ctx, rule, node, AlertObservation{At: now, Known: true, Active: true, Message: "fixture", NotificationTitle: "fixture", NotificationMessage: "fixture"}); err != nil {
		t.Fatal(err)
	}
	var incidentID, fingerprint, snapshot, deliveryID, payload string
	if err = db.db.QueryRowContext(ctx, `SELECT id,fingerprint,rule_snapshot_json FROM alert_incidents WHERE rule_id=?`, rule.ID).Scan(&incidentID, &fingerprint, &snapshot); err != nil {
		t.Fatal(err)
	}
	if err = db.db.QueryRowContext(ctx, `SELECT id,payload_json FROM notification_deliveries WHERE incident_id=?`, incidentID).Scan(&deliveryID, &payload); err != nil {
		t.Fatal(err)
	}
	count, err := db.MigrateLegacyAlertPolicies(ctx)
	if err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	items, err := db.ListAlertPolicies(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("%d %v", len(items), err)
	}
	p := items[0]
	if p.ID != rule.ID || p.Key != "legacy:"+rule.ID || p.Scope.NodeIDs[0] != node.ID || string(p.Config) != string(rule.Config) || p.ChannelID != rule.ChannelID {
		t.Fatalf("mapping changed rule: %+v", p)
	}
	if err = db.SyncAlertPolicyRules(ctx, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	unchanged, err := db.AlertRule(ctx, rule.ID)
	if err != nil || !unchanged.UpdatedAt.Equal(rule.UpdatedAt) {
		t.Fatalf("equivalent policy rewrote rule: %+v %v", unchanged, err)
	}
	var afterID, afterFingerprint, afterSnapshot, afterDeliveryID, afterPayload string
	if err = db.db.QueryRowContext(ctx, `SELECT id,fingerprint,rule_snapshot_json FROM alert_incidents WHERE rule_id=?`, rule.ID).Scan(&afterID, &afterFingerprint, &afterSnapshot); err != nil {
		t.Fatal(err)
	}
	if err = db.db.QueryRowContext(ctx, `SELECT id,payload_json FROM notification_deliveries WHERE incident_id=?`, incidentID).Scan(&afterDeliveryID, &afterPayload); err != nil {
		t.Fatal(err)
	}
	if incidentID != afterID || fingerprint != afterFingerprint || snapshot != afterSnapshot || deliveryID != afterDeliveryID || payload != afterPayload {
		t.Fatal("migration changed fault or delivery identity")
	}
	if count, err = db.MigrateLegacyAlertPolicies(ctx); err != nil || count != 0 {
		t.Fatalf("non-idempotent migration %d %v", count, err)
	}
	if err = db.ReconcileIncidents(ctx, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var state string
	if err = db.db.QueryRowContext(ctx, `SELECT state FROM alert_incidents WHERE id=?`, incidentID).Scan(&state); err != nil || state != "firing" {
		t.Fatalf("mapping resolved incident: %s %v", state, err)
	}
	var deliveries int
	if err = db.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notification_deliveries`).Scan(&deliveries); err != nil || deliveries != 1 {
		t.Fatalf("mapping duplicated delivery: %d %v", deliveries, err)
	}
}

func TestLegacyPolicyMappingRejectsCapacityWithoutPartialWrites(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "capacity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	node, _, err := db.CreateNode(ctx, CreateNodeParams{Name: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	channel, err := db.CreateNotificationChannel(ctx, "fixture", ChannelKindWebhook, "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.db.ExecContext(ctx, `WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<1001)
	INSERT INTO alert_rules(id,node_id,channel_id,kind,config_json,enabled,cooldown_seconds,created_at,updated_at)
	SELECT 'capacity-'||i,?,?,'cpu','{"threshold_percent":80}',1,900,?,? FROM n`, node.ID, channel.ID, nowText(), nowText())
	if err != nil {
		t.Fatal(err)
	}
	if n, err := db.MigrateLegacyAlertPolicies(ctx); err == nil || n != 0 {
		t.Fatalf("capacity accepted: %d %v", n, err)
	}
	for _, table := range []string{"alert_policies", "alert_policy_rule_bindings"} {
		var count int
		if err := db.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("partial migration in %s: %d %v", table, count, err)
		}
	}
}

func TestLegacyPolicyMappingRejectsIdentityCollision(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "collision.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	node, _, err := db.CreateNode(ctx, CreateNodeParams{Name: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	channel, err := db.CreateNotificationChannel(ctx, "fixture", ChannelKindWebhook, "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	p, err := db.SaveAlertPolicy(ctx, AlertPolicy{Policy: alertpolicy.Policy{Key: "existing", Enabled: true, Scope: alertpolicy.Scope{Kind: "all"}}, Name: "existing", ChannelID: channel.ID, Kind: "cpu", Config: json.RawMessage(`{"threshold_percent":80}`), CooldownSeconds: 900})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := db.CreateAlertRule(ctx, node.ID, channel.ID, "cpu", json.RawMessage(`{"threshold_percent":75}`), 900)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.db.ExecContext(ctx, `UPDATE alert_rules SET id=? WHERE id=?`, p.ID, rule.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := db.MigrateLegacyAlertPolicies(ctx); !errors.Is(err, ErrAlertPolicyConflict) || n != 0 {
		t.Fatalf("collision not rejected: %d %v", n, err)
	}
	items, err := db.ListAlertPolicies(ctx)
	if err != nil || len(items) != 1 || items[0].Key != "existing" || items[0].Revision != 1 {
		t.Fatalf("existing policy overwritten: %+v %v", items, err)
	}
	var bindings int
	if err = db.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alert_policy_rule_bindings`).Scan(&bindings); err != nil || bindings != 0 {
		t.Fatalf("partial bindings: %d %v", bindings, err)
	}
}
