package store

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
)

func TestCertificateMigrationPreservesRulesAndLegacyHistory(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(httpFixtureDir(t), "pre035.db")
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
		if entry.Name() >= "035_" {
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
	service, err := s.SaveHTTPService(ctx, HTTPService{Name: "old service", Enabled: true, IntervalSeconds: 30, NodeIDs: []string{node.ID}, Spec: httpcheck.Spec{URL: "https://example.test/", Method: "GET", StatusCodes: []int{200}, TimeoutMS: 1000, MaxBodyBytes: 1024}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO http_service_alerts(service_id,revision,channel_id,enabled,duration_seconds,recovery_seconds,repeat_seconds,cooldown_seconds,updated_at) VALUES(?,1,?,1,0,0,0,300,?)`, service.ID, channel.ID, nowText()); err != nil {
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
	if _, err := db.Exec(`CREATE TRIGGER fail_service_migration BEFORE INSERT ON schema_migrations WHEN NEW.version='035_certificate_reminders.sql' BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err == nil {
		t.Fatal("forced migration succeeded")
	}
	if rowCount(t, s, "alert_events") != 1 || rowCount(t, s, "alert_states") != 1 || rowCount(t, s, "alert_rules") != 1 {
		t.Fatal("migration failure erased old rows")
	}
	var newTable int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='http_certificate_reminders'`).Scan(&newTable); err != nil || newTable != 0 {
		t.Fatal("failed upgrade left certificate table")
	}
	insertCertificateKind := func() error {
		_, err := db.Exec(`INSERT INTO alert_rules(id,node_id,channel_id,kind,config_json,enabled,cooldown_seconds,created_at,updated_at) VALUES('certificate-enum',?,?,'service_certificate','{}',0,300,?,?)`, node.ID, channel.ID, formatTime(at), formatTime(at))
		return err
	}
	if err := insertCertificateKind(); err == nil {
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
	settings, err := s.ServiceAlertSettings(ctx, service.ID)
	if err != nil || settings.CertificateRemindersEnabled || !settings.Enabled || settings.Revision != 1 || settings.ChannelID != channel.ID {
		t.Fatal("upgrade changed old settings or enabled reminders")
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
	if err := insertCertificateKind(); err != nil {
		t.Fatal("new owned kind not admitted", err)
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
