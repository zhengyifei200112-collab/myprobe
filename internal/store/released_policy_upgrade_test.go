package store

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Read the immutable release tree rather than reconstructing an old schema from
// today's migration files. This needs Git history, so shallow ordinary CI skips it.
func TestReleased021SchemaUpgradeToPolicies(t *testing.T) {
	if os.Getenv("MYPROBE_TEST_RELEASE_UPGRADE") != "1" {
		t.Skip("requires pinned v0.2.1 Git history")
	}
	const release = "532352963ab47aed4af730c400f25163d0e6ed45"
	ctx := context.Background()
	git := func(args ...string) []byte {
		t.Helper()
		command := exec.CommandContext(ctx, "git", args...)
		command.Dir = "../.."
		out, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("read release tree: %v %s", err, out)
		}
		return out
	}
	files := strings.Fields(string(git("ls-tree", "-r", "--name-only", release, "internal/store/migrations")))
	if len(files) != 10 {
		t.Fatalf("release migrations: %d", len(files))
	}
	path := filepath.Join(t.TempDir(), "release.db")
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	at := time.Date(2026, 7, 22, 12, 0, 0, 456000000, time.UTC)
	for _, file := range files {
		if _, err := db.ExecContext(ctx, string(git("show", release+":"+file))); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, "INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)", filepath.Base(file), formatTime(at)); err != nil {
			t.Fatal(err)
		}
	}
	for _, statement := range []string{
		`INSERT INTO nodes(id,name,created_at,updated_at) VALUES('node','release fixture',?,?)`,
		`INSERT INTO notification_channels(id,name,kind,config_encrypted,created_at,updated_at) VALUES('channel','fixture','webhook','synthetic',?,?)`,
		`INSERT INTO alert_rules(id,node_id,channel_id,kind,config_json,cooldown_seconds,created_at,updated_at) VALUES('rule','node','channel','cpu','{"threshold_percent":90}',900,?,?)`,
	} {
		if _, err := db.ExecContext(ctx, statement, formatTime(at), formatTime(at)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO alert_states(fingerprint,rule_id,node_id,active,last_message,last_attempt_at,last_error,updated_at) VALUES('cpu:rule:node','rule','node',1,'fixture fault',?,'delivery failed',?)`, formatTime(at), formatTime(at)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	for i, want := range []int{1, 0} {
		count, err := upgraded.PrepareAlertPolicyRules(ctx, at.Add(time.Duration(i+1)*time.Second))
		if err != nil || count != want {
			t.Fatalf("prepare %d: %d %v", i, count, err)
		}
	}
	policies, err := upgraded.ListAlertPolicies(ctx)
	if err != nil || len(policies) != 1 || policies[0].ID != "rule" {
		t.Fatalf("policies: %+v %v", policies, err)
	}
	incidents, err := upgraded.ListIncidents(ctx, IncidentFilter{Limit: 10})
	if err != nil || len(incidents) != 1 {
		t.Fatalf("incidents: %+v %v", incidents, err)
	}
	jobs, err := upgraded.ListIncidentDeliveries(ctx, incidents[0].ID, 0, 10)
	if err != nil || len(jobs) != 1 || !jobs[0].AvailableAt.Equal(at.Add(900*time.Second)) {
		t.Fatalf("retry: %+v %v", jobs, err)
	}
	var integrity string
	if err := upgraded.db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity: %s %v", integrity, err)
	}
	rows, err := upgraded.db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign key violation after release upgrade")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
