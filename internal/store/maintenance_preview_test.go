package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMaintenanceWindowPreviewConfirmation(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, id := range []string{"a", "b"} {
		if _, _, err := db.CreateNode(ctx, CreateNodeParams{ID: id, Name: id, Tags: []string{"prod"}}); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Date(2026, 10, 11, 10, 0, 0, 123456789, time.FixedZone("offset", 8*3600))
	draft := MaintenanceDraft{Selection: MaintenanceSelection{Kind: "nodes", NodeIDs: []string{"b", "a"}}, Reason: " planned work ", Timezone: "Asia/Shanghai", StartsAt: at, EndsAt: at.Add(time.Hour)}
	first, err := db.PreviewMaintenanceWindow(ctx, draft)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Digest) != 64 || first.Draft.Reason != "planned work" || first.Draft.StartsAt.Location() != time.UTC || first.Draft.StartsAt.Nanosecond() != 123000000 || draft.Selection.NodeIDs[0] != "b" {
		t.Fatalf("normalization: %+v input %+v", first, draft)
	}
	canonical, err := db.PreviewMaintenanceWindow(ctx, first.Draft)
	if err != nil || canonical.Digest != first.Digest {
		t.Fatalf("canonical round trip: %+v %v", canonical, err)
	}
	changed := draft
	changed.Reason = "different reason"
	second, err := db.PreviewMaintenanceWindow(ctx, changed)
	if err != nil || second.Digest == first.Digest {
		t.Fatalf("changed reason: %+v %v", second, err)
	}
	if _, err := db.db.ExecContext(ctx, `UPDATE nodes SET name='renamed' WHERE id='a'`); err != nil {
		t.Fatal(err)
	}
	second, err = db.PreviewMaintenanceWindow(ctx, draft)
	if err != nil || second.Digest == first.Digest {
		t.Fatalf("changed display: %+v %v", second, err)
	}
	for _, mutate := range []func(*MaintenanceDraft){
		func(d *MaintenanceDraft) { d.Reason = "" },
		func(d *MaintenanceDraft) { d.Timezone = "Local" },
		func(d *MaintenanceDraft) { d.EndsAt = d.StartsAt.Add(time.Nanosecond) },
		func(d *MaintenanceDraft) { d.StartsAt = time.Time{} },
	} {
		invalid := draft
		mutate(&invalid)
		if _, err := db.PreviewMaintenanceWindow(ctx, invalid); !errors.Is(err, ErrInvalidMaintenance) {
			t.Fatalf("invalid: %v", err)
		}
	}
	windows, _, err := db.ListMaintenanceWindows(ctx, "", 10)
	if err != nil || len(windows) != 0 {
		t.Fatalf("preview mutated windows: %v %v", windows, err)
	}
	draft.Selection = MaintenanceSelection{Kind: "tags", Tags: []string{"prod"}, TagMode: "all"}
	before, err := db.PreviewMaintenanceWindow(ctx, draft)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `UPDATE nodes SET tags_json='[]' WHERE id='b'`); err != nil {
		t.Fatal(err)
	}
	after, err := db.PreviewMaintenanceWindow(ctx, draft)
	if err != nil || len(after.Members) != 1 || after.Digest == before.Digest {
		t.Fatalf("changed membership: %+v %v", after, err)
	}
}
