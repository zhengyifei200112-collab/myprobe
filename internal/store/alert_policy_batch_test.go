package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/zhengyifei200112-collab/myprobe/internal/alertpolicy"
)

func TestPolicyBatchPreviewCommitAndRollback(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	channel, err := db.CreateNotificationChannel(ctx, "fixture", ChannelKindWebhook, "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	first := AlertPolicy{Policy: alertpolicy.Policy{Key: "cpu", Enabled: true, Scope: alertpolicy.Scope{Kind: "all"}}, Name: "cpu", ChannelID: channel.ID, Kind: "cpu", Config: json.RawMessage(`{"threshold_percent":90}`), CooldownSeconds: 900}
	second := first
	second.Key = "cpu.other"
	batch := []AlertPolicy{first, second}
	for _, invalid := range []string{"conflict", "channel", "overwrite"} {
		candidate := append([]AlertPolicy(nil), batch...)
		want := ErrInvalidAlertPolicy
		switch invalid {
		case "conflict":
			candidate[1].Key = first.Key
			want = ErrAlertPolicyConflict
		case "channel":
			candidate[1].ChannelID = "missing"
		case "overwrite":
			candidate[1].ID = "existing"
			candidate[1].Revision = 1
		}
		for _, dry := range []bool{false, true} {
			if partial, err := db.CreateAlertPolicies(ctx, candidate, dry); !errors.Is(err, want) || partial != nil {
				t.Fatalf("%s dry=%v: %+v %v", invalid, dry, partial, err)
			}
			items, err := db.ListAlertPolicies(ctx)
			if err != nil || len(items) != 0 {
				t.Fatalf("partial batch persisted: %+v %v", items, err)
			}
		}
	}
	preview, err := db.CreateAlertPolicies(ctx, batch, true)
	if err != nil || len(preview) != 2 {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	items, err := db.ListAlertPolicies(ctx)
	if err != nil || len(items) != 0 {
		t.Fatal("preview persisted changes", err)
	}
	committed, err := db.CreateAlertPolicies(ctx, batch, false)
	if err != nil || len(committed) != 2 {
		t.Fatalf("commit: %+v %v", committed, err)
	}
	for i, p := range committed {
		if p.ID == "" || p.ID == preview[i].ID || p.Revision != 1 || batch[i].ID != "" {
			t.Fatal("invalid identity or caller mutation")
		}
	}
	if _, err := db.CreateAlertPolicies(ctx, batch, false); !errors.Is(err, ErrAlertPolicyConflict) {
		t.Fatalf("duplicate enabled batch accepted: %v", err)
	}
	items, err = db.ListAlertPolicies(ctx)
	if err != nil || len(items) != 2 {
		t.Fatalf("failed retry changed population: %+v %v", items, err)
	}
}
