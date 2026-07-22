package store

import (
	"context"
	"strings"
	"testing"
)

func TestCustomDisplayIsValidatedSanitizedAndPublic(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	node, _, err := database.CreateNode(ctx, CreateNodeParams{Name: "custom"})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := database.UpdateNode(ctx, node.ID, UpdateNodeParams{
		Name: "custom", LatencyMode: "ping", CollectionSeconds: 5, ReportSeconds: 5,
		CustomBadges: []CustomBadge{{Label: " CN2 GIA ", Color: "GREEN"}},
		CustomLinks:  []CustomLink{{Label: "Status", URL: "https://status.example.com"}},
		CustomHTML:   `<p><strong>可用区 A</strong><script>alert(1)</script><a href="javascript:alert(2)" onclick="x()">bad</a></p>`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.CustomBadges) != 1 || updated.CustomBadges[0].Label != "CN2 GIA" || updated.CustomBadges[0].Color != "green" {
		t.Fatalf("badges = %#v", updated.CustomBadges)
	}
	if len(updated.CustomLinks) != 1 || updated.CustomLinks[0].URL != "https://status.example.com" {
		t.Fatalf("links = %#v", updated.CustomLinks)
	}
	for _, forbidden := range []string{"script", "javascript", "onclick", "alert("} {
		if strings.Contains(strings.ToLower(updated.CustomHTML), forbidden) {
			t.Fatalf("custom HTML contains %q: %s", forbidden, updated.CustomHTML)
		}
	}
	public, err := database.ListPublicNodes(ctx, updated.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(public) != 1 || len(public[0].Node.CustomBadges) != 1 || len(public[0].Node.CustomLinks) != 1 || public[0].Node.CustomHTML != updated.CustomHTML {
		t.Fatalf("public node = %#v", public)
	}
}

func TestCustomDisplayRejectsUnsafeStructuredLinks(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	node, _, err := database.CreateNode(ctx, CreateNodeParams{Name: "custom-invalid"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.UpdateNode(ctx, node.ID, UpdateNodeParams{Name: "custom-invalid", LatencyMode: "ping", CollectionSeconds: 5, ReportSeconds: 5, CustomLinks: []CustomLink{{Label: "unsafe", URL: "javascript:alert(1)"}}})
	if err == nil {
		t.Fatal("unsafe structured link was accepted")
	}
}
