package store

import (
	"context"
	"encoding/json"
	"testing"
)

func TestChartShareNodeMigrationBackfillsLegacyJSON(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	node, _, _ := database.CreateNode(ctx, CreateNodeParams{Name: "legacy"})
	share, err := database.CreateChartShare(ctx, "legacy", "hash", []string{node.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `DELETE FROM chart_share_nodes WHERE share_id=?`, share.ID); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal([]string{node.ID})
	if _, err := database.db.ExecContext(ctx, `UPDATE chart_shares SET node_filter_json=? WHERE id=?`, string(raw), share.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version='006_chart_share_nodes.sql'`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	share, err = database.ChartShare(ctx, share.ID)
	if err != nil || len(share.NodeIDs) != 1 || share.NodeIDs[0] != node.ID {
		t.Fatalf("share = %#v, error = %v", share, err)
	}
}
