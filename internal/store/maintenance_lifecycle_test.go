package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func maintenanceActor(t *testing.T, db *Store, id string) {
	t.Helper()
	if _, err := db.db.ExecContext(context.Background(), `INSERT INTO users(id,username,password_hash,created_at,updated_at) VALUES(?,?, 'unused',?,?)`, id, id, nowText(), nowText()); err != nil {
		t.Fatal(err)
	}
}

func maintenanceDraftFixture(t *testing.T, db *Store) MaintenancePreview {
	t.Helper()
	ctx := context.Background()
	if _, _, err := db.CreateNode(ctx, CreateNodeParams{ID: "node", Name: "Fixture", Tags: []string{"prod"}}); err != nil {
		t.Fatal(err)
	}
	maintenanceActor(t, db, "actor")
	at := time.Now().UTC().Truncate(time.Millisecond)
	preview, err := db.PreviewMaintenanceWindow(ctx, MaintenanceDraft{Selection: MaintenanceSelection{Kind: "tags", Tags: []string{"prod"}, TagMode: "all"}, Reason: "work", Timezone: "UTC", StartsAt: at.Add(-time.Minute), EndsAt: at.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	return preview
}

func TestMaintenanceCancelAuditAndRevision(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	p := maintenanceDraftFixture(t, db)
	maintenanceActor(t, db, "other")
	created, err := db.CreateConfirmedMaintenance(ctx, p.Draft, p.Digest, "create", "actor")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := db.CancelConfirmedMaintenance(ctx, created.WindowID, 2, "actor", now); !errors.Is(err, ErrMaintenanceConflict) {
		t.Fatalf("wrong revision: %v", err)
	}
	if _, err := db.CancelConfirmedMaintenance(ctx, "missing", 1, "actor", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if _, err := db.CancelConfirmedMaintenance(ctx, created.WindowID, 1, "missing-actor", now); err == nil {
		t.Fatal("unknown actor accepted")
	}
	active, err := db.NodeInMaintenance(ctx, "node", now)
	if err != nil || !active {
		t.Fatalf("failed cancel changed window: %v %v", active, err)
	}
	if _, err := db.db.ExecContext(ctx, `CREATE TRIGGER fail_cancel_audit BEFORE INSERT ON audit_log WHEN NEW.action='cancel' BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CancelConfirmedMaintenance(ctx, created.WindowID, 1, "actor", now); err == nil {
		t.Fatal("audit failure accepted")
	}
	var actions int
	if err := db.db.QueryRowContext(ctx, `SELECT count(*) FROM maintenance_cancel_actions`).Scan(&actions); err != nil || actions != 0 {
		t.Fatalf("partial cancellation: %d %v", actions, err)
	}
	if _, err := db.db.ExecContext(ctx, `DROP TRIGGER fail_cancel_audit`); err != nil {
		t.Fatal(err)
	}
	first, err := db.CancelConfirmedMaintenance(ctx, created.WindowID, 1, "actor", now)
	if err != nil || first.Replayed || first.Revision != 2 {
		t.Fatalf("cancel: %+v %v", first, err)
	}
	retry, err := db.CancelConfirmedMaintenance(ctx, created.WindowID, 1, "actor", now.Add(time.Hour))
	if err != nil || !retry.Replayed || !retry.CanceledAt.Equal(first.CanceledAt) {
		t.Fatalf("retry: %+v %v", retry, err)
	}
	if _, err := db.CancelConfirmedMaintenance(ctx, created.WindowID, 1, "other", now); !errors.Is(err, ErrMaintenanceConflict) {
		t.Fatalf("other actor: %v", err)
	}
	if _, err := db.CancelConfirmedMaintenance(ctx, created.WindowID, 2, "actor", now); !errors.Is(err, ErrMaintenanceConflict) {
		t.Fatalf("new version repeat: %v", err)
	}
	windows, _, err := db.ListMaintenanceWindows(ctx, "", 10)
	if err != nil || len(windows) != 1 || windows[0].CanceledBy != "actor" || windows[0].Selection == nil || windows[0].Selection.Kind != "tags" {
		t.Fatalf("history: %+v %v", windows, err)
	}
	before, err := db.NodeInMaintenance(ctx, "node", first.CanceledAt.Add(-time.Millisecond))
	if err != nil || !before {
		t.Fatalf("lost history: %v %v", before, err)
	}
	after, err := db.NodeInMaintenance(ctx, "node", first.CanceledAt)
	if err != nil || after {
		t.Fatalf("cancel boundary: %v %v", after, err)
	}
}

func TestMaintenanceConfirmedConcurrencyAndRestore(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "live.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	p := maintenanceDraftFixture(t, db)
	const concurrency = 8
	results := make(chan MaintenanceCreateResult, concurrency)
	failures := make(chan error, concurrency)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := db.CreateConfirmedMaintenance(ctx, p.Draft, p.Digest, "one-request", "actor")
			results <- result
			failures <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	windowID := ""
	newCount := 0
	for result := range results {
		if windowID == "" {
			windowID = result.WindowID
		}
		if result.WindowID != windowID {
			t.Fatalf("duplicate IDs: %+v", result)
		}
		if !result.Replayed {
			newCount++
		}
	}
	if newCount != 1 {
		t.Fatalf("new creations=%d", newCount)
	}
	cancelAt := time.Now().UTC()
	cancelResults := make(chan MaintenanceCancelResult, concurrency)
	start = make(chan struct{})
	for range concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := db.CancelConfirmedMaintenance(ctx, windowID, 1, "actor", cancelAt)
			if err != nil {
				t.Error(err)
			}
			cancelResults <- result
		}()
	}
	close(start)
	wg.Wait()
	close(cancelResults)
	newCount = 0
	for result := range cancelResults {
		if result.WindowID != windowID || result.Revision != 2 {
			t.Fatalf("cancel result: %+v", result)
		}
		if !result.Replayed {
			newCount++
		}
	}
	if newCount != 1 {
		t.Fatalf("new cancellation count=%d", newCount)
	}
	if t.Failed() {
		t.FailNow()
	}
	snapshot := filepath.Join(t.TempDir(), "snapshot.db")
	if err := db.ConsistentBackup(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	// Changes after the snapshot must be rewound during restoration.
	if _, _, err := db.CreateNode(ctx, CreateNodeParams{ID: "later", Name: "Later", Tags: []string{"prod"}}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := db.CreateConfirmedMaintenance(ctx, p.Draft, p.Digest, "one-request", "actor")
	if err != nil || !retry.Replayed || retry.WindowID != windowID {
		t.Fatalf("restart retry: %+v %v", retry, err)
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
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	retry, err = db.CreateConfirmedMaintenance(ctx, p.Draft, p.Digest, "one-request", "actor")
	if err != nil || !retry.Replayed || retry.WindowID != windowID {
		t.Fatalf("restored retry: %+v %v", retry, err)
	}
	canceled, err := db.CancelConfirmedMaintenance(ctx, windowID, 1, "actor", time.Now().UTC())
	if err != nil || !canceled.Replayed || !canceled.CanceledAt.Equal(cancelAt.UTC().Truncate(time.Millisecond)) {
		t.Fatalf("restored cancellation: %+v %v", canceled, err)
	}
	windows, _, err := db.ListMaintenanceWindows(ctx, "", 10)
	if err != nil || len(windows) != 1 || windows[0].Selection == nil || windows[0].CanceledBy != "actor" || windows[0].Revision != 2 {
		t.Fatalf("restored history: %+v %v", windows, err)
	}
	var count int
	if err := db.db.QueryRowContext(ctx, `SELECT count(*) FROM nodes WHERE id='later'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("snapshot not restored: %d %v", count, err)
	}
	if err := db.db.QueryRowContext(ctx, `SELECT count(*) FROM audit_log WHERE object_type='maintenance_window'`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("restored audits: %d %v", count, err)
	}
}

func TestMaintenanceExpiredAdmissionAndUTCYear(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	p := maintenanceDraftFixture(t, db)
	draft := p.Draft
	draft.StartsAt = time.Now().UTC().Add(-2 * time.Hour)
	draft.EndsAt = draft.StartsAt.Add(time.Hour)
	expired, err := db.PreviewMaintenanceWindow(ctx, draft)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateConfirmedMaintenance(ctx, expired.Draft, expired.Digest, "expired", "actor"); !errors.Is(err, ErrMaintenanceExpired) {
		t.Fatalf("expired create: %v", err)
	}
	created, err := db.CreateConfirmedMaintenance(ctx, p.Draft, p.Digest, "valid", "actor")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CancelConfirmedMaintenance(ctx, created.WindowID, 1, "actor", p.Draft.EndsAt); !errors.Is(err, ErrMaintenanceExpired) {
		t.Fatalf("cancel at end: %v", err)
	}
	windows, _, err := db.ListMaintenanceWindows(ctx, "", 10)
	if err != nil || len(windows) != 1 || windows[0].CanceledAt != nil {
		t.Fatalf("expired operations mutated: %+v %v", windows, err)
	}
	// An offset can move a locally valid year outside the UTC storage bounds.
	for _, tc := range []struct{ start, end time.Time }{
		{time.Date(1970, 1, 1, 0, 0, 0, 0, time.FixedZone("plus", 3600)), time.Date(1970, 1, 1, 2, 0, 0, 0, time.FixedZone("plus", 3600))},
		{time.Date(9999, 12, 31, 22, 0, 0, 0, time.UTC), time.Date(9999, 12, 31, 23, 30, 0, 0, time.FixedZone("minus", -3600))},
	} {
		invalid := p.Draft
		invalid.StartsAt = tc.start
		invalid.EndsAt = tc.end
		if _, err := db.PreviewMaintenanceWindow(ctx, invalid); !errors.Is(err, ErrInvalidMaintenance) {
			t.Fatalf("UTC year: %v", err)
		}
	}
}
