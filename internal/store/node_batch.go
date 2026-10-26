package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
)

var ErrBatchInvalid = errors.New("invalid batch")

var ErrBatchExpired = errors.New("preview expired; create a new preview")
var ErrBatchKey = errors.New("idempotency key already used for another operation")

type BatchConflict struct {
	NodeIDs []string `json:"node_ids"`
}

func (e *BatchConflict) Error() string {
	return "configuration changed; preview the entire batch again"
}

type BatchTargets struct {
	Mode string   `json:"mode"`
	IDs  []string `json:"ids"`
}
type NodeBatchRequest struct {
	NodeIDs           []string      `json:"node_ids"`
	AddTags           []string      `json:"add_tags,omitempty"`
	RemoveTags        []string      `json:"remove_tags,omitempty"`
	Hidden            *bool         `json:"hidden,omitempty"`
	CollectionSeconds *int          `json:"collection_seconds,omitempty"`
	ReportSeconds     *int          `json:"report_seconds,omitempty"`
	Targets           *BatchTargets `json:"targets,omitempty"`
}
type BatchNodeState struct {
	Tags              []string `json:"tags"`
	Hidden            bool     `json:"hidden"`
	CollectionSeconds int      `json:"collection_seconds"`
	ReportSeconds     int      `json:"report_seconds"`
	TargetIDs         []string `json:"target_ids"`
}
type BatchNodeChange struct {
	NodeID   string         `json:"node_id"`
	Name     string         `json:"name"`
	Revision int64          `json:"revision"`
	Before   BatchNodeState `json:"before"`
	After    BatchNodeState `json:"after"`
	Changed  bool           `json:"changed"`
}
type NodeBatchPreview struct {
	ID        string            `json:"id"`
	ExpiresAt time.Time         `json:"expires_at"`
	Nodes     []BatchNodeChange `json:"nodes"`
}
type NodeBatchResult struct {
	PreviewID    string    `json:"preview_id"`
	AppliedAt    time.Time `json:"applied_at"`
	ChangedIDs   []string  `json:"changed_ids"`
	UnchangedIDs []string  `json:"unchanged_ids"`
}

func validateBatch(r NodeBatchRequest) error {
	if len(r.NodeIDs) == 0 || len(r.NodeIDs) > 100 {
		return errors.New("select between 1 and 100 nodes")
	}
	seen := map[string]bool{}
	for _, id := range r.NodeIDs {
		if id == "" || len(id) > 128 || seen[id] {
			return errors.New("invalid or duplicate node ID")
		}
		seen[id] = true
	}
	if len(r.AddTags)+len(r.RemoveTags) > 100 {
		return errors.New("too many tags")
	}
	seen = map[string]bool{}
	for _, tag := range append(append([]string{}, r.AddTags...), r.RemoveTags...) {
		if strings.TrimSpace(tag) != tag || tag == "" || len(tag) > 128 || seen[tag] {
			return errors.New("tags must be nonempty, unique and cannot be both added and removed")
		}
		seen[tag] = true
	}
	for _, interval := range []*int{r.CollectionSeconds, r.ReportSeconds} {
		if interval != nil && (*interval < 1 || *interval > 3600) {
			return errors.New("interval must be between 1 and 3600 seconds")
		}
	}
	if r.Targets != nil {
		if r.Targets.Mode != "add" && r.Targets.Mode != "remove" && r.Targets.Mode != "replace" {
			return errors.New("target mode must be add, remove or replace")
		}
		if len(r.Targets.IDs) > 100 || (len(r.Targets.IDs) == 0 && r.Targets.Mode != "replace") {
			return errors.New("select between 1 and 100 targets, or replace with an empty list")
		}
		seen = map[string]bool{}
		for _, id := range r.Targets.IDs {
			if id == "" || len(id) > 128 || seen[id] {
				return errors.New("invalid or duplicate target ID")
			}
			seen[id] = true
		}
	}
	if len(r.AddTags)+len(r.RemoveTags) == 0 && r.Hidden == nil && r.CollectionSeconds == nil && r.ReportSeconds == nil && r.Targets == nil {
		return errors.New("choose at least one operation")
	}
	return nil
}

