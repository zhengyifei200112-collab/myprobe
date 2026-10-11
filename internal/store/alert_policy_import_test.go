package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/alertpolicy"
)

func TestPolicyImportReplaySurvivesDatabaseRestore(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "live.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	channel, err := db.CreateNotificationChannel(ctx, "fixture", ChannelKindWebhook, "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	p := AlertPolicy{Policy: alertpolicy.Policy{Key: "cpu", Scope: alertpolicy.Scope{Kind: "all"}}, Name: "fixture", ChannelID: channel.ID, Kind: "cpu", Config: json.RawMessage(`{"threshold_percent":90}`), CooldownSeconds: 900}
	digest := strings.Repeat("a", 64)
	original, err := db.ApplyPolicyImport(ctx, "before-backup", digest, []AlertPolicy{p})
	if err != nil {
		t.Fatal(err)
	}
	// A historical result remains useful even when its policy was deleted before backup.
	if err := db.DeleteAlertPolicy(ctx, original.PolicyIDs[0], 1); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(dir, "snapshot.db")
	if err := db.ConsistentBackup(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	after, err := db.ApplyPolicyImport(ctx, "after-backup", digest, []AlertPolicy{p})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := StageDatabaseRestore(ctx, path, snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyPendingRestore(ctx, path, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	replay, err := restored.ApplyPolicyImport(ctx, "before-backup", digest, []AlertPolicy{p})
	if err != nil || !replay.Replayed || !reflect.DeepEqual(replay.PolicyIDs, original.PolicyIDs) {
		t.Fatalf("restored replay: %+v %v", replay, err)
	}
	if _, err := restored.ApplyPolicyImport(ctx, "before-backup", strings.Repeat("b", 64), []AlertPolicy{p}); !errors.Is(err, ErrPolicyImportConflict) {
		t.Fatalf("restored conflict: %v", err)
	}
	items, err := restored.ListAlertPolicies(ctx)
	if err != nil || len(items) != 0 {
		t.Fatalf("replay resurrected policies: %+v %v", items, err)
	}
	// Restore rewinds the ledger as well: post-backup imports cannot be claimed as replayed.
	if recorded, err := restored.LookupPolicyImport(ctx, "after-backup", digest); err != nil || recorded != nil {
		t.Fatalf("future record survived restore: %+v %v", recorded, err)
	}
	fresh, err := restored.ApplyPolicyImport(ctx, "after-backup", digest, []AlertPolicy{p})
	if err != nil || fresh.Replayed || len(fresh.PolicyIDs) != 1 || fresh.PolicyIDs[0] == after.PolicyIDs[0] {
		t.Fatalf("restored future import: %+v %v", fresh, err)
	}
}

func TestPolicyImportRetriesAcrossConnectionsAndReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "import.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	other, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	channel, err := db.CreateNotificationChannel(ctx, "fixture", ChannelKindWebhook, "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	// Disabled policies do not conflict, so only the request ledger prevents duplicates.
	p := AlertPolicy{Policy: alertpolicy.Policy{Key: "cpu", Scope: alertpolicy.Scope{Kind: "all"}}, Name: "fixture", ChannelID: channel.ID, Kind: "cpu", Config: json.RawMessage(`{"threshold_percent":90}`), CooldownSeconds: 900}
	digest := strings.Repeat("a", 64)
	results := make(chan PolicyImportResult, 2)
	var workers sync.WaitGroup
	for _, s := range []*Store{db, other} {
		workers.Add(1)
		go func(s *Store) {
			defer workers.Done()
			result, err := s.ApplyPolicyImport(ctx, "request", digest, []AlertPolicy{p})
			if err != nil {
				t.Error(err)
			}
			results <- result
		}(s)
	}
	workers.Wait()
	close(results)
	var ids []string
	replays := 0
	for result := range results {
		if len(result.PolicyIDs) != 1 {
			t.Fatalf("result %+v", result)
		}
		if ids == nil {
			ids = result.PolicyIDs
		} else if !reflect.DeepEqual(ids, result.PolicyIDs) {
			t.Fatal("concurrent retry created different IDs")
		}
		if result.Replayed {
			replays++
		}
	}
	if replays != 1 {
		t.Fatalf("replays=%d", replays)
	}
	if _, err := db.ApplyPolicyImport(ctx, "request", strings.Repeat("b", 64), []AlertPolicy{p}); !errors.Is(err, ErrPolicyImportConflict) {
		t.Fatalf("digest conflict: %v", err)
	}
	bad := p
	bad.ChannelID = "missing"
	if _, err := db.ApplyPolicyImport(ctx, "failed", digest, []AlertPolicy{p, bad}); err == nil {
		t.Fatal("partial import accepted")
	}
	var recorded int
	if err := db.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alert_policy_imports`).Scan(&recorded); err != nil || recorded != 1 {
		t.Fatalf("failed batch recorded: %d %v", recorded, err)
	}
	items, err := db.ListAlertPolicies(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("partial policies %+v %v", items, err)
	}
	if err := db.DeleteAlertPolicy(ctx, ids[0], 1); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	result, err := reopened.ApplyPolicyImport(ctx, "request", digest, []AlertPolicy{p})
	if err != nil || !result.Replayed || !reflect.DeepEqual(ids, result.PolicyIDs) {
		t.Fatalf("reopen lost replay: %+v %v", result, err)
	}
	items, err = reopened.ListAlertPolicies(ctx)
	if err != nil || len(items) != 0 {
		t.Fatal("retry resurrected deleted policy", err)
	}
}
