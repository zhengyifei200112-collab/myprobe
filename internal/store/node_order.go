package store

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
)

var ErrNodeOrderInvalid = errors.New("invalid complete node order")
var ErrNodeOrderConflict = errors.New("node order changed; reload before saving")

const MaxNodeOrderSize = 1000

type NodeOrderEntry struct {
	ID        string `json:"id"`
	SortOrder int    `json:"sort_order"`
}
type NodeOrderRequest struct {
	Expected []NodeOrderEntry `json:"expected"`
	NodeIDs  []string         `json:"node_ids"`
}

func (r NodeOrderRequest) validate() error {
	if len(r.Expected) == 0 || len(r.Expected) > MaxNodeOrderSize || len(r.Expected) != len(r.NodeIDs) {
		return ErrNodeOrderInvalid
	}
	ids := make(map[string]bool, len(r.Expected))
	for _, entry := range r.Expected {
		if strings.TrimSpace(entry.ID) != entry.ID || entry.ID == "" || len(entry.ID) > 128 || ids[entry.ID] {
			return ErrNodeOrderInvalid
		}
		ids[entry.ID] = true
	}
	for _, id := range r.NodeIDs {
		if !ids[id] {
			return ErrNodeOrderInvalid
		}
		delete(ids, id)
	}
	return nil
}

// ReorderNodes serializes the complete-list compare, order writes and audit.
// Returns changed=false for no-ops and successfully replayed requests.
func (s *Store) ReorderNodes(ctx context.Context, userID string, request NodeOrderRequest) (changed bool, err error) {
	if err = request.validate(); err != nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	// Acquire the writer before reading, without altering any configuration field.
	if _, err = tx.ExecContext(ctx, "UPDATE nodes SET updated_at=updated_at WHERE id=?", request.Expected[0].ID); err != nil {
		return false, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT id,sort_order FROM nodes ORDER BY sort_order,name,id LIMIT ?", MaxNodeOrderSize+1)
	if err != nil {
		return false, err
	}
	current := make([]NodeOrderEntry, 0, len(request.Expected))
	for rows.Next() {
		var entry NodeOrderEntry
		if err = rows.Scan(&entry.ID, &entry.SortOrder); err != nil {
			rows.Close()
			return false, err
		}
		current = append(current, entry)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	if len(current) != len(request.Expected) {
		return false, ErrNodeOrderConflict
	}
	normalized := true
	sameIDs := true
	for index, entry := range current {
		if entry.ID != request.NodeIDs[index] {
			sameIDs = false
		}
		if entry.ID != request.NodeIDs[index] || entry.SortOrder != index {
			normalized = false
		}
	}
	// A replay of a committed request must not create a second audit entry.
	if normalized {
		return false, tx.Commit()
	}
	if !reflect.DeepEqual(current, request.Expected) {
		return false, ErrNodeOrderConflict
	}
	if sameIDs {
		return false, tx.Commit()
	}
	before := make(map[string]int, len(current))
	for _, entry := range current {
		before[entry.ID] = entry.SortOrder
	}
	for index, id := range request.NodeIDs {
		if before[id] == index {
			continue
		}
		if _, err = tx.ExecContext(ctx, "UPDATE nodes SET sort_order=?,updated_at=? WHERE id=?", index, nowText(), id); err != nil {
			return false, err
		}
	}
	raw, err := json.Marshal(struct {
		Before []NodeOrderEntry `json:"before"`
		After  []string         `json:"after"`
	}{Before: current, After: request.NodeIDs})
	if err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO audit_log(user_id,action,object_type,object_id,remote_ip,details_json,created_at) VALUES(?,?,?,?,?,?,?)`, userID, "reorder", "nodes", "", "", string(raw), nowText()); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
