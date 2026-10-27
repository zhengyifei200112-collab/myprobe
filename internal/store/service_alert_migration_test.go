package store

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"testing"
	"time"
)

func TestServiceAlertMigrationPreservesRulesAndLegacyHistory(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(httpFixtureDir(t), "pre034.db")
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() >= "034_" {
			continue
		}
		raw, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(raw)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)`, entry.Name(), nowText()); err != nil {
			t.Fatal(err)
		}
	}
	s := &Store{db: db, path: path}
	node, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "old observer"})
	if err != nil {
		t.Fatal(err)
	}
	channel, err := s.CreateNotificationChannel(ctx, "old channel", "webhook", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	rule, err := s.CreateAlertRule(ctx, node.ID, channel.ID, "cpu", []byte(`{"threshold_percent":90}`), 300)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Second)
	incident := observe(t, s, rule, node, at, true, true)
	if _, err := db.Exec(`INSERT INTO alert_events(id,rule_id,node_id,state,fingerprint,message,created_at,delivery_error) VALUES('old-event',?,?,'failed','old-fingerprint','retained-message',?,'http_server_error')`, rule.ID, node.ID, formatTime(at)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO alert_states(fingerprint,rule_id,node_id,active,last_message,last_attempt_at,updated_at,pending_since) VALUES('old-fingerprint',?,?,1,'old-state',?,?,?)`, rule.ID, node.ID, formatTime(at), formatTime(at), formatTime(at)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER fail_service_migration BEFORE INSERT ON schema_migrations WHEN NEW.version='034_service_alerts.sql' BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err == nil {
		t.Fatal("forced migration succeeded")
	}
	if rowCount(t, s, "alert_events") != 1 || rowCount(t, s, "alert_states") != 1 || rowCount(t, s, "alert_rules") != 1 {
		t.Fatal("migration failure erased old rows")
	}
	if _, err := s.CreateAlertRule(ctx, node.ID, channel.ID, "memory", []byte(`{}`), 300); err == nil {
		t.Fatal("rollback failed to restore old enum")
	}
	if _, err := db.Exec(`DROP TRIGGER fail_service_migration`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	current, err := s.AlertRule(ctx, rule.ID)
	if err != nil || current.Kind != rule.Kind || string(current.Config) != string(rule.Config) {
		t.Fatal("old rule changed")
	}
	var message, class, state, pending string
	if err := db.QueryRow(`SELECT message,delivery_error FROM alert_events WHERE id='old-event' AND rule_id=?`, rule.ID).Scan(&message, &class); err != nil || message != "retained-message" || class != "http_server_error" {
		t.Fatal("old receipt changed")
	}
	if err := db.QueryRow(`SELECT last_message,pending_since FROM alert_states WHERE fingerprint='old-fingerprint' AND rule_id=?`, rule.ID).Scan(&state, &pending); err != nil || state != "old-state" || pending != formatTime(at) {
		t.Fatal("legacy state changed")
	}
	jobs, err := s.ListIncidentDeliveries(ctx, incident.ID, 0, 10)
	if err != nil || len(jobs) != 1 || jobs[0].Status != "pending" {
		t.Fatal("old queue lost")
	}
	for _, kind := range []string{"memory", "disk", "latency"} {
		if _, err := s.CreateAlertRule(ctx, node.ID, channel.ID, kind, []byte(`{}`), 300); err != nil {
			t.Fatal("supported kind not admitted")
		}
	}
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("rule rebuild damaged foreign keys")
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
}
