package store

import (
	"context"
	"strings"
	"testing"
)

func TestSiteSettingsRoundTripAndSanitize(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	updated, err := database.UpdateSiteSettings(ctx, SiteSettings{
		AgentURL:        " https://agent.example.com/probe/ ",
		SiteTitle:       " Edge Fleet ",
		SiteDescription: " Global nodes ",
		HeaderHTML:      `<p><strong>Welcome</strong><script>alert(1)</script></p>`,
		FooterHTML:      `<a href="javascript:alert(2)" onclick="bad()">link</a>`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.AgentURL != "https://agent.example.com/probe" || updated.SiteTitle != "Edge Fleet" {
		t.Fatalf("normalized settings = %#v", updated)
	}
	for _, forbidden := range []string{"script", "javascript", "onclick", "alert("} {
		if strings.Contains(strings.ToLower(updated.HeaderHTML+updated.FooterHTML), forbidden) {
			t.Fatalf("settings contain %q: %#v", forbidden, updated)
		}
	}
	loaded, err := database.GetSiteSettings(ctx)
	if err != nil || loaded != updated {
		t.Fatalf("loaded settings = %#v, error = %v", loaded, err)
	}
}

func TestSiteSettingsRejectInvalidAgentURL(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, value := range []string{"ws://example.com", "https://user:secret@example.com", "https://example.com?q=secret"} {
		if _, err := database.UpdateSiteSettings(ctx, SiteSettings{AgentURL: value}); err == nil {
			t.Fatalf("invalid Agent URL %q was accepted", value)
		}
	}
}

func TestDirectTargetAssignmentRoundTrip(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	node, _, err := database.CreateNode(ctx, CreateNodeParams{Name: "direct"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := database.CreateTarget(ctx, CreateTargetParams{Name: "ping", Kind: "ping", Host: "1.1.1.1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AssignTarget(ctx, node.ID, target.ID); err != nil {
		t.Fatal(err)
	}
	assignments, err := database.ListTargetAssignments(ctx)
	if err != nil || len(assignments) != 1 || assignments[0].NodeID != node.ID || assignments[0].Target.ID != target.ID {
		t.Fatalf("assignments = %#v, error = %v", assignments, err)
	}
	items, err := database.ListNodeTargets(ctx)
	if err != nil || len(items) != 1 || items[0].TargetID != target.ID {
		t.Fatalf("node targets = %#v, error = %v", items, err)
	}
	if err := database.UnassignTarget(ctx, node.ID, target.ID); err != nil {
		t.Fatal(err)
	}
	assignments, err = database.ListTargetAssignments(ctx)
	if err != nil || len(assignments) != 0 {
		t.Fatalf("assignments after removal = %#v, error = %v", assignments, err)
	}
}
