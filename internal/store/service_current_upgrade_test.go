package store

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
)

// The current integration already applied timeline migrations 041/047; the
// service alert migrations are added later by filename without rewriting them.
func TestServiceUpgradeAfterTimelinePreservesIncidentAndOutbox(t *testing.T) {
	ctx := context.Background()
	file := filepath.Join(httpFixtureDir(t), "pre-service.db")
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
		if entry.IsDir() || name >= "034_" && name < "039_" {
			continue
		}
		raw, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.ExecContext(ctx, string(raw)); err != nil {
			t.Fatal(err)
		}
		if _, err = db.ExecContext(ctx, "INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)", name, nowText()); err != nil {
			t.Fatal(err)
		}
	}
	s := &Store{db: db, path: file}
	node, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "existing observer"})
	if err != nil {
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
	at := time.Now().UTC().Truncate(time.Millisecond)
	incident := observe(t, s, rule, node, at, true, true)
	before, err := s.ListIncidentDeliveries(ctx, incident.ID, 0, 10)
	if err != nil || len(before) != 1 {
		t.Fatalf("pre-upgrade outbox: %+v %v", before, err)
	}
	timeline, err := s.NodeTimelineRange(ctx, node.ID, at.Add(-time.Second), at.Add(time.Second), true, "", 50)
	if err != nil || len(timeline.Events) < 1 {
		t.Fatalf("pre-upgrade timeline: %+v %v", timeline, err)
	}
	service, err := s.SaveHTTPService(ctx, HTTPService{Name: "existing service", Enabled: true, IntervalSeconds: 60, NodeIDs: []string{node.ID}, Spec: httpcheck.Spec{URL: "https://example.test", Method: "GET", StatusCodes: []int{200}, TimeoutMS: 1000, MaxBodyBytes: 1024}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TRIGGER fail034 BEFORE INSERT ON schema_migrations WHEN NEW.version='034_service_alerts.sql' BEGIN SELECT RAISE(ABORT,'synthetic receipt failure'); END"); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err == nil {
		t.Fatal("failed receipt was accepted")
	}
	assertCount(t, s, "SELECT count(*) FROM sqlite_master WHERE name='http_service_alerts'", 0)
	assertCount(t, s, "SELECT count(*) FROM schema_migrations WHERE version IN ('034_service_alerts.sql','035_certificate_reminders.sql','036_http_diagnostics_index.sql','037_service_alert_imports.sql','038_http_retention_catchup.sql')", 0)
	assertCount(t, s, "SELECT count(*) FROM schema_migrations WHERE version IN ('041_history_retention_indexes.sql','047_node_timeline_indexes.sql')", 2)
	afterFailure, err := s.ListIncidentDeliveries(ctx, incident.ID, 0, 10)
	if err != nil || !reflect.DeepEqual(before, afterFailure) {
		t.Fatalf("rollback changed outbox: %+v %v", afterFailure, err)
	}
	if _, err = db.Exec("DROP TRIGGER fail034"); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	assertCount(t, s, "SELECT count(*) FROM schema_migrations WHERE version IN ('034_service_alerts.sql','035_certificate_reminders.sql','036_http_diagnostics_index.sql','037_service_alert_imports.sql','038_http_retention_catchup.sql')", 5)
	after, err := s.ListIncidentDeliveries(ctx, incident.ID, 0, 10)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("upgrade changed outbox: %+v %v", after, err)
	}
	newTimeline, err := s.NodeTimelineRange(ctx, node.ID, at.Add(-time.Second), at.Add(time.Second), true, "", 50)
	if err != nil || !reflect.DeepEqual(timeline, newTimeline) {
		t.Fatalf("upgrade changed timeline: %+v %v", newTimeline, err)
	}
	if _, err = s.SaveServiceAlertSettings(ctx, ServiceAlertSettings{ServiceID: service.ID, ServiceRevision: service.Revision, ChannelID: channel.ID, Enabled: true, CooldownSeconds: 30}); err != nil {
		t.Fatal(err)
	}
	rows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		rows.Close()
		t.Fatal("foreign-key damage")
	}
	if err = rows.Close(); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	replayed, err := reopened.ListIncidentDeliveries(ctx, incident.ID, 0, 10)
	if err != nil || !reflect.DeepEqual(before, replayed) {
		t.Fatalf("outbox changed on reopen: %+v %v", replayed, err)
	}
}
