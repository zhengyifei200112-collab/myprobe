package store

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"testing"
	"time"

	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

func TestScheduledBackupMigrationAfterNetworkAndTimelineRollsBack(t *testing.T) {
	ctx := context.Background()
	file := filepath.Join(httpFixtureDir(t), "pre-scheduled-main.db")
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
		if entry.IsDir() || name == "039_scheduled_backups.sql" {
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
	node, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "synthetic pre-backup node"})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	if err = s.SaveReport(ctx, node.ID, protocol.Report{CapturedAt: at, Uptime: 17}); err != nil {
		t.Fatal(err)
	}
	var original string
	if err = db.QueryRowContext(ctx, "SELECT report_json FROM metric_latest WHERE node_id=?", node.ID).Scan(&original); err != nil {
		t.Fatal(err)
	}
	assertCount(t, s, "SELECT COUNT(*) FROM schema_migrations WHERE version IN ('041_history_retention_indexes.sql','042_network_schedule_epochs.sql','043_network_probe_tasks.sql','044_network_quality_rollups.sql','047_node_timeline_indexes.sql')", 5)
	if _, err = db.ExecContext(ctx, "CREATE TRIGGER fail039 BEFORE INSERT ON schema_migrations WHEN NEW.version='039_scheduled_backups.sql' BEGIN SELECT RAISE(ABORT,'synthetic receipt failure'); END"); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err == nil {
		t.Fatal("expected backup migration failure")
	}
	assertCount(t, s, "SELECT COUNT(*) FROM sqlite_master WHERE name IN ('backup_schedule_state','backup_runs','system_notifications')", 0)
	assertCount(t, s, "SELECT COUNT(*) FROM schema_migrations WHERE version='039_scheduled_backups.sql'", 0)
	if _, err = db.ExecContext(ctx, "DROP TRIGGER fail039"); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	config, err := s.BackupSchedule(ctx)
	if err != nil || config.Enabled || config.Directory != "" || config.Keep != 7 || config.Timezone != "UTC" {
		t.Fatal("upgrade enabled or invented a backup schedule", err)
	}
	var restored string
	if err = db.QueryRowContext(ctx, "SELECT report_json FROM metric_latest WHERE node_id=?", node.ID).Scan(&restored); err != nil || restored != original {
		t.Fatal("backup migration changed existing report bytes", err)
	}
	run, err := s.BeginBackupRun(ctx, "2026-10-28", nil, at)
	if err != nil || run == nil {
		t.Fatal("first daily claim failed", err)
	}
	if err = s.FinishBackupRun(ctx, run.ID, "failed", "backup_insufficient_space", nil, at); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if duplicate, err := reopened.BeginBackupRun(ctx, "2026-10-28", nil, at.Add(time.Second)); err != nil || duplicate != nil {
		t.Fatal("failed daily claim was replayed after reopen", err)
	}
}
