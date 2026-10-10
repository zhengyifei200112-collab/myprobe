package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/zhengyifei200112-collab/myprobe/internal/alertpolicy"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestPolicyConfigurationValidationAndDefaults(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "policies.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	channel, err := db.CreateNotificationChannel(ctx, "fixture", store.ChannelKindWebhook, "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	service := New(db, "", nil, nil)
	policy := store.AlertPolicy{Policy: alertpolicy.Policy{Key: "cpu", Enabled: true, Scope: alertpolicy.Scope{Kind: "all"}}, Name: "fixture", ChannelID: channel.ID, Kind: "cpu", CooldownSeconds: 60}
	for _, raw := range []string{`null`, `[]`, `{"threshold_percent":101}`, `{"threshold_percent":-1}`, `{"threshold_percnt":80}`, `{"duration_seconds":-1}`, `{"threshold_percent":80} {}`, `{"template_id":"missing"}`} {
		policy.Config = json.RawMessage(raw)
		if _, err := service.SavePolicy(ctx, policy); !errors.Is(err, store.ErrInvalidAlertPolicy) {
			t.Fatalf("invalid config accepted: %s: %v", raw, err)
		}
	}
	items, err := db.ListAlertPolicies(ctx)
	if err != nil || len(items) != 0 {
		t.Fatalf("invalid writes persisted: %d %v", len(items), err)
	}
	policy.Config = json.RawMessage(`{}`)
	saved, err := service.SavePolicy(ctx, policy)
	if err != nil {
		t.Fatal(err)
	}
	var config RuleConfig
	if err = json.Unmarshal(saved.Config, &config); err != nil || config.ThresholdPercent != 90 {
		t.Fatalf("default not normalized: %+v %v", config, err)
	}
	saved.Config = json.RawMessage(`{"threshold_percent":85,"duration_seconds":60}`)
	updated, err := service.SavePolicy(ctx, saved)
	if err != nil || updated.Revision != 2 {
		t.Fatalf("valid update failed: %+v %v", updated, err)
	}
	if _, err = service.SavePolicy(ctx, saved); !errors.Is(err, store.ErrAlertPolicyConflict) {
		t.Fatal("lost CAS error", err)
	}
}
