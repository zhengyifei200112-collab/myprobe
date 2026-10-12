package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type MaintenanceCancelResult struct {
	WindowID   string    `json:"window_id"`
	Revision   int64     `json:"revision"`
	CanceledAt time.Time `json:"canceled_at"`
	Replayed   bool      `json:"replayed"`
}

// CancelConfirmedMaintenance preserves history and commits cancellation, actor
// and audit atomically. A same-actor retry of the confirmed revision replays.
func (s *Store) CancelConfirmedMaintenance(ctx context.Context, id string, revision int64, actor string, now time.Time) (MaintenanceCancelResult, error) {
	if !portableID.MatchString(id) || !portableID.MatchString(actor) || revision <= 0 || revision == int64(1<<63-1) || now.IsZero() {
		return MaintenanceCancelResult{}, ErrInvalidMaintenance
	}
	now = now.UTC().Truncate(time.Millisecond)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MaintenanceCancelResult{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE maintenance_writer SET version=version WHERE id=1`); err != nil {
		return MaintenanceCancelResult{}, err
	}
	var current, end int64
	var canceled sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT revision,ends_at_ms,canceled_at_ms FROM maintenance_windows WHERE id=?`, id).Scan(&current, &end, &canceled)
	if errors.Is(err, sql.ErrNoRows) {
		return MaintenanceCancelResult{}, ErrNotFound
	}
	if err != nil {
		return MaintenanceCancelResult{}, err
	}
	if canceled.Valid {
		var previousActor string
		var previousRevision int64
		err := tx.QueryRowContext(ctx, `SELECT actor,confirmed_revision FROM maintenance_cancel_actions WHERE window_id=?`, id).Scan(&previousActor, &previousRevision)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return MaintenanceCancelResult{}, err
		}
		if err == nil && previousActor == actor && previousRevision == revision && current == revision+1 {
			return MaintenanceCancelResult{WindowID: id, Revision: current, CanceledAt: time.UnixMilli(canceled.Int64).UTC(), Replayed: true}, nil
		}
		return MaintenanceCancelResult{}, ErrMaintenanceConflict
	}
	if current != revision {
		return MaintenanceCancelResult{}, ErrMaintenanceConflict
	}
	if end <= now.UnixMilli() {
		return MaintenanceCancelResult{}, ErrMaintenanceExpired
	}
	if _, err := tx.ExecContext(ctx, `UPDATE maintenance_windows SET canceled_at_ms=?,revision=revision+1 WHERE id=?`, now.UnixMilli(), id); err != nil {
		return MaintenanceCancelResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO maintenance_cancel_actions(window_id,actor,confirmed_revision) VALUES(?,?,?)`, id, actor, revision); err != nil {
		return MaintenanceCancelResult{}, err
	}
	details, err := json.Marshal(struct {
		Revision int64 `json:"revision"`
	}{revision + 1})
	if err != nil {
		return MaintenanceCancelResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log(user_id,action,object_type,object_id,remote_ip,details_json,created_at) VALUES(?,?,?,?,?,?,?)`, actor, "cancel", "maintenance_window", id, "", string(details), formatTime(now)); err != nil {
		return MaintenanceCancelResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return MaintenanceCancelResult{}, err
	}
	return MaintenanceCancelResult{WindowID: id, Revision: revision + 1, CanceledAt: now}, nil
}
