package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestMaintenanceSelectionPreview(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, n := range []CreateNodeParams{{ID: "a", Name: "A", Tags: []string{"prod", "east"}}, {ID: "b", Name: "B", Tags: []string{"prod"}}, {ID: "c", Name: "C", Tags: []string{"west"}}} {
		if _, _, err := db.CreateNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name      string
		selection MaintenanceSelection
		ids       string
	}{
		{"explicit", MaintenanceSelection{Kind: "nodes", NodeIDs: []string{"b", "a"}}, "ab"},
		{"all", MaintenanceSelection{Kind: "tags", Tags: []string{"prod", "east"}, TagMode: "all"}, "a"},
		{"any", MaintenanceSelection{Kind: "tags", Tags: []string{"prod", "west"}, TagMode: "any"}, "abc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			members, err := db.PreviewMaintenanceMembers(ctx, tc.selection)
			if err != nil {
				t.Fatal(err)
			}
			ids := ""
			for _, member := range members {
				ids += member.ID
				if member.Name == "" {
					t.Fatal("missing display name")
				}
			}
			if ids != tc.ids {
				t.Fatalf("members=%s want %s", ids, tc.ids)
			}
		})
	}
	for _, selection := range []MaintenanceSelection{
		{}, {Kind: "nodes"}, {Kind: "nodes", NodeIDs: []string{"a", "missing"}}, {Kind: "nodes", NodeIDs: []string{"a", "a"}},
		{Kind: "nodes", NodeIDs: []string{"a"}, TagMode: "all"}, {Kind: "tags", Tags: []string{"prod"}},
		{Kind: "tags", Tags: []string{"absent"}, TagMode: "any"}, {Kind: "tags", Tags: []string{"prod", "prod"}, TagMode: "all"},
		{Kind: "tags", Tags: []string{" prod"}, TagMode: "any"}, {Kind: "tags", Tags: []string{"prod"}, TagMode: "all", NodeIDs: []string{"a"}},
	} {
		if _, err := db.PreviewMaintenanceMembers(ctx, selection); !errors.Is(err, ErrInvalidMaintenance) {
			t.Fatalf("invalid %+v: %v", selection, err)
		}
	}
	selection := MaintenanceSelection{Kind: "tags", Tags: []string{"east"}, TagMode: "all"}
	before, err := db.PreviewMaintenanceMembers(ctx, selection)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, `UPDATE nodes SET tags_json='["east"]' WHERE id='b'`); err != nil {
		t.Fatal(err)
	}
	after, err := db.PreviewMaintenanceMembers(ctx, selection)
	if err != nil || len(before) != 1 || len(after) != 2 {
		t.Fatalf("changed selection: %v %v %v", before, after, err)
	}
	windows, _, err := db.ListMaintenanceWindows(ctx, "", 10)
	if err != nil || len(windows) != 0 {
		t.Fatalf("preview wrote windows: %v %v", windows, err)
	}
}

func TestMaintenanceSelectionRejectsOversizedMatch(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i := 0; i < 101; i++ {
		if _, _, err := db.CreateNode(ctx, CreateNodeParams{ID: fmt.Sprintf("node-%03d", i), Name: "fixture", Tags: []string{"prod"}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.PreviewMaintenanceMembers(ctx, MaintenanceSelection{Kind: "tags", Tags: []string{"prod"}, TagMode: "all"}); !errors.Is(err, ErrMaintenanceLimit) {
		t.Fatalf("over capacity: %v", err)
	}
}
