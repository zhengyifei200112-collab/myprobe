package store

import (
	"context"
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func preNodeBatchIntegrationStore(t *testing.T, legacy bool) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pre046.db")
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	apply := func(name string, body []byte) {
		t.Helper()
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)`, name, nowText()); err != nil {
			t.Fatal(err)
		}
	}
	for _, entry := range entries {
		if entry.Name() >= "046_" {
			continue
		}
		if legacy && entry.Name() == "015_incident_delivery.sql" {
			body, err := os.ReadFile(filepath.Join("testdata", "014_node_batches.sql"))
			if err != nil {
				t.Fatal(err)
			}
			apply("014_node_batches.sql", body)
		}
		body, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		apply(entry.Name(), body)
	}
	if legacy {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version='014_node_batches.sql'`).Scan(&count); err != nil || count != 1 {
			t.Fatal("legacy fixture was not installed", err)
		}
	}
	return &Store{db: db, path: path}
}

func TestNodeBatchForwardUpgradeAndLegacyReplay(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "current045", true: "early014"}[legacy], func(t *testing.T) {
			ctx := context.Background()
			s := preNodeBatchIntegrationStore(t, legacy)
			user, err := s.CreateUser(ctx, "synthetic administrator", "fixture-hash")
			if err != nil {
				t.Fatal(err)
			}
			node, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "preserved node", Tags: []string{"old"}})
			if err != nil {
				t.Fatal(err)
			}
			at := time.Now().UTC()
			var applied, pending NodeBatchPreview
			var original NodeBatchResult
			if legacy {
				applied, err = s.PreviewNodeBatch(ctx, user.ID, NodeBatchRequest{NodeIDs: []string{node.ID}, AddTags: []string{"applied"}}, at)
				if err != nil {
					t.Fatal(err)
				}
				original, err = s.ApplyNodeBatch(ctx, user.ID, applied.ID, "legacy-result-replay-key", at)
				if err != nil {
					t.Fatal(err)
				}
				pending, err = s.PreviewNodeBatch(ctx, user.ID, NodeBatchRequest{NodeIDs: []string{node.ID}, AddTags: []string{"pending"}}, at)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			if err := s.Migrate(ctx); err != nil {
				t.Fatal("reopen migration is not idempotent", err)
			}
			assertCount(t, s, `SELECT COUNT(*) FROM schema_migrations WHERE version='046_node_batches.sql'`, 1)
			assertCount(t, s, `SELECT COUNT(*) FROM nodes WHERE name='preserved node'`, 1)
			if legacy {
				assertCount(t, s, `SELECT COUNT(*) FROM schema_migrations WHERE version='014_node_batches.sql'`, 1)
				replay, err := s.ApplyNodeBatch(ctx, user.ID, applied.ID, "legacy-result-replay-key", at.Add(time.Hour))
				if err != nil || !reflect.DeepEqual(replay, original) {
					t.Fatal("legacy result changed", err)
				}
				if _, err := s.ApplyNodeBatch(ctx, user.ID, pending.ID, "legacy-pending-apply-key", at.Add(time.Minute)); err != nil {
					t.Fatal("pending legacy preview was not preserved", err)
				}
			} else {
				preview, err := s.PreviewNodeBatch(ctx, user.ID, NodeBatchRequest{NodeIDs: []string{node.ID}, AddTags: []string{"new"}}, at)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.ApplyNodeBatch(ctx, user.ID, preview.ID, "forward-batch-apply-key", at); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestNodeBatchMigrationRollbackAndUnrecognizedColumn(t *testing.T) {
	ctx := context.Background()
	t.Run("legacy receipt cannot recreate lost revisions", func(t *testing.T) {
		s := preNodeBatchIntegrationStore(t, true)
		if _, err := s.db.Exec(`DROP TRIGGER nodes_config_revision; DROP TRIGGER node_targets_revision_insert; DROP TRIGGER node_targets_revision_delete; ALTER TABLE nodes DROP COLUMN config_revision`); err != nil {
			t.Fatal(err)
		}
		if err := s.Migrate(ctx); err == nil {
			t.Fatal("missing legacy revision state must not reset to zero")
		}
		assertCount(t, s, `SELECT COUNT(*) FROM schema_migrations WHERE version='046_node_batches.sql'`, 0)
		assertCount(t, s, `SELECT COUNT(*) FROM pragma_table_info('nodes') WHERE name='config_revision'`, 0)
	})
	t.Run("receipt failure rolls back DDL", func(t *testing.T) {
		s := preNodeBatchIntegrationStore(t, false)
		if _, err := s.db.Exec(`CREATE TRIGGER fail046 BEFORE INSERT ON schema_migrations WHEN NEW.version='046_node_batches.sql' BEGIN SELECT RAISE(ABORT,'synthetic 046 failure'); END`); err != nil {
			t.Fatal(err)
		}
		if err := s.Migrate(ctx); err == nil {
			t.Fatal("expected migration failure")
		}
		assertCount(t, s, `SELECT COUNT(*) FROM pragma_table_info('nodes') WHERE name='config_revision'`, 0)
		assertCount(t, s, `SELECT COUNT(*) FROM sqlite_schema WHERE name='node_batch_previews'`, 0)
		assertCount(t, s, `SELECT COUNT(*) FROM schema_migrations WHERE version='046_node_batches.sql'`, 0)
		if _, err := s.db.Exec(`DROP TRIGGER fail046`); err != nil {
			t.Fatal(err)
		}
		if err := s.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	})
	for _, declaration := range []string{"TEXT NOT NULL DEFAULT 'unknown'", "INTEGER NOT NULL DEFAULT 0"} {
		t.Run(declaration, func(t *testing.T) {
			s := preNodeBatchIntegrationStore(t, false)
			if _, err := s.db.Exec(`ALTER TABLE nodes ADD COLUMN config_revision ` + declaration); err != nil {
				t.Fatal(err)
			}
			if err := s.Migrate(ctx); err == nil {
				t.Fatal("unrecognized pre-existing column must fail closed")
			}
			assertCount(t, s, `SELECT COUNT(*) FROM schema_migrations WHERE version='046_node_batches.sql'`, 0)
			assertCount(t, s, `SELECT COUNT(*) FROM sqlite_schema WHERE name='node_batch_previews'`, 0)
		})
	}
}
