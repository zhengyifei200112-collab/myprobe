package rollbackprep

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy # 测试.db")
	db := openTest(t, path)
	entries, err := os.ReadDir("../store/migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() > LegacyVersion {
			continue
		}
		body, err := os.ReadFile(filepath.Join("../store/migrations", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(string(body)); err != nil {
			t.Fatalf("%s: %v", entry.Name(), err)
		}
		if _, err = db.Exec("INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)", entry.Name(), "2026-01-01T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		`INSERT INTO users VALUES('user','operator','synthetic-password-hash','2026-01-01','2026-01-01')`,
		`INSERT INTO nodes(id,name,created_at,updated_at) VALUES('node','Synthetic node','2026-01-01','2026-01-01')`,
		`INSERT INTO agent_tokens(id,node_id,token_hash,created_at) VALUES('token','node','synthetic-token-hash','2026-01-01')`,
		`INSERT INTO metric_samples(node_id,captured_at,cpu_usage,memory_used,memory_total,disk_used,disk_total,net_rx_rate,net_tx_rate,net_rx_total,net_tx_total,report_json) VALUES('node','2026-01-01T00:00:00Z',1.25,2,4,3,6,0,0,42,84,'{"synthetic":true}')`,
		`INSERT INTO audit_log(user_id,action,object_type,created_at) VALUES('user','fixture','node','2026-01-01')`,
		`INSERT INTO settings VALUES('typed-fixture',X'0001FF','2026-01-01')`,
		`INSERT INTO metric_rollups VALUES('node',60,'2026-01-01T00:00:00Z',1,1.25,50,50,0,0)`,
	} {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func openTest(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+url.PathEscape(filepath.ToSlash(path))+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
func digestTest(t *testing.T, path string) string {
	t.Helper()
	s, err := fileSHA(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPreparePreservesLegacyBackupAndPublishesSeparateCopy(t *testing.T) {
	source := fixture(t)
	expected := digestTest(t, source)
	target := filepath.Join(t.TempDir(), "prepared.db")
	result, err := Prepare(context.Background(), source, target, expected)
	if err != nil {
		t.Fatal(err)
	}
	if digestTest(t, source) != expected || result.SourceSHA256 != expected || result.PreparedSHA256 != digestTest(t, target) || result.Reports != 1 || result.SchemaVersion != LegacyVersion {
		t.Fatalf("invalid receipt: %+v", result)
	}
	db := openTest(t, target)
	for q, want := range map[string]string{
		`SELECT password_hash FROM users WHERE id='user'`:                "synthetic-password-hash",
		`SELECT token_hash FROM agent_tokens WHERE id='token'`:           "synthetic-token-hash",
		`SELECT report_json FROM metric_samples`:                         `{"synthetic":true}`,
		`SELECT hex(value_json) FROM settings WHERE key='typed-fixture'`: "0001FF",
		`SELECT MAX(version) FROM schema_migrations`:                     LegacyVersion,
		`SELECT action FROM audit_log`:                                   "fixture",
	} {
		var got string
		if err := db.QueryRow(q).Scan(&got); err != nil || got != want {
			t.Fatalf("%s: %q, %v", q, got, err)
		}
	}
	for _, name := range result.Indexes {
		var n int
		if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?", name).Scan(&n); err != nil || n != 1 {
			t.Fatal(name, n, err)
		}
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM metric_rollups WHERE node_id='node' AND bucket_seconds=60 AND unixepoch(bucket_at)=unixepoch('2026-01-01T00:00:00Z')").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	// An already prepared copy can be checked into another new destination;
	// receipts and table rows remain exactly the same, without app migration 041.
	again, err := Prepare(context.Background(), target, filepath.Join(t.TempDir(), "again.db"), result.PreparedSHA256)
	if err != nil || again.ContentSHA256 != result.ContentSHA256 {
		t.Fatal(again, err)
	}
}

func TestPrepareRejectsUnsafeOrUnsupportedInputsWithoutPublishing(t *testing.T) {
	for _, scenario := range []string{"wrong hash", "existing destination", "live WAL", "newer schema", "conflicting index", "canceled context", "source symlink"} {
		t.Run(scenario, func(t *testing.T) {
			source := fixture(t)
			target := filepath.Join(t.TempDir(), "prepared.db")
			ctx := context.Background()
			var want error
			switch scenario {
			case "existing destination":
				if err := os.WriteFile(target, []byte("keep-existing"), 0600); err != nil {
					t.Fatal(err)
				}
				want = os.ErrExist
			case "live WAL":
				if err := os.WriteFile(source+"-wal", []byte("not checkpointed"), 0600); err != nil {
					t.Fatal(err)
				}
				want = ErrInvalidInput
			case "newer schema":
				db := openTest(t, source)
				if _, err := db.Exec("INSERT INTO schema_migrations VALUES('049_history_retention_anchors.sql','2026-01-01')"); err != nil {
					t.Fatal(err)
				}
				db.Close()
				want = ErrUnsupportedSchema
			case "conflicting index":
				db := openTest(t, source)
				if _, err := db.Exec("CREATE INDEX idx_metric_rollups_epoch_lookup ON metric_rollups(bucket_at)"); err != nil {
					t.Fatal(err)
				}
				db.Close()
				want = ErrExistingIndex
			case "canceled context":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				want = context.Canceled
			case "source symlink":
				link := filepath.Join(t.TempDir(), "link.db")
				if err := os.Symlink(source, link); err != nil {
					t.Skip("symlink unavailable:", err)
				}
				source = link
				want = ErrInvalidInput
			}
			expected := digestTest(t, source)
			supplied := expected
			if scenario == "wrong hash" {
				supplied = strings.Repeat("0", 64)
				want = ErrSourceChanged
			}
			if _, err := Prepare(ctx, source, target, supplied); !errors.Is(err, want) {
				t.Fatalf("got %v, want %v", err, want)
			}
			if digestTest(t, source) != expected {
				t.Fatal("source changed")
			}
			if scenario == "existing destination" {
				b, err := os.ReadFile(target)
				if err != nil || string(b) != "keep-existing" {
					t.Fatal("destination overwritten", err)
				}
			} else if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("failed preparation published a destination", err)
			}
			leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(target), ".myprobe-rollback-*"))
			if err != nil || len(leftovers) != 0 {
				t.Fatal("temporary files leaked", leftovers, err)
			}
		})
	}
}

func TestPrepareClosedWALSnapshotProducesStandaloneDatabase(t *testing.T) {
	source := fixture(t)
	db := openTest(t, source)
	if _, err := db.Exec("PRAGMA journal_mode=WAL; INSERT INTO settings VALUES('wal-fixture','42','2026-01-01')"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	expected := digestTest(t, source)
	target := filepath.Join(t.TempDir(), "prepared.db")
	result, err := Prepare(context.Background(), source, target, expected)
	if err != nil {
		t.Fatal(err)
	}
	if result.SourceSHA256 != digestTest(t, source) || result.PreparedSHA256 != digestTest(t, target) {
		t.Fatal("snapshot hash mismatch")
	}
	prepared := openTest(t, target)
	var got string
	if err := prepared.QueryRow("SELECT value_json FROM settings WHERE key='wal-fixture'").Scan(&got); err != nil || got != "42" {
		t.Fatal(got, err)
	}
	if err := prepared.Close(); err != nil {
		t.Fatal(err)
	}
	leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(target), ".myprobe-rollback-*"))
	if err != nil || len(leftovers) != 0 {
		t.Fatal("temporary SQLite files leaked", leftovers, err)
	}
}
