package store

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"testing"
	"time"
)

func TestChannelPauseMigrationPreservesOldLeasesAndAssociations(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "pre-033.db")
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
		if entry.IsDir() || entry.Name() >= "033_" {
			continue
		}
		raw, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, string(raw)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)`, entry.Name(), nowText()); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Now().UTC().Truncate(time.Second)
	for _, query := range []string{
		`INSERT INTO notification_channels(id,name,kind,provider,config_encrypted,created_at,updated_at) VALUES('channel','old','webhook','webhook','private-old-config','2026-10-13T00:00:00Z','2026-10-13T00:00:00Z')`,
		`INSERT INTO alert_incidents(id,fingerprint,rule_id,node_id,node_name,kind,rule_snapshot_json,state,started_at,fired_at,updated_at) VALUES('incident','fixture','rule','node','fixture','cpu','{}','firing','2026-10-13T00:00:00Z','2026-10-13T00:00:00Z','2026-10-13T00:00:00Z')`,
		`INSERT INTO notification_aggregates(id,policy_id,policy_revision,channel_id,provider,notification_type,starts_at_ms,ends_at_ms,status,sealed,attempt_count,available_at_ms,lease_token,lease_until_ms,created_at,updated_at) VALUES('group','policy',1,'channel','webhook','firing',0,1,'inflight',1,1,1,'owner',9999999999999,'2026-10-13T00:00:00Z','2026-10-13T00:00:00Z')`,
		`INSERT INTO notification_deliveries(id,incident_id,channel_id,channel_name,provider,notification_type,idempotency_key,payload_json,status,attempt_count,available_at_ms,lease_token,lease_until_ms,created_at,updated_at,aggregate_id) VALUES('delivery','incident','channel','old','webhook','firing','logical-key','{}','inflight',1,1,'owner',9999999999999,'2026-10-13T00:00:00Z','2026-10-13T00:00:00Z','group')`,
		`INSERT INTO delivery_attempts(id,delivery_id,attempt_number,lease_token,started_at,outcome) VALUES(73,'delivery',1,'owner','2026-10-13T00:00:00Z','started')`,
		`UPDATE sqlite_sequence SET seq=150 WHERE name='delivery_attempts'`,
		`INSERT INTO notification_aggregate_attempts(aggregate_id,attempt_number,lease_token,started_at,outcome,idempotency_key,payload_json) VALUES('group',1,'owner','2026-10-13T00:00:00Z','started','transport-key','{"title":"frozen legacy body"}')`,
		`INSERT INTO notification_aggregate_attempt_members(aggregate_id,aggregate_attempt,delivery_id,delivery_attempt,included) VALUES('group',1,'delivery',1,1)`,
		`CREATE TRIGGER fail_migration_record BEFORE INSERT ON schema_migrations WHEN NEW.version='033_channel_delivery_backpressure.sql' BEGIN SELECT RAISE(ABORT,'fixture'); END`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	s := &Store{db: db, path: path}
	if err := s.Migrate(ctx); err == nil {
		t.Fatal("migration failure committed")
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM notification_aggregate_attempt_members WHERE included=1`).Scan(&count); err != nil || count != 1 {
		t.Fatal("migration rollback lost links")
	}
	if _, err := db.Exec(`INSERT INTO delivery_attempts(delivery_id,attempt_number,lease_token,started_at,outcome) VALUES('delivery',6,'new','2026-10-13T00:00:00Z','started')`); err == nil {
		t.Fatal("failed migration changed old numbering constraint")
	}
	if _, err := db.Exec(`DROP TRIGGER fail_migration_record`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	d, err := s.Delivery(ctx, "delivery")
	if err != nil || d.AttemptCount != 1 || d.AttemptSequence != 1 || !d.SendAuthorized || d.LeaseToken != "owner" || d.IdempotencyKey != "logical-key" {
		t.Fatalf("legacy lease changed: %+v %v", d, err)
	}
	c, err := s.NotificationChannel(ctx, "channel")
	if err != nil || c.DeliveryRevision != 1 || c.DeliveryPausedUntil != nil {
		t.Fatal("migration invented a channel pause")
	}
	var id, seq int
	var payload, key string
	if err := db.QueryRow(`SELECT id FROM delivery_attempts WHERE delivery_id='delivery' AND attempt_number=1`).Scan(&id); err != nil || id != 73 {
		t.Fatal("attempt identity changed")
	}
	if err := db.QueryRow(`SELECT seq FROM sqlite_sequence WHERE name='delivery_attempts'`).Scan(&seq); err != nil || seq != 150 {
		t.Fatal("AUTOINCREMENT high-water mark lost")
	}
	if err := db.QueryRow(`SELECT payload_json,idempotency_key FROM notification_aggregate_attempts WHERE aggregate_id='group'`).Scan(&payload, &key); err != nil || payload != `{"title":"frozen legacy body"}` || key != "transport-key" {
		t.Fatal("old body or key changed")
	}
	if err := db.QueryRow(`SELECT count(*) FROM notification_aggregate_attempt_members WHERE included=1`).Scan(&count); err != nil || count != 1 {
		t.Fatal("migration lost exact membership")
	}
	if _, err := db.Exec(`INSERT INTO delivery_attempts(delivery_id,attempt_number,lease_token,started_at,outcome) VALUES('delivery',6,'new',?,'started')`, formatTime(at)); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT id FROM delivery_attempts WHERE attempt_number=6`).Scan(&id); err != nil || id != 151 {
		t.Fatal("new history reused an old ID")
	}
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign key violation")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
}