func batchNode(ctx context.Context, tx *sql.Tx, id string) (BatchNodeChange, error) {
	item := BatchNodeChange{NodeID: id}
	var tags string
	err := tx.QueryRowContext(ctx, `SELECT name,config_revision,tags_json,hidden,collection_seconds,report_seconds FROM nodes WHERE id=?`, id).Scan(&item.Name, &item.Revision, &tags, &item.Before.Hidden, &item.Before.CollectionSeconds, &item.Before.ReportSeconds)
	if errors.Is(err, sql.ErrNoRows) {
		return item, ErrNotFound
	}
	if err != nil {
		return item, err
	}
	if err = json.Unmarshal([]byte(tags), &item.Before.Tags); err != nil {
		return item, err
	}
	if item.Before.Tags == nil {
		item.Before.Tags = []string{}
	}
	item.Before.TargetIDs = []string{}
	rows, err := tx.QueryContext(ctx, `SELECT target_id FROM node_targets WHERE node_id=? ORDER BY target_id`, id)
	if err != nil {
		return item, err
	}
	defer rows.Close()
	for rows.Next() {
		var target string
		if err = rows.Scan(&target); err != nil {
			return item, err
		}
		item.Before.TargetIDs = append(item.Before.TargetIDs, target)
	}
	return item, rows.Err()
}

func changeStrings(before, add, remove []string) []string {
	deleted, included := map[string]bool{}, map[string]bool{}
	for _, value := range remove {
		deleted[value] = true
	}
	result := []string{}
	for _, value := range append(append([]string{}, before...), add...) {
		if !deleted[value] && !included[value] {
			result = append(result, value)
			included[value] = true
		}
	}
	return result
}

