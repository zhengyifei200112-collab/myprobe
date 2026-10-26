package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/zhengyifei200112-collab/myprobe/internal/alertpolicy"
)

func TestAlertPolicyPersistenceAndDynamicPreview(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "policies.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	node, _, err := db.CreateNode(ctx, CreateNodeParams{Name: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	channel, err := db.CreateNotificationChannel(ctx, "fixture", ChannelKindWebhook, "synthetic-encrypted")
	if err != nil {
		t.Fatal(err)
	}
	p := AlertPolicy{Policy: alertpolicy.Policy{Key: "cpu.warning", Enabled: true, Scope: alertpolicy.Scope{Kind: "all"}}, Name: "default", Kind: "cpu", ChannelID: channel.ID, Config: json.RawMessage(`{"threshold_percent":80}`), CooldownSeconds: 60}
	saved, err := db.SaveAlertPolicy(ctx, p)
	if err != nil || saved.Revision != 1 {
		t.Fatalf("%+v %v", saved, err)
	}
	stale := saved
	saved.Name = "updated"
	saved, err = db.SaveAlertPolicy(ctx, saved)
	if err != nil || saved.Revision != 2 {
		t.Fatalf("%+v %v", saved, err)
	}
	if _, err = db.SaveAlertPolicy(ctx, stale); !errors.Is(err, ErrAlertPolicyConflict) {
		t.Fatal("stale overwrite", err)
	}
	if err = db.DeleteAlertPolicy(ctx, saved.ID, 1); !errors.Is(err, ErrAlertPolicyConflict) {
		t.Fatal("stale delete", err)
	}
	selected, err := db.EffectiveAlertPolicies(ctx, node.ID)
	if err != nil || len(selected) != 1 || selected[0].SelectedID != saved.ID {
		t.Fatalf("%+v %v", selected, err)
	}
	p.Name = "tag policy"
	p.Scope = alertpolicy.Scope{Kind: "tags", TagMode: "all", Tags: []string{"prod"}}
	tagPolicy, err := db.SaveAlertPolicy(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.db.ExecContext(ctx, `UPDATE nodes SET tags_json='["prod"]' WHERE id=?`, node.ID); err != nil {
		t.Fatal(err)
	}
	selected, err = db.EffectiveAlertPolicies(ctx, node.ID)
	if err != nil || selected[0].SelectedID != tagPolicy.ID || len(selected[0].CandidateIDs) != 2 {
		t.Fatalf("%+v %v", selected, err)
	}
	p.Scope = alertpolicy.Scope{Kind: "nodes", NodeIDs: []string{"unknown-node"}}
	if _, err = db.SaveAlertPolicy(ctx, p); !errors.Is(err, ErrInvalidAlertPolicy) {
		t.Fatal("unknown node accepted", err)
	}
	items, err := db.ListAlertPolicies(ctx)
	if err != nil || len(items) != 2 {
		t.Fatalf("partial write: %d %v", len(items), err)
	}
	if err = db.DeleteNotificationChannel(ctx, channel.ID); err == nil {
		t.Fatal("referenced channel deleted")
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	items, err = reopened.ListAlertPolicies(ctx)
	if err != nil || len(items) != 2 {
		t.Fatalf("restart lost policies: %d %v", len(items), err)
	}
	if err = reopened.DeleteAlertPolicy(ctx, saved.ID, 2); err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.EffectiveAlertPolicies(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestConcurrentAlertPolicyConflictsAcrossStores(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "concurrent.db")
	a, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	channel, err := a.CreateNotificationChannel(ctx, "fixture", ChannelKindWebhook, "synthetic-encrypted")
	if err != nil {
		t.Fatal(err)
	}
	p := AlertPolicy{Policy: alertpolicy.Policy{Key: "cpu", Enabled: true, Scope: alertpolicy.Scope{Kind: "all"}}, Name: "same tier", Kind: "cpu", ChannelID: channel.ID, Config: json.RawMessage(`{}`), CooldownSeconds: 60}
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, db := range []*Store{a, b} {
		workers.Add(1)
		go func(db *Store) { defer workers.Done(); <-start; _, err := db.SaveAlertPolicy(ctx, p); results <- err }(db)
	}
	close(start)
	workers.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrAlertPolicyConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("success=%d conflicts=%d", success, conflict)
	}
	items, err := a.ListAlertPolicies(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("%d %v", len(items), err)
	}
	p.Enabled = false
	draft, err := a.SaveAlertPolicy(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	draft.Enabled = true
	if _, err = a.SaveAlertPolicy(ctx, draft); !errors.Is(err, ErrAlertPolicyConflict) {
		t.Fatal("activation conflict not rejected", err)
	}
	items, err = a.ListAlertPolicies(ctx)
	if err != nil || len(items) != 2 {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.ID == draft.ID && (item.Enabled || item.Revision != 1) {
			t.Fatal("failed activation partially applied")
		}
	}
}
