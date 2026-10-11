package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

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
