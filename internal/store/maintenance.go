package store

import (
	"context"
	"database/sql"
	"encoding/json"
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
	ID         string                `json:"id"`
	Revision   int64                 `json:"revision"`
	Reason     string                `json:"reason"`
	Timezone   string                `json:"timezone"`
	StartsAt   time.Time             `json:"starts_at"`
	EndsAt     time.Time             `json:"ends_at"`
	CanceledAt *time.Time            `json:"canceled_at,omitempty"`
	NodeIDs    []string              `json:"node_ids"`
	CreatedBy  string                `json:"created_by"`
	CreatedAt  time.Time             `json:"created_at"`
	Selection  *MaintenanceSelection `json:"selection,omitempty"`
	CanceledBy string                `json:"canceled_by,omitempty"`
}

// ListMaintenanceWindows reads window metadata and frozen membership from one
// snapshot. IDs provide a stable cursor, not chronological ordering.
func (s *Store) ListMaintenanceWindows(ctx context.Context, after string, limit int) ([]MaintenanceWindow, string, error) {
	if limit < 1 || limit > 100 || len(after) > 128 {
		return nil, "", ErrInvalidMaintenance
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT w.id,w.revision,w.reason,w.timezone,w.starts_at_ms,w.ends_at_ms,w.canceled_at_ms,w.created_by,w.created_at,s.selection_json,c.actor FROM maintenance_windows w LEFT JOIN maintenance_selections s ON s.window_id=w.id LEFT JOIN maintenance_cancel_actions c ON c.window_id=w.id WHERE w.id>? ORDER BY w.id LIMIT ?`, after, limit+1)
	if err != nil {
		return nil, "", err
	}
	items := make([]MaintenanceWindow, 0, limit+1)
	for rows.Next() {
		var w MaintenanceWindow
		var start, end int64
		var canceled sql.NullInt64
		var created string
		var selection, cancelActor sql.NullString
		if err := rows.Scan(&w.ID, &w.Revision, &w.Reason, &w.Timezone, &start, &end, &canceled, &w.CreatedBy, &created, &selection, &cancelActor); err != nil {
			rows.Close()
			return nil, "", err
		}
		w.StartsAt, w.EndsAt = time.UnixMilli(start).UTC(), time.UnixMilli(end).UTC()
		if canceled.Valid {
			at := time.UnixMilli(canceled.Int64).UTC()
			w.CanceledAt = &at
		}
		w.CanceledBy = cancelActor.String
		if selection.Valid {
			w.Selection = new(MaintenanceSelection)
			if err := json.Unmarshal([]byte(selection.String), w.Selection); err != nil {
				rows.Close()
				return nil, "", err
			}
		}
		w.CreatedAt, err = parseTime(created)
		if err != nil {
			rows.Close()
			return nil, "", err
		}
		w.NodeIDs = []string{}
		items = append(items, w)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		next = items[len(items)-1].ID
	}
	for i := range items {
		members, err := tx.QueryContext(ctx, `SELECT node_id FROM maintenance_window_nodes WHERE window_id=? ORDER BY node_id`, items[i].ID)
		if err != nil {
			return nil, "", err
		}
		for members.Next() {
			var id string
			if err := members.Scan(&id); err != nil {
				members.Close()
				return nil, "", err
			}
			items[i].NodeIDs = append(items[i].NodeIDs, id)
		}
		err = members.Err()
		members.Close()
		if err != nil {
			return nil, "", err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, "", err
	}
	return items, next, nil
}

func normalizeMaintenanceWindow(w MaintenanceWindow) (MaintenanceWindow, error) {
	w.Reason = strings.TrimSpace(w.Reason)
	if w.ID != "" || w.Revision != 0 || w.CanceledAt != nil || w.CanceledBy != "" || w.Selection != nil || w.Reason == "" || len(w.Reason) > 1000 || !utf8.ValidString(w.Reason) || strings.IndexFunc(w.Reason, unicode.IsControl) >= 0 || w.CreatedBy == "" || len(w.CreatedBy) > 128 || !utf8.ValidString(w.CreatedBy) || strings.IndexFunc(w.CreatedBy, unicode.IsControl) >= 0 || len(w.NodeIDs) == 0 || len(w.NodeIDs) > 100 || w.StartsAt.IsZero() || w.EndsAt.IsZero() || w.StartsAt.Year() < 1970 || w.EndsAt.Year() > 9999 {
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
	if w.StartsAt.Year() < 1970 || w.StartsAt.Year() > 9999 || w.EndsAt.Year() < 1970 || w.EndsAt.Year() > 9999 || !w.EndsAt.After(w.StartsAt) {
		return MaintenanceWindow{}, ErrInvalidMaintenance
	}
	seen := make(map[string]bool, len(w.NodeIDs))
	for _, id := range w.NodeIDs {
		if id == "" || seen[id] {
			return MaintenanceWindow{}, ErrInvalidMaintenance
		}
		seen[id] = true
	}
	return w, nil
}

func (s *Store) CreateMaintenanceWindow(ctx context.Context, w MaintenanceWindow) (MaintenanceWindow, error) {
	w, err := normalizeMaintenanceWindow(w)
	if err != nil {
		return MaintenanceWindow{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MaintenanceWindow{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE maintenance_writer SET version=version WHERE id=1`); err != nil {
		return MaintenanceWindow{}, err
	}
	w, err = insertMaintenanceWindow(ctx, tx, w)
	if err != nil {
		return MaintenanceWindow{}, err
	}
	if err := tx.Commit(); err != nil {
		return MaintenanceWindow{}, err
	}
	return w, nil
}

func insertMaintenanceWindow(ctx context.Context, tx *sql.Tx, w MaintenanceWindow) (MaintenanceWindow, error) {
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

// Called with the delivery writer transaction held; cancellation and resetting
// the repeat clock must commit together so a fresh post-window observation can notify.
func suppressMaintenanceDelivery(ctx context.Context, tx *sql.Tx, d NotificationDelivery, now time.Time) (bool, error) {
	var nodeID string
	if err := tx.QueryRowContext(ctx, `SELECT node_id FROM alert_incidents WHERE id=?`, d.IncidentID).Scan(&nodeID); err != nil {
		return false, err
	}
	active, err := nodeInMaintenance(ctx, tx, nodeID, now)
	if err == nil && !active && now.After(d.CreatedAt) {
		// The server may have been stopped for the entire window. An old queued
		// transition must not be delivered merely because maintenance ended.
		active, err = nodeMaintenanceOverlaps(ctx, tx, nodeID, d.CreatedAt, now)
	}
	if err != nil || !active {
		return false, err
	}
	if err := cancelDelivery(ctx, tx, d.ID, "maintenance", now); err != nil {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE alert_incidents SET last_enqueued_at=NULL WHERE id=?`, d.IncidentID)
	return true, err
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
	return nodeMaintenanceOverlaps(ctx, s.db, nodeID, start, end)
}

func nodeMaintenanceOverlaps(ctx context.Context, q maintenanceQuery, nodeID string, start, end time.Time) (bool, error) {
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
	err := q.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM maintenance_windows w
		JOIN maintenance_window_nodes n ON n.window_id=w.id
		WHERE n.node_id=? AND w.starts_at_ms<? AND w.ends_at_ms>?
		AND (w.canceled_at_ms IS NULL OR (w.canceled_at_ms>w.starts_at_ms AND w.canceled_at_ms>?))
	)`, nodeID, upper, start.UnixMilli(), start.UnixMilli()).Scan(&overlaps)
	return overlaps, err
}
