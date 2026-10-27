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

// The current main-plus-timeline candidate has 041/047, while HTTP migrations
// 016-020 are integrated later. Migration receipts are tracked per filename.
func TestHTTPUpgradeAfterTimelinePreservesEvidenceAndRollsBackSeed(t *testing.T) {
	ctx := context.Background()
	file := filepath.Join(httpFixtureDir(t), "pre-http.db")
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
		// Service migrations depend on the HTTP tables absent in this fixture.
		if entry.IsDir() || name >= "016_" && name <= "020_z" || name >= "034_" && name < "039_" {
			continue
		}
		body, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.ExecContext(ctx, string(body)); err != nil {
			t.Fatal(err)
		}
		if _, err = db.ExecContext(ctx, "INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)", name, nowText()); err != nil {
			t.Fatal(err)
		}
	}
	s := &Store{db: db, path: file}
	node, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "existing main node"})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 1, 1, 0, 0, 0, 123, time.UTC)
	if err = s.SaveReport(ctx, node.ID, protocol.Report{CapturedAt: at, Uptime: 100}); err != nil {
		t.Fatal(err)
	}
	private := "{\"private\":\"preserved\"}"
	if _, err = db.Exec("INSERT INTO audit_log(action,object_type,object_id,details_json,created_at) VALUES('update','node',?,?,?)", node.ID, private, formatTime(at)); err != nil {
		t.Fatal(err)
	}
	before, err := s.NodeTimelineRange(ctx, node.ID, at, at.Add(time.Second), true, "", 50)
	if err != nil || len(before.Events) != 2 {
		t.Fatalf("pre-upgrade timeline: %+v %v", before, err)
	}
	var oldTimestamp, oldReport string
	if err = db.QueryRow("SELECT captured_at,report_json FROM metric_samples WHERE node_id=?", node.ID).Scan(&oldTimestamp, &oldReport); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TRIGGER fail018 BEFORE INSERT ON schema_migrations WHEN NEW.version='018_http_schedule_epochs.sql' BEGIN SELECT RAISE(ABORT,'synthetic receipt failure'); END"); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err == nil {
		t.Fatal("failed receipt was accepted")
	}
	assertCount(t, s, "SELECT count(*) FROM schema_migrations WHERE version IN ('016_http_services.sql','017_http_tasks.sql')", 2)
	assertCount(t, s, "SELECT count(*) FROM schema_migrations WHERE version IN ('018_http_schedule_epochs.sql','019_http_retention.sql','020_http_result_order.sql')", 0)
	assertCount(t, s, "SELECT count(*) FROM sqlite_master WHERE name='http_schedule_epochs'", 0)
	assertCount(t, s, "SELECT count(*) FROM schema_migrations WHERE version IN ('041_history_retention_indexes.sql','047_node_timeline_indexes.sql')", 2)
	// A previously saved service must be seeded atomically with the 018 receipt.
	spec := "{\"url\":\"https://example.com\",\"method\":\"GET\",\"status_codes\":[200],\"timeout_ms\":5000,\"max_body_bytes\":1024}"
	if _, err = db.Exec("INSERT INTO http_services(id,name,revision,enabled,interval_seconds,spec_json,created_at,updated_at) VALUES('existing-service','fixture',3,1,60,?,?,?)", spec, formatTime(at), formatTime(at)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO http_service_nodes(service_id,node_id) VALUES('existing-service',?)", node.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err == nil {
		t.Fatal("failed populated seed was accepted")
	}
	assertCount(t, s, "SELECT count(*) FROM sqlite_master WHERE name='http_schedule_epochs'", 0)
	if _, err = db.Exec("DROP TRIGGER fail018"); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	assertCount(t, s, "SELECT count(*) FROM schema_migrations WHERE version IN ('016_http_services.sql','017_http_tasks.sql','018_http_schedule_epochs.sql','019_http_retention.sql','020_http_result_order.sql')", 5)
	var start int64
	var revision, interval int
	if err = db.QueryRow("SELECT revision,start_ns,interval_seconds FROM http_schedule_epochs WHERE service_id='existing-service' AND node_id=?", node.ID).Scan(&revision, &start, &interval); err != nil {
		t.Fatal(err)
	}
	if revision != 3 || start != at.UnixNano() || interval != 60 {
		t.Fatalf("seed changed: revision=%d start=%d interval=%d", revision, start, interval)
	}
	var newTimestamp, newReport, details string
	if err = db.QueryRow("SELECT captured_at,report_json FROM metric_samples WHERE node_id=?", node.ID).Scan(&newTimestamp, &newReport); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("SELECT details_json FROM audit_log WHERE object_id=?", node.ID).Scan(&details); err != nil {
		t.Fatal(err)
	}
	if oldTimestamp != newTimestamp || oldReport != newReport || details != private {
		t.Fatal("existing raw evidence changed")
	}
	after, err := s.NodeTimelineRange(ctx, node.ID, at, at.Add(time.Second), true, "", 50)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("timeline changed: before=%+v after=%+v err=%v", before, after, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	again, err := reopened.NodeTimelineRange(ctx, node.ID, at, at.Add(time.Second), true, "", 50)
	if err != nil || !reflect.DeepEqual(before, again) {
		t.Fatalf("timeline lost on reopen: %+v %v", again, err)
	}
}
