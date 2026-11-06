package rollbackprep

import (
	"context"
	"database/sql"
)

// This offline command targets one fixed historical release. Keep its admission
// list independent of the current application's growing migration set.
var legacyMigrations = [...]string{
	"001_initial.sql",
	"002_traffic_state.sql",
	"003_alert_states.sql",
	"004_alert_delivery_error.sql",
	"005_chart_share_access.sql",
	"006_chart_share_nodes.sql",
	"007_custom_display.sql",
	"008_auth_security.sql",
	"009_agent_metadata.sql",
	"010_history_rollups.sql",
	"011_direct_target_assignments.sql",
	"012_notification_system.sql",
	LegacyVersion,
}

func validateLegacyMigrations(ctx context.Context, db *sql.DB) error {
	// One extra row detects oversized or duplicate histories without materializing
	// an arbitrary supplied migration ledger. Receipt values never enter errors.
	rows, err := db.QueryContext(ctx, `SELECT CASE
WHEN typeof(version)='text' AND length(CAST(version AS BLOB)) BETWEEN 1 AND 128
THEN version ELSE NULL END FROM schema_migrations LIMIT ?`, len(legacyMigrations)+1)
	if err != nil {
		return ErrUnsupportedSchema
	}
	defer rows.Close()
	seen := make(map[string]bool, len(legacyMigrations))
	for rows.Next() {
		var value any
		if err := rows.Scan(&value); err != nil {
			return ErrUnsupportedSchema
		}
		name, ok := value.(string)
		if !ok || len(name) > 128 || seen[name] {
			return ErrUnsupportedSchema
		}
		known := false
		for _, allowed := range legacyMigrations {
			if name == allowed {
				known = true
				break
			}
		}
		if !known {
			return ErrUnsupportedSchema
		}
		seen[name] = true
	}
	if rows.Err() != nil || len(seen) != len(legacyMigrations) {
		return ErrUnsupportedSchema
	}
	return nil
}
