package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"time"

	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

func preHistoryIndexStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pre041.db")
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() >= "041_" {
			continue
		}
		body, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)`, entry.Name(), nowText()); err != nil {
			t.Fatal(err)
		}
	}
	return &Store{db: db, path: path}
}

func legacyHistoryStatements() retentionStatements {
	statements := standardRetentionStatements()
	removeHints := func(query string) string {
		for _, name := range historyRetentionIndexNames {
			query = strings.ReplaceAll(query, " INDEXED BY "+name, "")
		}
		query = strings.ReplaceAll(query, "captured_sort_ns", "captured_at")
		for _, column := range []string{"captured_at", "m.captured_at", "l.captured_at", "d.captured_at"} {
			query = strings.ReplaceAll(query, "unixepoch(substr("+column+",1,19)||'Z')", "unixepoch("+column+")")
		}
		return query
	}
	statements.rawMetrics = removeHints(statements.rawMetrics)
	statements.rawLatency = removeHints(statements.rawLatency)
	statements.rawTraffic = removeHints(statements.rawTraffic)
	statements.pruneMetrics = removeHints(statements.pruneMetrics)
	statements.legacyTime = true
	return statements
}

func seedPre041Report(t *testing.T, s *Store, nodeID string, report protocol.Report) {
	t.Helper()
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO metric_latest(node_id,captured_at,report_json) VALUES(?,?,?) ON CONFLICT(node_id) DO UPDATE SET captured_at=excluded.captured_at,report_json=excluded.report_json`, nodeID, formatTime(report.CapturedAt), string(encoded)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO metric_samples(node_id,captured_at,cpu_usage,memory_used,memory_total,disk_used,disk_total,net_rx_rate,net_tx_rate,net_rx_total,net_tx_total,report_json) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, nodeID, formatTime(report.CapturedAt), report.CPU.UsagePercent, 512, 1024, 1024, 2048, 40, 20, report.Networks[0].RXTotalBytes, report.Networks[0].TXTotalBytes, string(encoded)); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryIndexMigrationRollbackAndConsistentRestore(t *testing.T) {
	s := preHistoryIndexStore(t)
	ctx := context.Background()
	node, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "Synthetic upgrade"})
	if err != nil {
		t.Fatal(err)
	}
	report := historyCapacityReport(time.Now().UTC(), 3)
	seedPre041Report(t, s, node.ID, report)
	if _, err := s.db.Exec(`CREATE TRIGGER synthetic_041_failure BEFORE INSERT ON schema_migrations WHEN NEW.version='041_history_retention_indexes.sql' BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err == nil {
		t.Fatal("forced migration completed")
	}
	for _, name := range historyRetentionIndexNames {
		var count int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, name).Scan(&count); err != nil || count != 0 {
			t.Fatal("failed migration left a partial index set")
		}
	}
	assertCount(t, s, `SELECT COUNT(*) FROM metric_samples`, 1)
	assertCount(t, s, `SELECT COUNT(*) FROM schema_migrations WHERE version='041_history_retention_indexes.sql'`, 0)
	assertCount(t, s, `SELECT COUNT(*) FROM pragma_table_xinfo('metric_samples') WHERE name='captured_sort_ns'`, 0)
	assertCount(t, s, `SELECT COUNT(*) FROM pragma_table_xinfo('latency_samples') WHERE name='captured_sort_ns'`, 0)
	if _, err := s.db.Exec(`DROP TRIGGER synthetic_041_failure`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, name := range historyRetentionIndexNames {
		var count int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, name).Scan(&count); err != nil || count != 1 {
			t.Fatal("successful migration omitted a required index")
		}
	}
	path := filepath.Join(t.TempDir(), "restored.db")
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	actual, err := restored.LatestReport(ctx, node.ID)
	if err != nil || actual == nil || !actual.CapturedAt.Equal(report.CapturedAt) || actual.Networks[0].RXTotalBytes != report.Networks[0].RXTotalBytes {
		t.Fatal("consistent restore changed the original report")
	}
	if err := restored.ApplyRetention(ctx, time.Now().UTC(), DefaultRetentionPolicy()); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryIndexSelectionPreservesEpochAliasesAndCounterResets(t *testing.T) {
	ctx := context.Background()
	legacy := preHistoryIndexStore(t)
	node, _, err := legacy.CreateNode(ctx, CreateNodeParams{Name: "Synthetic equivalence"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	for _, age := range []time.Duration{40 * 24 * time.Hour, 10 * 24 * time.Hour, 24 * time.Hour} {
		for i, total := range []uint64{1000, 1200, 20, 90} {
			report := historyCapacityReport(now.Add(-age).Add(time.Duration(i)*15*time.Second), i)
			report.CPU.UsagePercent = float64(11 + i*13)
			report.Networks[0].RXTotalBytes = total
			seedPre041Report(t, legacy, node.ID, report)
		}
	}
	// An equivalent fractional timestamp already representing the raw bucket
	// must suppress a duplicate in both legacy and indexed epoch lookups.
	alias := now.Add(-10 * 24 * time.Hour).Format("2006-01-02T15:04:05.000Z")
	if _, err := legacy.db.Exec(`INSERT INTO metric_rollups VALUES(?,60,?,4,122,200,200,160,80)`, node.ID, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.db.Exec(`INSERT INTO traffic_rollups VALUES(?,60,?,290,1200)`, node.ID, alias); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "indexed.db")
	if _, err := legacy.db.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
		t.Fatal(err)
	}
	indexed, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer indexed.Close()
	if err := legacy.applyRetentionStatements(ctx, now, DefaultRetentionPolicy(), legacyHistoryStatements()); err != nil {
		t.Fatal(err)
	}
	if err := indexed.ApplyRetention(ctx, now, DefaultRetentionPolicy()); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`SELECT COUNT(*),COALESCE(SUM(sample_count),0),ROUND(COALESCE(SUM(cpu_sum),0),9) FROM metric_rollups`,
		`SELECT COUNT(*),COALESCE(SUM(rx_bytes),0),COALESCE(SUM(tx_bytes),0) FROM traffic_rollups`,
		`SELECT COUNT(*),COALESCE(SUM(net_rx_total),0),COALESCE(SUM(net_tx_total),0) FROM metric_samples`,
	} {
		var a, b [3]float64
		if err := legacy.db.QueryRow(query).Scan(&a[0], &a[1], &a[2]); err != nil {
			t.Fatal(err)
		}
		if err := indexed.db.QueryRow(query).Scan(&b[0], &b[1], &b[2]); err != nil {
			t.Fatal(err)
		}
		if a != b {
			t.Fatalf("indexed retention changed retained sums: %v != %v", a, b)
		}
	}
	if err := indexed.ApplyRetention(ctx, now.Add(2*24*time.Hour), DefaultRetentionPolicy()); err != nil {
		t.Fatal(err)
	}
	// Metadata/reports remain the same typed payload contract.
	actual, err := indexed.LatestReport(ctx, node.ID)
	if err != nil || actual == nil || actual.Validate() != nil {
		t.Fatal("report contract changed")
	}
}
