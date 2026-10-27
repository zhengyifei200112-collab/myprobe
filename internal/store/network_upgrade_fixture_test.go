package store

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

// Reconstruct the actual parent schema, including already-applied 041/047.
// Network ledger/rollup migrations are absent; old receipts are not renumbered.
func preNetworkLedgerStore(t *testing.T) *Store {
	t.Helper()
	file := filepath.Join(httpFixtureDir(t), "pre-network.db")
	db, err := sql.Open("sqlite", sqliteDSN(file))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || name == "042_network_schedule_epochs.sql" || name == "043_network_probe_tasks.sql" || name == "044_network_quality_rollups.sql" {
			continue
		}
		body, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(context.Background(), string(body)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)", name, nowText()); err != nil {
			t.Fatal(err)
		}
	}
	return &Store{db: db, path: file}
}

func TestNetworkUpgradePreservesExistingOutboxAndTimeline(t *testing.T) {
	ctx := context.Background()
	s := preNetworkLedgerStore(t)
	node, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "existing network observer"})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(-time.Second)
	if err := s.SaveReport(ctx, node.ID, protocol.Report{CapturedAt: at, Uptime: 100}); err != nil {
		t.Fatal(err)
	}
	channel, err := s.CreateNotificationChannel(ctx, "existing channel", "webhook", "synthetic-config")
	if err != nil {
		t.Fatal(err)
	}
	rule, err := s.CreateAlertRule(ctx, node.ID, channel.ID, "cpu", []byte("{\"threshold_percent\":90}"), 300)
	if err != nil {
		t.Fatal(err)
	}
	incident := observe(t, s, rule, node, at, true, true)
	before, err := s.ListIncidentDeliveries(ctx, incident.ID, 0, 10)
	if err != nil || len(before) != 1 {
		t.Fatalf("existing outbox: %+v %v", before, err)
	}
	timeline, err := s.NodeTimelineRange(ctx, node.ID, at.Add(-time.Second), at.Add(3*time.Second), true, "", 50)
	if err != nil || len(timeline.Events) == 0 {
		t.Fatalf("existing timeline: %+v %v", timeline, err)
	}
	for range 2 {
		if err := s.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	assertCount(t, s, "SELECT COUNT(*) FROM schema_migrations WHERE version IN ('041_history_retention_indexes.sql','047_node_timeline_indexes.sql')", 2)
	assertCount(t, s, "SELECT COUNT(*) FROM schema_migrations WHERE version IN ('042_network_schedule_epochs.sql','043_network_probe_tasks.sql')", 2)
	file := s.path
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	after, err := reopened.ListIncidentDeliveries(ctx, incident.ID, 0, 10)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("network upgrade changed outbox: %+v %v", after, err)
	}
	afterTimeline, err := reopened.NodeTimelineRange(ctx, node.ID, at.Add(-time.Second), at.Add(3*time.Second), true, "", 50)
	if err != nil || !reflect.DeepEqual(timeline, afterTimeline) {
		t.Fatalf("network upgrade changed timeline: %+v %v", afterTimeline, err)
	}
}
