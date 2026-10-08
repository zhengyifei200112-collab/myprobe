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

func TestPolicyRulesFollowTagsAndKeepStableBindings(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "rules.db"))
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
	definition := AlertPolicy{Policy: alertpolicy.Policy{Key: "cpu", Enabled: true, Scope: alertpolicy.Scope{Kind: "all"}}, Name: "global", ChannelID: channel.ID, Kind: "cpu", Config: json.RawMessage(`{"threshold_percent":80}`), CooldownSeconds: 900}
	global, err := db.SaveAlertPolicy(ctx, definition)
	if err != nil {
		t.Fatal(err)
	}
	definition.Name = "tags"
	definition.Scope = alertpolicy.Scope{Kind: "tags", Tags: []string{"prod"}, TagMode: "all"}
	tag, err := db.SaveAlertPolicy(ctx, definition)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err = db.SyncAlertPolicyRules(ctx, now); err != nil {
		t.Fatal(err)
	}
	rules, err := db.ListAlertRules(ctx)
	if err != nil || len(rules) != 1 {
		t.Fatalf("initial rules %+v %v", rules, err)
	}
	globalRule := rules[0]
	if _, err = db.ObserveAlert(ctx, globalRule, node, AlertObservation{At: now, Known: true, Active: true, Message: "fixture"}); err != nil {
		t.Fatal(err)
	}
	if _, err = db.db.ExecContext(ctx, `UPDATE nodes SET tags_json='["prod"]' WHERE id=?`, node.ID); err != nil {
		t.Fatal(err)
	}
	if err = db.SyncAlertPolicyRules(ctx, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	rules, err = db.ListAlertRules(ctx)
	if err != nil || len(rules) != 2 {
		t.Fatalf("tag rules %+v %v", rules, err)
	}
	var tagRule AlertRule
	for _, r := range rules {
		if r.ID == globalRule.ID {
			if r.Enabled {
				t.Fatal("overridden rule active")
			}
		} else {
			tagRule = r
			if !r.Enabled {
				t.Fatal("selected rule disabled")
			}
		}
	}
	if err = db.ReconcileIncidents(ctx, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	var state, reason string
	if err = db.db.QueryRowContext(ctx, `SELECT state,resolution_reason FROM alert_incidents WHERE rule_id=?`, globalRule.ID).Scan(&state, &reason); err != nil || state != "resolved" || reason != "rule_disabled" {
		t.Fatalf("management closure: %s %s %v", state, reason, err)
	}
	if err = db.SyncAlertPolicyRules(ctx, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	again, err := db.AlertRule(ctx, tagRule.ID)
	if err != nil || !again.UpdatedAt.Equal(tagRule.UpdatedAt) {
		t.Fatal("unchanged binding rewritten", err)
	}
	if err = db.DeleteAlertPolicy(ctx, tag.ID, tag.Revision+1); !errors.Is(err, ErrAlertPolicyConflict) {
		t.Fatal(err)
	}
	if _, err = db.AlertRule(ctx, tagRule.ID); err != nil {
		t.Fatal("stale delete removed bound rule", err)
	}
	if err = db.DeleteAlertPolicy(ctx, tag.ID, tag.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = db.AlertRule(ctx, tagRule.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted policy retained execution rule", err)
	}
	if err = db.SyncAlertPolicyRules(ctx, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	again, err = db.AlertRule(ctx, globalRule.ID)
	if err != nil || !again.Enabled {
		t.Fatal("fallback failed", err)
	}
	global.Enabled = false
	if _, err = db.SaveAlertPolicy(ctx, global); err != nil {
		t.Fatal(err)
	}
	if err = db.SyncAlertPolicyRules(ctx, now.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	again, err = db.AlertRule(ctx, globalRule.ID)
	if err != nil || again.Enabled {
		t.Fatal("disabled policy still executes", err)
	}
}
