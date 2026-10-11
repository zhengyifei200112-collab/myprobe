package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
)

var ErrMaintenancePreviewChanged = errors.New("maintenance preview changed")
var ErrMaintenanceRequestConflict = errors.New("maintenance request reused with different content")

type MaintenanceCreateResult struct {
	WindowID string `json:"window_id"`
	Replayed bool   `json:"replayed"`
}

// CreateConfirmedMaintenance commits the window, original selector, replay
// identity and audit together. The actor must come from authenticated context.
func (s *Store) CreateConfirmedMaintenance(ctx context.Context, draft MaintenanceDraft, previewDigest, requestID, actor string) (MaintenanceCreateResult, error) {
	decoded, err := hex.DecodeString(previewDigest)
	if !portableID.MatchString(requestID) || !portableID.MatchString(actor) || err != nil || len(decoded) != 32 {
		return MaintenanceCreateResult{}, ErrInvalidMaintenance
	}
	// Bind exact typed request content, including the confirmation and actor.
	// Successful retries do not depend on mutable node references.
	raw, err := json.Marshal(struct {
		Draft          MaintenanceDraft
		Preview, Actor string
	}{draft, previewDigest, actor})
	if err != nil {
		return MaintenanceCreateResult{}, ErrInvalidMaintenance
	}
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MaintenanceCreateResult{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE maintenance_writer SET version=version WHERE id=1`); err != nil {
		return MaintenanceCreateResult{}, err
	}
	var previous, id string
	err = tx.QueryRowContext(ctx, `SELECT request_digest,window_id FROM maintenance_create_requests WHERE request_id=?`, requestID).Scan(&previous, &id)
	if err == nil {
		if previous != digest {
			return MaintenanceCreateResult{}, ErrMaintenanceRequestConflict
		}
		return MaintenanceCreateResult{WindowID: id, Replayed: true}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return MaintenanceCreateResult{}, err
	}
	preview, err := previewMaintenanceWindow(ctx, tx, draft)
	if err != nil {
		return MaintenanceCreateResult{}, err
	}
	if preview.Digest != previewDigest {
		return MaintenanceCreateResult{}, ErrMaintenancePreviewChanged
	}
	ids := make([]string, len(preview.Members))
	for i, member := range preview.Members {
		ids[i] = member.ID
	}
	w, err := normalizeMaintenanceWindow(MaintenanceWindow{Reason: preview.Draft.Reason, Timezone: preview.Draft.Timezone, StartsAt: preview.Draft.StartsAt, EndsAt: preview.Draft.EndsAt, NodeIDs: ids, CreatedBy: actor})
	if err != nil {
		return MaintenanceCreateResult{}, err
	}
	w, err = insertMaintenanceWindow(ctx, tx, w)
	if err != nil {
		return MaintenanceCreateResult{}, err
	}
	selection, err := json.Marshal(preview.Draft.Selection)
	if err != nil {
		return MaintenanceCreateResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO maintenance_selections(window_id,selection_json) VALUES(?,?)`, w.ID, string(selection)); err != nil {
		return MaintenanceCreateResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO maintenance_create_requests(request_id,request_digest,window_id,created_at) VALUES(?,?,?,?)`, requestID, digest, w.ID, formatTime(w.CreatedAt)); err != nil {
		return MaintenanceCreateResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log(user_id,action,object_type,object_id,remote_ip,details_json,created_at) VALUES(?,?,?,?,?,?,?)`, actor, "create", "maintenance_window", w.ID, "", `{"frozen_membership":true}`, formatTime(w.CreatedAt)); err != nil {
		return MaintenanceCreateResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return MaintenanceCreateResult{}, err
	}
	return MaintenanceCreateResult{WindowID: w.ID}, nil
}
