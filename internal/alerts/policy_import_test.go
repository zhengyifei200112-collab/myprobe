package alerts

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/zhengyifei200112-collab/myprobe/internal/alertpolicy"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestPolicyImportRejectsTemplateDeletedAfterPreparation(t *testing.T) {
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
	template, err := db.SaveNotificationTemplate(ctx, "", "destination", "all", "{{message}}", "{{message}}")
	if err != nil {
		t.Fatal(err)
	}
	bundle := PolicyBundle{Format: "myprobe-alert-policies", Version: 1, Policies: []PortablePolicy{{SourceID: "source", Name: "CPU", Key: "cpu", Scope: alertpolicy.Scope{Kind: "all"}, ChannelID: "channel", Kind: "cpu", Config: RuleConfig{ThresholdPercent: 90, TemplateID: "template"}, CooldownSeconds: 900}}}
	mapping := PolicyImportMapping{Channels: map[string]string{"channel": channel.ID}, Templates: map[string]string{"template": template.ID}}
	prepared, err := s.preparePolicyImport(ctx, bundle, mapping)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteNotificationTemplate(ctx, template.ID); err != nil {
		t.Fatal(err)
	}
	// Reproduce the gap between service validation and acquiring the writer lock.
	digest := strings.Repeat("a", 64)
	if _, err := db.ApplyPolicyImport(ctx, "deleted-template", digest, prepared); !errors.Is(err, store.ErrInvalidAlertPolicy) {
		t.Fatalf("stale template accepted: %v", err)
	}
	if replay, err := db.LookupPolicyImport(ctx, "deleted-template", digest); err != nil || replay != nil {
		t.Fatalf("failed import recorded: %+v %v", replay, err)
	}
	items, err := db.ListAlertPolicies(ctx)
	if err != nil || len(items) != 0 {
		t.Fatalf("invalid import persisted: %+v %v", items, err)
	}
}

func TestPolicyImportApplyReplaysAfterReferencesDisappear(t *testing.T) {
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
	bundle := PolicyBundle{Format: "myprobe-alert-policies", Version: 1, Policies: []PortablePolicy{{SourceID: "source", Name: "CPU", Key: "cpu", Scope: alertpolicy.Scope{Kind: "nodes", NodeIDs: []string{"node"}}, ChannelID: "channel", Kind: "cpu", Config: RuleConfig{ThresholdPercent: 90, TemplateID: "template"}, CooldownSeconds: 900}}}
	mapping := PolicyImportMapping{Channels: map[string]string{"channel": channel.ID}, Nodes: map[string]string{"node": node.ID}, Templates: map[string]string{"template": template.ID}}
	if _, err := s.ApplyPolicyImport(ctx, "invalid key", bundle, mapping); !errors.Is(err, store.ErrInvalidAlertPolicy) {
		t.Fatalf("invalid request key: %v", err)
	}
	first, err := s.ApplyPolicyImport(ctx, "request", bundle, mapping)
	if err != nil || first.Replayed || len(first.PolicyIDs) != 1 {
		t.Fatalf("first: %+v %v", first, err)
	}
	if err := db.DeleteAlertPolicy(ctx, first.PolicyIDs[0], 1); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteNotificationTemplate(ctx, template.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteNode(ctx, node.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteNotificationChannel(ctx, channel.ID); err != nil {
		t.Fatal(err)
	}
	// A fresh service must replay historical IDs before checking mutable references.
	s = New(db, "", nil, nil)
	retry, err := s.ApplyPolicyImport(ctx, "request", bundle, mapping)
	if err != nil || !retry.Replayed || !reflect.DeepEqual(first.PolicyIDs, retry.PolicyIDs) {
		t.Fatalf("retry: %+v %v", retry, err)
	}
	changed := bundle
	changed.Policies = append([]PortablePolicy(nil), bundle.Policies...)
	changed.Policies[0].Name = "changed"
	if _, err := s.ApplyPolicyImport(ctx, "request", changed, mapping); !errors.Is(err, store.ErrPolicyImportConflict) {
		t.Fatalf("changed bundle: %v", err)
	}
	changedMapping := mapping
	changedMapping.Channels = map[string]string{"channel": "different"}
	if _, err := s.ApplyPolicyImport(ctx, "request", bundle, changedMapping); !errors.Is(err, store.ErrPolicyImportConflict) {
		t.Fatalf("changed mapping: %v", err)
	}
	if _, err := s.ApplyPolicyImport(ctx, "new-request", bundle, mapping); err == nil {
		t.Fatal("new import accepted missing references")
	}
	items, err := db.ListAlertPolicies(ctx)
	if err != nil || len(items) != 0 {
		t.Fatalf("replay resurrected policies: %+v %v", items, err)
	}
}

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