func (s *Store) PreviewNodeBatch(ctx context.Context, userID string, request NodeBatchRequest, now time.Time) (NodeBatchPreview, error) {
	preview := NodeBatchPreview{}
	if err := validateBatch(request); err != nil {
		return preview, fmt.Errorf("%w: %s", ErrBatchInvalid, err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return preview, err
	}
	defer tx.Rollback()
	// A write first obtains the SQLite writer reservation before reading snapshots.
	if _, err = tx.ExecContext(ctx, `DELETE FROM node_batch_previews WHERE (applied_at IS NULL AND expires_at < ?) OR (applied_at IS NOT NULL AND applied_at < ?)`, formatTime(now), formatTime(now.Add(-30*24*time.Hour))); err != nil {
		return preview, err
	}
	var pending int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM node_batch_previews WHERE user_id=? AND applied_at IS NULL`, userID).Scan(&pending); err != nil {
		return preview, err
	}
	if pending >= 100 {
		return preview, fmt.Errorf("%w: too many pending previews; wait for existing previews to expire", ErrBatchInvalid)
	}
	if request.Targets != nil {
		for _, id := range request.Targets.IDs {
			var count int
			if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM targets WHERE id=?`, id).Scan(&count); err != nil {
				return preview, err
			}
			if count != 1 {
				return preview, ErrNotFound
			}
		}
	}
	preview = NodeBatchPreview{ID: randomID(), ExpiresAt: now.Add(10 * time.Minute), Nodes: []BatchNodeChange{}}
	for _, id := range request.NodeIDs {
		item, err := batchNode(ctx, tx, id)
		if err != nil {
			return NodeBatchPreview{}, err
		}
		item.After = item.Before
		if len(request.AddTags)+len(request.RemoveTags) > 0 {
			item.After.Tags = changeStrings(item.Before.Tags, request.AddTags, request.RemoveTags)
		}
		if len(item.After.Tags) > 100 {
			return NodeBatchPreview{}, fmt.Errorf("%w: result exceeds 100 tags per node", ErrBatchInvalid)
		}
		if request.Hidden != nil {
			item.After.Hidden = *request.Hidden
		}
		if request.CollectionSeconds != nil {
			item.After.CollectionSeconds = *request.CollectionSeconds
		}
		if request.ReportSeconds != nil {
			item.After.ReportSeconds = *request.ReportSeconds
		}
		if request.Targets != nil {
			switch request.Targets.Mode {
			case "add":
				item.After.TargetIDs = changeStrings(item.Before.TargetIDs, request.Targets.IDs, nil)
			case "remove":
				item.After.TargetIDs = changeStrings(item.Before.TargetIDs, nil, request.Targets.IDs)
			case "replace":
				item.After.TargetIDs = append([]string{}, request.Targets.IDs...)
			}
			sort.Strings(item.After.TargetIDs)
		}
		item.Changed = !reflect.DeepEqual(item.Before, item.After)
		preview.Nodes = append(preview.Nodes, item)
	}
	raw, err := json.Marshal(preview)
	if err != nil {
		return NodeBatchPreview{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO node_batch_previews(id,user_id,preview_json,created_at,expires_at) VALUES(?,?,?,?,?)`, preview.ID, userID, string(raw), formatTime(now), formatTime(preview.ExpiresAt))
	if err != nil {
		return NodeBatchPreview{}, err
	}
	if err = tx.Commit(); err != nil {
		return NodeBatchPreview{}, err
	}
	return preview, nil
}

func (s *Store) ApplyNodeBatch(ctx context.Context, userID, previewID, key string, now time.Time) (NodeBatchResult, error) {
	result := NodeBatchResult{}
	if len(key) < 16 || len(key) > 128 || strings.TrimSpace(key) != key {
		return result, fmt.Errorf("%w: idempotency key must contain 16 to 128 characters", ErrBatchInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	// Acquire the writer reservation before checking versions and idempotency.
	if _, err = tx.ExecContext(ctx, `UPDATE node_batch_previews SET id=id WHERE id=? AND user_id=?`, previewID, userID); err != nil {
		return result, err
	}
	var raw, expiry string
	var priorKey, priorResult sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT preview_json,expires_at,idempotency_key,result_json FROM node_batch_previews WHERE id=? AND user_id=?`, previewID, userID).Scan(&raw, &expiry, &priorKey, &priorResult)
	if errors.Is(err, sql.ErrNoRows) {
		return result, ErrNotFound
	}
	if err != nil {
		return result, err
	}
	if priorResult.Valid {
		if priorKey.String != key {
			return result, ErrBatchKey
		}
		err = json.Unmarshal([]byte(priorResult.String), &result)
		return result, err
	}
	expires, err := parseTime(expiry)
	if err != nil {
		return result, err
	}
	if !now.Before(expires) {
		return result, ErrBatchExpired
	}
	var used int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM node_batch_previews WHERE user_id=? AND idempotency_key=?`, userID, key).Scan(&used); err != nil {
		return result, err
	}
	if used > 0 {
		return result, ErrBatchKey
	}
	var preview NodeBatchPreview
	if err = json.Unmarshal([]byte(raw), &preview); err != nil {
		return result, err
	}
	conflicts := []string{}
	for _, item := range preview.Nodes {
		current, readErr := batchNode(ctx, tx, item.NodeID)
		if errors.Is(readErr, ErrNotFound) || (readErr == nil && current.Revision != item.Revision) {
			conflicts = append(conflicts, item.NodeID)
			continue
		}
		if readErr != nil {
			return result, readErr
		}
		for _, target := range item.After.TargetIDs {
			var exists int
			if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM targets WHERE id=?`, target).Scan(&exists); err != nil {
				return result, err
			}
			if exists == 0 {
				conflicts = append(conflicts, item.NodeID)
				break
			}
		}
	}
	if len(conflicts) > 0 {
		return result, &BatchConflict{NodeIDs: conflicts}
	}
	result = NodeBatchResult{PreviewID: previewID, AppliedAt: now, ChangedIDs: []string{}, UnchangedIDs: []string{}}
	for _, item := range preview.Nodes {
		if !item.Changed {
			result.UnchangedIDs = append(result.UnchangedIDs, item.NodeID)
			continue
		}
		tags, _ := json.Marshal(item.After.Tags)
		update, err := tx.ExecContext(ctx, `UPDATE nodes SET tags_json=?,hidden=?,collection_seconds=?,report_seconds=?,updated_at=? WHERE id=? AND config_revision=?`, string(tags), item.After.Hidden, item.After.CollectionSeconds, item.After.ReportSeconds, formatTime(now), item.NodeID, item.Revision)
		if err != nil {
			return NodeBatchResult{}, err
		}
		count, err := update.RowsAffected()
		if err != nil {
			return NodeBatchResult{}, err
		}
		if count != 1 {
			return NodeBatchResult{}, &BatchConflict{NodeIDs: []string{item.NodeID}}
		}
		if !reflect.DeepEqual(item.Before.TargetIDs, item.After.TargetIDs) {
			if _, err = tx.ExecContext(ctx, `DELETE FROM node_targets WHERE node_id=?`, item.NodeID); err != nil {
				return NodeBatchResult{}, err
			}
			for _, id := range item.After.TargetIDs {
				if _, err = tx.ExecContext(ctx, `INSERT INTO node_targets(node_id,target_id) VALUES(?,?)`, item.NodeID, id); err != nil {
					return NodeBatchResult{}, err
				}
			}
		}
		result.ChangedIDs = append(result.ChangedIDs, item.NodeID)
	}
	resultRaw, _ := json.Marshal(result)
	if _, err = tx.ExecContext(ctx, `UPDATE node_batch_previews SET applied_at=?,idempotency_key=?,result_json=? WHERE id=?`, formatTime(now), key, string(resultRaw), previewID); err != nil {
		return NodeBatchResult{}, err
	}
	// Record the transaction's exact result and field names, without secrets or IPs.
	details := map[string]any{"result": result, "changes": preview.Nodes}
	auditRaw, err := json.Marshal(details)
	if err != nil {
		return NodeBatchResult{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO audit_log(user_id,action,object_type,object_id,remote_ip,details_json,created_at) VALUES(?,?,?,?,?,?,?)`, userID, "batch_update", "nodes", previewID, "", string(auditRaw), formatTime(now)); err != nil {
		return NodeBatchResult{}, fmt.Errorf("batch audit: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return NodeBatchResult{}, err
	}
	return result, nil
}
