package alerts

import (
	"context"
	"errors"
	"testing"

	"github.com/zhengyifei200112-collab/myprobe/internal/alertpolicy"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestPolicyImportPreviewMapsReferencesWithoutWriting(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db, "", nil, nil)
	channel, err := db.CreateNotificationChannel(ctx, "destination", store.ChannelKindWebhook, "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "destination"})
	if err != nil {
		t.Fatal(err)
	}
	template, err := db.SaveNotificationTemplate(ctx, "", "destination", "all", "{{message}}", "{{message}}")
	if err != nil {
		t.Fatal(err)
	}
	zero := 0
	bundle := PolicyBundle{Format: "myprobe-alert-policies", Version: 1, Policies: []PortablePolicy{{SourceID: "source-policy", Name: "CPU", Key: "cpu", Enabled: true, Scope: alertpolicy.Scope{Kind: "nodes", NodeIDs: []string{"source-node"}}, ChannelID: "source-channel", Kind: "cpu", Config: RuleConfig{ThresholdPercent: 90, TemplateID: "source-template", RecoverySeconds: &zero}, CooldownSeconds: 900}}}
	mapping := PolicyImportMapping{Channels: map[string]string{"source-channel": channel.ID}, Nodes: map[string]string{"source-node": node.ID}, Templates: map[string]string{"source-template": template.ID}}
	preview, err := s.PreviewPolicyImport(ctx, bundle, mapping)
	if err != nil || len(preview.Policies) != 1 {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	p := preview.Policies[0]
	if p.SourceID != "source-policy" || p.ChannelID != channel.ID || p.Scope.NodeIDs[0] != node.ID || p.Config.TemplateID != template.ID || p.Config.RecoverySeconds == nil || *p.Config.RecoverySeconds != 0 {
		t.Fatalf("mapped preview: %+v", p)
	}
	if bundle.Policies[0].Scope.NodeIDs[0] != "source-node" || bundle.Policies[0].Config.TemplateID != "source-template" {
		t.Fatal("preview modified source bundle")
	}
	for _, bad := range []string{"channel", "node", "template", "threshold", "duplicate", "version", "missing-destination"} {
		candidate := bundle
		candidate.Policies = append([]PortablePolicy(nil), bundle.Policies...)
		refs := mapping
		switch bad {
		case "channel":
			refs.Channels = nil
		case "node":
			refs.Nodes = nil
		case "template":
			refs.Templates = nil
		case "threshold":
			candidate.Policies[0].Config.ThresholdPercent = 101
		case "duplicate":
			candidate.Policies = append(candidate.Policies, candidate.Policies[0])
		case "version":
			candidate.Version = 2
		case "missing-destination":
			refs.Nodes = map[string]string{"source-node": "missing"}
		}
		if _, err := s.PreviewPolicyImport(ctx, candidate, refs); !errors.Is(err, store.ErrInvalidAlertPolicy) {
			t.Fatalf("%s accepted: %v", bad, err)
		}
	}
	items, err := db.ListAlertPolicies(ctx)
	if err != nil || len(items) != 0 {
		t.Fatalf("preview wrote policies: %+v %v", items, err)
	}
	definitions, err := s.preparePolicyImport(ctx, bundle, mapping)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.CreateAlertPolicies(ctx, definitions, false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PreviewPolicyImport(ctx, bundle, mapping); !errors.Is(err, store.ErrAlertPolicyConflict) {
		t.Fatalf("destination conflict not detected: %v", err)
	}
}
