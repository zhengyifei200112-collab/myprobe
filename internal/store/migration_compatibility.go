package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

var ErrIncompatibleDatabaseSchema = errors.New("database migration history is not supported by this server")

// Check the recorded lineage rather than only its largest numeric prefix:
// independently developed or newer databases can use different migration names.
// Unknown names are not echoed, because the database is an untrusted input.
func validateMigrationHistory(ctx context.Context, db *sql.DB) error {
	var kind string
	err := db.QueryRowContext(ctx, `SELECT type FROM sqlite_master WHERE name='schema_migrations' COLLATE NOCASE`).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // A new empty database has not run the initial migration yet.
	}
	if err != nil {
		return fmt.Errorf("inspect database migration history: %w", err)
	}
	if kind != "table" {
		return ErrIncompatibleDatabaseSchema
	}
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("read supported migrations: %w", err)
	}
	known := make(map[string]bool, len(entries))
	maxVersionBytes := 0
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			known[entry.Name()] = true
			maxVersionBytes = max(maxVersionBytes, len(entry.Name()))
		}
	}
	// Bound both row count and returned text for an untrusted uploaded ledger.
	rows, err := db.QueryContext(ctx, `SELECT substr(version,1,?),length(CAST(version AS BLOB)),typeof(version) FROM schema_migrations LIMIT ?`, maxVersionBytes+1, len(known)+1)
	if err != nil {
		return fmt.Errorf("read database migration history: %w", err)
	}
	defer rows.Close()
	seen := make(map[string]bool, len(known))
	for rows.Next() {
		var version, valueType string
		var length int
		if err := rows.Scan(&version, &length, &valueType); err != nil {
			return ErrIncompatibleDatabaseSchema
		}
		if valueType != "text" || length > maxVersionBytes || len(version) != length || !known[version] || seen[version] {
			return ErrIncompatibleDatabaseSchema
		}
		seen[version] = true
	}
	return rows.Err()
}

// Do not apply the writer's WAL pragmas to an incompatible existing database.
// Normal opening must also work when committed migration records are in a WAL,
// so this deliberately uses SQLite's normal read-only mode, not immutable mode.
func validateExistingMigrationHistory(ctx context.Context, path string) error {
	if path == ":memory:" {
		return nil
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect database path: %w", err)
	}
	if info.Size() == 0 {
		return nil
	}
	db, err := sql.Open("sqlite", "file:"+url.PathEscape(filepath.ToSlash(path))+"?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return fmt.Errorf("open database migration history: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	return validateMigrationHistory(ctx, db)
}
