package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var ErrInvalidMaintenance = errors.New("invalid maintenance window")
var ErrMaintenanceConflict = errors.New("maintenance window changed or canceled")
var ErrMaintenanceLimit = errors.New("maintenance window limit reached")

type MaintenanceWindow struct {
	ID         string     `json:"id"`
	Revision   int64      `json:"revision"`
	Reason     string     `json:"reason"`
	Timezone   string     `json:"timezone"`
	StartsAt   time.Time  `json:"starts_at"`
	EndsAt     time.Time  `json:"ends_at"`
	CanceledAt *time.Time `json:"canceled_at,omitempty"`
	NodeIDs    []string   `json:"node_ids"`
	CreatedBy  string     `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
}

func (s *Store) CreateMaintenanceWindow(ctx context.Context, w MaintenanceWindow) (MaintenanceWindow, error) {
	w.Reason = strings.TrimSpace(w.Reason)
	if w.ID != "" || w.Revision != 0 || w.CanceledAt != nil || w.Reason == "" || len(w.Reason) > 1000 || !utf8.ValidString(w.Reason) || strings.IndexFunc(w.Reason, unicode.IsControl) >= 0 || w.CreatedBy == "" || len(w.CreatedBy) > 128 || !utf8.ValidString(w.CreatedBy) || strings.IndexFunc(w.CreatedBy, unicode.IsControl) >= 0 || len(w.NodeIDs) == 0 || len(w.NodeIDs) > 100 || w.StartsAt.IsZero() || w.EndsAt.IsZero() || w.StartsAt.Year() < 1970 || w.EndsAt.Year() > 9999 {
		return MaintenanceWindow{}, ErrInvalidMaintenance
	}
	if w.Timezone == "" || len(w.Timezone) > 128 || w.Timezone == "Local" {
		return MaintenanceWindow{}, ErrInvalidMaintenance
	}
	if _, err := time.LoadLocation(w.Timezone); err != nil {
		return MaintenanceWindow{}, ErrInvalidMaintenance
	}
	w.StartsAt = w.StartsAt.UTC().Truncate(time.Millisecond)
	w.EndsAt = w.EndsAt.UTC().Truncate(time.Millisecond)
	if !w.EndsAt.After(w.StartsAt) {
		return MaintenanceWindow{}, ErrInvalidMaintenance
	}
	seen := make(map[string]bool, len(w.NodeIDs))
	for _, id := range w.NodeIDs {
		if id == "" || seen[id] {
			return MaintenanceWindow{}, ErrInvalidMaintenance
		}
		seen[id] = true
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MaintenanceWindow{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE maintenance_writer SET version=version WHERE id=1`); err != nil {
		return MaintenanceWindow{}, err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM maintenance_windows`).Scan(&count); err != nil {
		return MaintenanceWindow{}, err
	}
	if count >= 1000 {
		return MaintenanceWindow{}, ErrMaintenanceLimit
	}
	for _, id := range w.NodeIDs {
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM nodes WHERE id=?`, id).Scan(&count); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return MaintenanceWindow{}, ErrInvalidMaintenance
			}
			return MaintenanceWindow{}, err
		}
	}
	w.ID, w.Revision, w.CreatedAt = randomID(), 1, time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `INSERT INTO maintenance_windows(id,revision,reason,timezone,starts_at_ms,ends_at_ms,created_by,created_at) VALUES(?,?,?,?,?,?,?,?)`, w.ID, w.Revision, w.Reason, w.Timezone, w.StartsAt.UnixMilli(), w.EndsAt.UnixMilli(), w.CreatedBy, formatTime(w.CreatedAt)); err != nil {
		return MaintenanceWindow{}, err
	}
	for _, id := range w.NodeIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO maintenance_window_nodes(window_id,node_id) VALUES(?,?)`, w.ID, id); err != nil {
			return MaintenanceWindow{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return MaintenanceWindow{}, err
	}
	w.NodeIDs = append([]string(nil), w.NodeIDs...)
	return w, nil
}

// Cancellation ends suppression at now while retaining the earlier interval.
func (s *Store) CancelMaintenanceWindow(ctx context.Context, id string, revision int64, now time.Time) error {
	if id == "" || revision <= 0 || revision == int64(1<<63-1) || now.IsZero() {
		return ErrInvalidMaintenance
	}
	result, err := s.db.ExecContext(ctx, `UPDATE maintenance_windows SET canceled_at_ms=?,revision=revision+1 WHERE id=? AND revision=? AND canceled_at_ms IS NULL`, now.UnixMilli(), id, revision)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrMaintenanceConflict
	}
	return nil
}

// NodeInMaintenance includes past windows so restarts can inspect historical times.
func (s *Store) NodeInMaintenance(ctx context.Context, nodeID string, at time.Time) (bool, error) {
	return nodeInMaintenance(ctx, s.db, nodeID, at)
}

type maintenanceQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func nodeInMaintenance(ctx context.Context, q maintenanceQuery, nodeID string, at time.Time) (bool, error) {
	if nodeID == "" || at.IsZero() {
		return false, ErrInvalidMaintenance
	}
	var active bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM maintenance_windows w JOIN maintenance_window_nodes n ON n.window_id=w.id WHERE n.node_id=? AND w.starts_at_ms<=? AND w.ends_at_ms>? AND (w.canceled_at_ms IS NULL OR w.canceled_at_ms>?))`, nodeID, at.UnixMilli(), at.UnixMilli(), at.UnixMilli()).Scan(&active)
	return active, err
}

// NodeMaintenanceOverlaps tests a nonempty half-open interval, including ended
// and canceled windows. Cancellation clips the effective end without erasing history.
func (s *Store) NodeMaintenanceOverlaps(ctx context.Context, nodeID string, start, end time.Time) (bool, error) {
	if nodeID == "" || start.IsZero() || end.IsZero() || !end.After(start) {
		return false, ErrInvalidMaintenance
	}
	// Window endpoints are exact milliseconds. Round the query's exclusive upper
	// bound upward so an interval ending just after a window start still overlaps.
	upper := end.UnixMilli()
	if time.UnixMilli(upper).Before(end) {
		upper++
	}
	var overlaps bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM maintenance_windows w
		JOIN maintenance_window_nodes n ON n.window_id=w.id
		WHERE n.node_id=? AND w.starts_at_ms<? AND w.ends_at_ms>?
		AND (w.canceled_at_ms IS NULL OR (w.canceled_at_ms>w.starts_at_ms AND w.canceled_at_ms>?))
	)`, nodeID, upper, start.UnixMilli(), start.UnixMilli()).Scan(&overlaps)
	return overlaps, err
}
