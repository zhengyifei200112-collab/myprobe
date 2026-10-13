package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/zhengyifei200112-collab/myprobe/internal/alertpolicy"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
	"path/filepath"
	"testing"
)

func TestAggregationConfigBoundsAndOlderEditorPreservation(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "config.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	svc := New(db, "synthetic-configuration-key-0123456789", &recordingSender{}, nil)
	channel, err := svc.CreateChannel(ctx, "fixture", "webhook", ChannelConfig{URL: "https://example.com/unused"})
	if err != nil {
		t.Fatal(err)
	}
	p := store.AlertPolicy{Policy: alertpolicy.Policy{Key: "cpu", Scope: alertpolicy.Scope{Kind: "all"}, Enabled: true}, Name: "cpu", ChannelID: channel.ID, Kind: "cpu", CooldownSeconds: 300}
	for _, seconds := range []int{-1, 1, 4, 301} {
		p.Config = json.RawMessage(fmt.Sprintf(`{"threshold_percent":90,"aggregation_seconds":%d}`, seconds))
		if _, err := svc.SavePolicy(ctx, p); !errors.Is(err, store.ErrInvalidAlertPolicy) {
			t.Fatalf("accepted %d: %v", seconds, err)
		}
	}
	p.Config = json.RawMessage(`{"threshold_percent":90,"aggregation_seconds":30}`)
	p, err = svc.SavePolicy(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	p.Name = "older editor name"
	p.Config = json.RawMessage(`{"threshold_percent":90}`)
	p, err = svc.SavePolicy(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	var cfg RuleConfig
	if err := json.Unmarshal(p.Config, &cfg); err != nil || cfg.AggregationSeconds == nil || *cfg.AggregationSeconds != 30 {
		t.Fatal("older editor accidentally disabled aggregation")
	}
	p.Config = json.RawMessage(`{"threshold_percent":90,"aggregation_seconds":0}`)
	p, err = svc.SavePolicy(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(p.Config, &cfg); err != nil || cfg.AggregationSeconds == nil || *cfg.AggregationSeconds != 0 {
		t.Fatal("explicit disable was ignored")
	}
	for _, seconds := range []int{5, 300} {
		p.Config = json.RawMessage(fmt.Sprintf(`{"threshold_percent":90,"aggregation_seconds":%d}`, seconds))
		p, err = svc.SavePolicy(ctx, p)
		if err != nil {
			t.Fatalf("valid boundary %d: %v", seconds, err)
		}
	}
}
