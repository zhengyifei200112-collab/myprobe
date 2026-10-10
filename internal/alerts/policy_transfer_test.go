package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/zhengyifei200112-collab/myprobe/internal/alertpolicy"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestPolicyExportPreservesRecoveryAndExcludesRuntimeAndCredentials(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db, "", nil, nil)
	empty, err := s.ExportPolicyBundle(ctx)
	if err != nil || empty.Policies == nil || len(empty.Policies) != 0 || empty.Version != 1 {
		t.Fatalf("empty bundle: %+v %v", empty, err)
	}
	channel, err := db.CreateNotificationChannel(ctx, "secret-channel-name", store.ChannelKindWebhook, "secret-channel-config")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"inherit", "zero"} {
		raw := json.RawMessage(`{"threshold_percent":90,"duration_seconds":60}`)
		if key == "zero" {
			raw = json.RawMessage(`{"threshold_percent":90,"duration_seconds":60,"recovery_seconds":0}`)
		}
		// Cover both persisted representations, including pre-normalization legacy data.
		_, err := db.SaveAlertPolicy(ctx, store.AlertPolicy{Policy: alertpolicy.Policy{Key: key, Enabled: true, Scope: alertpolicy.Scope{Kind: "all"}}, Name: key, ChannelID: channel.ID, Kind: "cpu", Config: raw, CooldownSeconds: 900})
		if err != nil {
			t.Fatal(err)
		}
	}
	bundle, err := s.ExportPolicyBundle(ctx)
	if err != nil || len(bundle.Policies) != 2 {
		t.Fatalf("export: %+v %v", bundle, err)
	}
	for _, p := range bundle.Policies {
		if p.ChannelID != channel.ID || p.SourceID == "" {
			t.Fatal("lost references")
		}
		if p.Key == "inherit" && p.Config.RecoverySeconds != nil {
			t.Fatal("inheritance lost")
		}
		if p.Key == "zero" && (p.Config.RecoverySeconds == nil || *p.Config.RecoverySeconds != 0) {
			t.Fatal("explicit zero lost")
		}
	}
	raw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"secret-channel-name", "secret-channel-config", `"revision"`, `"created_at"`, `"updated_at"`, `"deliveries"`} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("export contains excluded field %q", forbidden)
		}
	}
	// Store-level legacy definitions can have fields the portable format cannot represent.
	_, err = db.SaveAlertPolicy(ctx, store.AlertPolicy{Policy: alertpolicy.Policy{Key: "unsupported", Scope: alertpolicy.Scope{Kind: "all"}}, Name: "unsupported", ChannelID: channel.ID, Kind: "cpu", Config: json.RawMessage(`{"threshold_percent":90,"unknown_field":"secret"}`), CooldownSeconds: 900})
	if err != nil {
		t.Fatal(err)
	}
	if partial, err := s.ExportPolicyBundle(ctx); !errors.Is(err, store.ErrInvalidAlertPolicy) || partial.Policies != nil {
		t.Fatalf("unsupported field exported or silently dropped: %+v %v", partial, err)
	}
}
