package store

import (
	"context"
	"testing"
)

func TestNotificationTemplateLifecycleAndDefaults(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	items, err := database.ListNotificationTemplates(ctx)
	if err != nil || len(items) != 2 || !items[0].IsDefault {
		t.Fatalf("defaults = %#v, err = %v", items, err)
	}
	item, err := database.SaveNotificationTemplate(ctx, "", "Ops", "all", "Alert {{node.name}}", "{{message}}")
	if err != nil {
		t.Fatal(err)
	}
	item, err = database.SaveNotificationTemplate(ctx, item.ID, "Ops v2", "firing", "Alert", "Message")
	if err != nil || item.Name != "Ops v2" {
		t.Fatalf("updated = %#v, err = %v", item, err)
	}
	if err := database.DeleteNotificationTemplate(ctx, item.ID); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteNotificationTemplate(ctx, "default-firing"); err == nil {
		t.Fatal("default template was deleted")
	}
	if _, err := database.SaveNotificationTemplate(ctx, "", "unsafe", "all", "{{unknown}}", "message"); err == nil {
		t.Fatal("unknown template variable was accepted")
	}
}
