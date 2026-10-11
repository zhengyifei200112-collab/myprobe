package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestMaintenancePaginationRetainsHistoricalMembers(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	empty, next, err := db.ListMaintenanceWindows(ctx, "", 10)
	if err != nil || empty == nil || len(empty) != 0 || next != "" {
		t.Fatalf("empty: %+v %q %v", empty, next, err)
	}
	node, _, err := db.CreateNode(ctx, CreateNodeParams{Name: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Millisecond)
	expected := map[string]MaintenanceWindow{}
	for i := 0; i < 5; i++ {
		w, err := db.CreateMaintenanceWindow(ctx, MaintenanceWindow{Reason: "pagination", Timezone: "UTC", CreatedBy: "admin", NodeIDs: []string{node.ID}, StartsAt: at, EndsAt: at.Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if err := db.CancelMaintenanceWindow(ctx, w.ID, 1, at); err != nil {
				t.Fatal(err)
			}
			w.Revision = 2
			w.CanceledAt = &at
		}
		expected[w.ID] = w
	}
	if err := db.DeleteNode(ctx, node.ID); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	cursor := ""
	for page := 0; page < 4; page++ {
		items, next, err := db.ListMaintenanceWindows(ctx, cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range items {
			original, ok := expected[w.ID]
			if !ok || seen[w.ID] || w.ID <= cursor || len(w.NodeIDs) != 1 || w.NodeIDs[0] != node.ID || w.Revision != original.Revision || (w.CanceledAt == nil) != (original.CanceledAt == nil) || !w.StartsAt.Equal(at) {
				t.Fatalf("invalid page item: %+v", w)
			}
			seen[w.ID] = true
		}
		if next == "" {
			break
		}
		cursor = next
	}
	if len(seen) != 5 {
		t.Fatalf("missing windows: %d", len(seen))
	}
	for _, limit := range []int{0, 101} {
		if _, _, err := db.ListMaintenanceWindows(ctx, "", limit); !errors.Is(err, ErrInvalidMaintenance) {
			t.Fatalf("limit %d: %v", limit, err)
		}
	}
}

func TestMaintenanceBoundariesCancellationAndReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "maintenance.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	node, _, err := db.CreateNode(ctx, CreateNodeParams{Name: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)
	w := MaintenanceWindow{Reason: "planned work", Timezone: "Asia/Shanghai", StartsAt: at, EndsAt: at.Add(time.Hour), NodeIDs: []string{node.ID}, CreatedBy: "fixture-admin"}
	first, err := db.CreateMaintenanceWindow(ctx, w)
	if err != nil {
		t.Fatal(err)
	}
	check := func(at time.Time, want bool) {
		t.Helper()
		got, err := db.NodeInMaintenance(ctx, node.ID, at)
		if err != nil || got != want {
			t.Fatalf("at %s: %v want %v (%v)", at, got, want, err)
		}
	}
	check(at.Add(-time.Nanosecond), false)
	check(at, true)
	check(at.Add(time.Hour), false)
	if err := db.CancelMaintenanceWindow(ctx, first.ID, 2, at.Add(30*time.Minute)); !errors.Is(err, ErrMaintenanceConflict) {
		t.Fatalf("stale cancel: %v", err)
	}
	if err := db.CancelMaintenanceWindow(ctx, first.ID, 1, at.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	check(at.Add(29*time.Minute), true)
	check(at.Add(30*time.Minute), false)
	if err := db.CancelMaintenanceWindow(ctx, first.ID, 1, at); !errors.Is(err, ErrMaintenanceConflict) {
		t.Fatalf("repeat cancel: %v", err)
	}
	w.StartsAt = at.Add(20 * time.Minute)
	if _, err := db.CreateMaintenanceWindow(ctx, w); err != nil {
		t.Fatal(err)
	}
	check(at.Add(30*time.Minute), true)
	if err := db.DeleteNode(ctx, node.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	check(at, true)
	check(at.Add(30*time.Minute), true)
	check(at.Add(time.Hour), false)
	if _, err := db.CreateMaintenanceWindow(ctx, w); !errors.Is(err, ErrInvalidMaintenance) {
		t.Fatalf("deleted member accepted: %v", err)
	}
}

func TestMaintenanceRejectsInvalidScopeAndTime(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	node, _, err := db.CreateNode(ctx, CreateNodeParams{Name: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	for _, kind := range []string{"empty", "duplicate", "missing", "time", "timezone", "reason"} {
		w := MaintenanceWindow{Reason: "work", Timezone: "UTC", StartsAt: at, EndsAt: at.Add(time.Hour), NodeIDs: []string{node.ID}, CreatedBy: "admin"}
		switch kind {
		case "empty":
			w.NodeIDs = nil
		case "duplicate":
			w.NodeIDs = append(w.NodeIDs, node.ID)
		case "missing":
			w.NodeIDs = append(w.NodeIDs, "missing")
		case "time":
			w.EndsAt = at
		case "timezone":
			w.Timezone = "invalid/timezone"
		case "reason":
			w.Reason = "\n"
		}
		if _, err := db.CreateMaintenanceWindow(ctx, w); !errors.Is(err, ErrInvalidMaintenance) {
			t.Fatalf("%s: %v", kind, err)
		}
	}
	var count int
	if err := db.db.QueryRow(`SELECT count(*) FROM maintenance_windows`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial writes: %d %v", count, err)
	}
}

func TestMaintenanceHistoricalOverlap(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	node, _, err := db.CreateNode(ctx, CreateNodeParams{Name: "history"})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	w, err := db.CreateMaintenanceWindow(ctx, MaintenanceWindow{Reason: "work", Timezone: "UTC", CreatedBy: "admin", NodeIDs: []string{node.ID}, StartsAt: start, EndsAt: start.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	check := func(a, b time.Time, want bool) {
		t.Helper()
		got, err := db.NodeMaintenanceOverlaps(ctx, node.ID, a, b)
		if err != nil || got != want {
			t.Fatalf("[%s,%s): %v want %v (%v)", a, b, got, want, err)
		}
	}
	check(start.Add(-time.Hour), start, false)
	check(start.Add(-time.Hour), start.Add(time.Nanosecond), true)
	check(start.Add(-time.Hour), start.Add(2*time.Hour), true)
	check(start.Add(time.Hour), start.Add(2*time.Hour), false)
	check(start.Add(time.Hour-time.Nanosecond), start.Add(time.Hour), true)
	canceled := start.Add(20 * time.Minute)
	if err := db.CancelMaintenanceWindow(ctx, w.ID, 1, canceled); err != nil {
		t.Fatal(err)
	}
	check(start.Add(-time.Hour), start.Add(2*time.Hour), true)
	check(canceled, start.Add(time.Hour), false)
	check(canceled.Add(-time.Nanosecond), canceled, true)
	for _, at := range []time.Time{start.Add(-time.Second), start} {
		future, err := db.CreateMaintenanceWindow(ctx, MaintenanceWindow{Reason: "canceled before work", Timezone: "UTC", CreatedBy: "admin", NodeIDs: []string{node.ID}, StartsAt: start.Add(2 * time.Hour), EndsAt: start.Add(3 * time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		if err := db.CancelMaintenanceWindow(ctx, future.ID, 1, at); err != nil {
			t.Fatal(err)
		}
	}
	check(start.Add(time.Hour), start.Add(4*time.Hour), false)
	if _, err := db.NodeMaintenanceOverlaps(ctx, node.ID, start, start); !errors.Is(err, ErrInvalidMaintenance) {
		t.Fatalf("empty interval: %v", err)
	}
}
