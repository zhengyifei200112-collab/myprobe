package store

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"testing"
)

// main already contains 041-044/047. Applying the older unused number040 must
// still be forward-only and atomic; MAX(version) is not an applied-file set.
func TestSamplingMigrationAfterNetworkAndTimelineRollsBack(t *testing.T) {
	ctx := context.Background()
	file := filepath.Join(httpFixtureDir(t), "pre-runtime-main.db")
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
		if entry.IsDir() || name == "040_agent_runtime_evidence.sql" {
			continue
		}
		body, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.ExecContext(ctx, string(body)); err != nil {
			t.Fatal(name, err)
		}
		if _, err = db.ExecContext(ctx, "INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)", name, nowText()); err != nil {
			t.Fatal(err)
		}
	}
	s := &Store{db: db, path: file}
	node, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "synthetic predecessor node", CollectionSeconds: 2, ReportSeconds: 3})
	if err != nil {
		t.Fatal(err)
	}
	// Seed the predecessor format directly: the current SaveReport requires040.
	const raw = `{"captured_at":"2026-01-01T00:00:00Z","uptime":17}`
	if _, err = db.ExecContext(ctx, "INSERT INTO metric_latest(node_id,captured_at,report_json) VALUES(?,?,?)", node.ID, "2026-01-01T00:00:00Z", raw); err != nil {
		t.Fatal(err)
	}
	assertCount(t, s, "SELECT COUNT(*) FROM schema_migrations WHERE version IN ('041_history_retention_indexes.sql','042_network_schedule_epochs.sql','043_network_probe_tasks.sql','044_network_quality_rollups.sql','047_node_timeline_indexes.sql')", 5)
	if _, err = db.ExecContext(ctx, "CREATE TRIGGER fail040 BEFORE INSERT ON schema_migrations WHEN NEW.version='040_agent_runtime_evidence.sql' BEGIN SELECT RAISE(ABORT,'synthetic receipt failure'); END"); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err == nil {
		t.Fatal("expected configuration migration failure")
	}
	assertCount(t, s, "SELECT COUNT(*) FROM pragma_table_info('nodes') WHERE name='agent_config_revision'", 0)
	assertCount(t, s, "SELECT COUNT(*) FROM sqlite_master WHERE name IN ('node_report_ingress','node_configuration_evidence','node_agent_config_revision')", 0)
	assertCount(t, s, "SELECT COUNT(*) FROM schema_migrations WHERE version='040_agent_runtime_evidence.sql'", 0)
	if _, err = db.ExecContext(ctx, "DROP TRIGGER fail040"); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	assertCount(t, s, "SELECT COUNT(*) FROM schema_migrations WHERE version='040_agent_runtime_evidence.sql'", 1)
	var stored string
	if err = db.QueryRowContext(ctx, "SELECT report_json FROM metric_latest WHERE node_id=?", node.ID).Scan(&stored); err != nil || stored != raw {
		t.Fatal("migration changed existing report bytes", err)
	}
	desired, err := s.DesiredSamplingConfiguration(ctx, node.ID)
	if err != nil || desired.Revision != 1 || desired.CollectionSeconds != 2 || desired.ReportSeconds != 3 {
		t.Fatal("upgrade changed desired sampling or revision", err)
	}
	evidence, err := s.SamplingConfigurationEvidence(ctx, node.ID)
	if err != nil || evidence.Status != "no_connection_evidence" || evidence.AppliedRevision != nil {
		t.Fatal("upgrade invented a historical configuration receipt", err)
	}
	ingress, err := s.LastReportIngress(ctx, node.ID)
	if err != nil || ingress.Status != "unavailable" {
		t.Fatal("upgrade invented a historical transport", err)
	}
	if _, err = db.ExecContext(ctx, "UPDATE nodes SET agent_config_revision=9223372036854775807 WHERE id=?", node.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, "UPDATE nodes SET collection_seconds=4 WHERE id=?", node.ID); err == nil {
		t.Fatal("revision overflow was accepted")
	}
	desired, err = s.DesiredSamplingConfiguration(ctx, node.ID)
	if err != nil || desired.CollectionSeconds != 2 || desired.Revision != 9223372036854775807 {
		t.Fatal("overflow did not roll back the settings update", err)
	}
}
