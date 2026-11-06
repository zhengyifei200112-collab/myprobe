package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func incompatibleMigrationFixture(t *testing.T, root string) string {
	t.Helper()
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(root, "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, _, err = s.CreateNode(ctx, CreateNodeParams{ID: "source", Name: "Synthetic source"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)`, "999_unsupported_future.sql", nowText()); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(root, "unsupported.db")
	if err = s.ConsistentBackup(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestUnsupportedMigrationHistoryInCommittedWALIsRejectedReadOnly(t *testing.T) {
	ctx := context.Background()
	root := httpFixtureDir(t)
	file := filepath.Join(root, "wal-history.db")
	s, err := Open(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.db.ExecContext(ctx, `INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)`, "999_wal_only.sql", nowText()); err != nil {
		t.Fatal(err)
	}
	mainBefore := migrationFixtureDigest(t, file)
	walBefore := migrationFixtureDigest(t, file+"-wal")
	opened, err := Open(ctx, file)
	if opened != nil {
		opened.Close()
	}
	if !errors.Is(err, ErrIncompatibleDatabaseSchema) {
		t.Fatal("committed WAL migration history was not checked", err)
	}
	if migrationFixtureDigest(t, file) != mainBefore || migrationFixtureDigest(t, file+"-wal") != walBefore {
		t.Fatal("rejected read-only check changed committed database bytes")
	}
}

func TestUnsupportedMigrationNamesAreNotEchoed(t *testing.T) {
	ctx := context.Background()
	root := httpFixtureDir(t)
	s, err := Open(ctx, filepath.Join(root, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	private := "untrusted-ledger-content-" + strings.Repeat("x", 1024)
	if _, err = s.db.ExecContext(ctx, `INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)`, private, nowText()); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); !errors.Is(err, ErrIncompatibleDatabaseSchema) || strings.Contains(err.Error(), "untrusted-ledger") {
		t.Fatal("unsupported input was accepted or echoed")
	}
}

func migrationFixtureDigest(t *testing.T, file string) [32]byte {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(b)
}

func TestValidateDatabaseFileRejectsUnsupportedMigrationHistory(t *testing.T) {
	root := httpFixtureDir(t)
	snapshot := incompatibleMigrationFixture(t, root)
	before := migrationFixtureDigest(t, snapshot)
	if err := ValidateDatabaseFile(context.Background(), snapshot); !errors.Is(err, ErrIncompatibleDatabaseSchema) {
		t.Fatal("unsupported migration history was accepted as a restorable database")
	}
	if after := migrationFixtureDigest(t, snapshot); after != before {
		t.Fatal("read-only validation changed the supplied database")
	}
}

func TestStageRestoreRejectsUnsupportedMigrationHistoryWithoutPendingFile(t *testing.T) {
	ctx := context.Background()
	root := httpFixtureDir(t)
	snapshot := incompatibleMigrationFixture(t, root)
	before := migrationFixtureDigest(t, snapshot)
	destination := filepath.Join(root, "destination.db")
	s, err := Open(ctx, destination)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, _, err = s.CreateNode(ctx, CreateNodeParams{ID: "destination", Name: "Destination marker"}); err != nil {
		t.Fatal(err)
	}
	if _, err = StageDatabaseRestore(ctx, destination, snapshot); !errors.Is(err, ErrIncompatibleDatabaseSchema) {
		t.Fatal("unsupported migration history was staged for replacement")
	}
	if _, err = os.Stat(destination + ".restore"); !os.IsNotExist(err) {
		t.Fatal("rejected input created a pending restore")
	}
	if migrationFixtureDigest(t, snapshot) != before {
		t.Fatal("rejected source was changed or removed")
	}
	var count int
	if err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes WHERE id='destination'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("destination marker was changed", err)
	}
}

func TestApplyPendingRestoreRejectsUnsupportedHistoryBeforeMovingCurrent(t *testing.T) {
	ctx := context.Background()
	root := httpFixtureDir(t)
	snapshot := incompatibleMigrationFixture(t, root)
	destination := filepath.Join(root, "destination.db")
	s, err := Open(ctx, destination)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.CreateNode(ctx, CreateNodeParams{ID: "destination", Name: "Destination marker"}); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	before := migrationFixtureDigest(t, destination)
	pending := destination + ".restore"
	if err = os.Rename(snapshot, pending); err != nil {
		t.Fatal(err)
	}
	inputBefore := migrationFixtureDigest(t, pending)
	if _, err = ApplyPendingRestore(ctx, destination, time.Now().UTC()); !errors.Is(err, ErrIncompatibleDatabaseSchema) {
		t.Fatal("unsupported pending database replaced the current database")
	}
	if migrationFixtureDigest(t, destination) != before || migrationFixtureDigest(t, pending) != inputBefore {
		t.Fatal("rejected activation changed the current or pending byte set")
	}
	previous, err := filepath.Glob(destination + ".pre-restore-*")
	if err != nil || len(previous) != 0 {
		t.Fatal("rejected activation moved the current database", err)
	}
}

func TestOpenUnsupportedHistoryRejectsBeforeChangingJournalMode(t *testing.T) {
	root := httpFixtureDir(t)
	snapshot := incompatibleMigrationFixture(t, root)
	before := migrationFixtureDigest(t, snapshot)
	s, err := Open(context.Background(), snapshot)
	if s != nil {
		s.Close()
	}
	if !errors.Is(err, ErrIncompatibleDatabaseSchema) {
		t.Fatal("a server opened a database with an unsupported migration history")
	}
	if migrationFixtureDigest(t, snapshot) != before {
		t.Fatal("rejected open changed the database or journal mode")
	}
}

func TestKnownMigrationPrefixStillUpgrades(t *testing.T) {
	ctx := context.Background()
	root := httpFixtureDir(t)
	file := filepath.Join(root, "released-prefix.db")
	db, err := sql.Open("sqlite", sqliteDSN(file))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() > "013_github_oauth.sql" {
			continue
		}
		body, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.ExecContext(ctx, string(body)); err != nil {
			t.Fatal(err)
		}
		if _, err = db.ExecContext(ctx, `INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)`, entry.Name(), nowText()); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if err = ValidateDatabaseFile(ctx, file); err != nil {
		t.Fatal("compatible older backup was rejected", err)
	}
	s, err := Open(ctx, file)
	if err != nil {
		t.Fatal("compatible older database could not upgrade", err)
	}
	defer s.Close()
	if _, _, err = s.CreateNode(ctx, CreateNodeParams{ID: "after-upgrade", Name: "After upgrade"}); err != nil {
		t.Fatal(err)
	}
}
