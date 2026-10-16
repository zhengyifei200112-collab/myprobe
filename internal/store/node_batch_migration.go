package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// The early development branch recorded 014 before the current chain reached
// 045. Keep that receipt and its data; new installations advance through 046.
// SQLite has no ADD COLUMN IF NOT EXISTS, so this runs in the migration's own
// transaction before the shared schema statements and receipt are committed.
func prepareNodeBatchMigration(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `PRAGMA table_info(nodes)`)
	if err != nil {
		return err
	}
	found, valid := false, false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, kind string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		if name == "config_revision" {
			found = true
			valid = strings.EqualFold(kind, "INTEGER") && notNull == 1 && primaryKey == 0 && defaultValue.Valid && defaultValue.String == "0"
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var legacy int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version='014_node_batches.sql'`).Scan(&legacy); err != nil {
		return err
	}
	if !found {
		if legacy != 0 {
			return errors.New("legacy node batch revision column is missing")
		}
		_, err = tx.ExecContext(ctx, `ALTER TABLE nodes ADD COLUMN config_revision INTEGER NOT NULL DEFAULT 0`)
		return err
	}
	if !valid || legacy != 1 {
		return errors.New("unrecognized existing node batch revision column")
	}
	return nil
}
