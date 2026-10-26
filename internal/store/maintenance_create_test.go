package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestConfirmedMaintenanceAtomicReplay(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.db.ExecContext(ctx, `INSERT INTO users(id,username,password_hash,created_at,updated_at) VALUES('actor','fixture','unused',?,?)`, nowText(), nowText()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.CreateNode(ctx, CreateNodeParams{ID: "a", Name: "A", Tags: []string{"prod"}}); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	draft := MaintenanceDraft{Selection: MaintenanceSelection{Kind: "tags", Tags: []string{"prod"}, TagMode: "all"}, Reason: "work", Timezone: "UTC", StartsAt: at, EndsAt: at.Add(time.Hour)}
	preview, err := db.PreviewMaintenanceWindow(ctx, draft)
	if err != nil {
		t.Fatal(err)
	}
	draft = preview.Draft
	if _, err := db.db.ExecContext(ctx, `CREATE TRIGGER fail_maintenance_audit BEFORE INSERT ON audit_log BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateConfirmedMaintenance(ctx, draft, preview.Digest, "request", "actor"); err == nil {
		t.Fatal("audit failure accepted")
	}
	for _, table := range []string{"maintenance_windows", "maintenance_window_nodes", "maintenance_selections", "maintenance_create_requests"} {
		var count int
		if err := db.db.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("partial %s: %d %v", table, count, err)
		}
	}
	if _, err := db.db.ExecContext(ctx, `DROP TRIGGER fail_maintenance_audit`); err != nil {
		t.Fatal(err)
	}
	first, err := db.CreateConfirmedMaintenance(ctx, draft, preview.Digest, "request", "actor")
	if err != nil || first.Replayed || first.WindowID == "" {
		t.Fatalf("create: %+v %v", first, err)
	}
	if _, err := db.db.ExecContext(ctx, `UPDATE nodes SET name='renamed' WHERE id='a'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateConfirmedMaintenance(ctx, draft, preview.Digest, "new-request", "actor"); !errors.Is(err, ErrMaintenancePreviewChanged) {
		t.Fatalf("stale preview: %v", err)
	}
	if _, err := db.db.ExecContext(ctx, `DELETE FROM nodes WHERE id='a'`); err != nil {
		t.Fatal(err)
	}
	retry, err := db.CreateConfirmedMaintenance(ctx, draft, preview.Digest, "request", "actor")
	if err != nil || !retry.Replayed || retry.WindowID != first.WindowID {
		t.Fatalf("replay after deletion: %+v %v", retry, err)
	}
	changed := draft
	changed.Reason = "changed"
	if _, err := db.CreateConfirmedMaintenance(ctx, changed, preview.Digest, "request", "actor"); !errors.Is(err, ErrMaintenanceRequestConflict) {
		t.Fatalf("request conflict: %v", err)
	}
	var count int
	if err := db.db.QueryRowContext(ctx, `SELECT count(*) FROM audit_log WHERE object_type='maintenance_window'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("audit: %d %v", count, err)
	}
}
