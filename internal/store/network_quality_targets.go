package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	quality "github.com/zhengyifei200112-collab/myprobe/internal/networkquality"
)

const NetworkQualityTargetPageSize = 128

type NetworkQualityTargetAlias struct{ ID, Name, Kind string }
type NetworkQualityTargetsPage struct {
	Targets    []NetworkQualityTargetAlias
	NextCursor string
}
type qualityTargetCursor struct {
	Version int    `json:"v"`
	Kind    string `json:"k"`
	ID      string `json:"i"`
	Scope   string `json:"s"`
}

func validateQualityNodes(ids []string) error {
	if len(ids) < 1 || len(ids) > 8 {
		return quality.ErrInvalid
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !validQualityID(id) || seen[id] {
			return quality.ErrInvalid
		}
		seen[id] = true
	}
	return nil
}

func authorizeQualityNodes(ctx context.Context, tx *sql.Tx, ids []string, hidden bool, share string) ([]NetworkQualityNodeStats, error) {
	nodes := make([]NetworkQualityNodeStats, 0, len(ids))
	for _, id := range ids {
		var name string
		err := tx.QueryRowContext(ctx, `SELECT name FROM nodes WHERE id=? AND (? OR hidden=0) AND (?='' OR EXISTS(SELECT 1 FROM chart_share_nodes n JOIN chart_shares s ON s.id=n.share_id WHERE n.share_id=? AND n.node_id=nodes.id AND s.enabled=1))`, id, hidden, share, share).Scan(&name)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, NetworkQualityNodeStats{NodeID: id, Name: name})
	}
	return nodes, nil
}

func qualityTargetScope(ids []string, hidden bool, share string) string {
	copyIDs := append([]string(nil), ids...)
	sort.Strings(copyIDs)
	mode := "public"
	if hidden {
		mode = "admin"
	}
	if share != "" {
		mode = "share:" + share
	}
	sum := sha256.Sum256([]byte(mode + "\x00" + strings.Join(copyIDs, "\x00")))
	return hex.EncodeToString(sum[:])
}

func (s *Store) PublicNetworkQualityTargets(ctx context.Context, ids []string, cursor string) (NetworkQualityTargetsPage, error) {
	return s.networkQualityTargets(ctx, ids, cursor, false, "")
}
func (s *Store) AdminNetworkQualityTargets(ctx context.Context, ids []string, cursor string) (NetworkQualityTargetsPage, error) {
	return s.networkQualityTargets(ctx, ids, cursor, true, "")
}
func (s *Store) SharedNetworkQualityTargets(ctx context.Context, share string, ids []string, cursor string) (NetworkQualityTargetsPage, error) {
	if !validQualityID(share) {
		return NetworkQualityTargetsPage{}, quality.ErrInvalid
	}
	return s.networkQualityTargets(ctx, ids, cursor, true, share)
}

func (s *Store) networkQualityTargets(ctx context.Context, ids []string, cursor string, hidden bool, share string) (NetworkQualityTargetsPage, error) {
	result := NetworkQualityTargetsPage{Targets: []NetworkQualityTargetAlias{}}
	if err := validateQualityNodes(ids); err != nil {
		return result, err
	}
	scope := qualityTargetScope(ids, hidden, share)
	after := qualityTargetCursor{Scope: scope}
	if cursor != "" {
		if len(cursor) > 512 {
			return result, quality.ErrInvalid
		}
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || len(cursor) > 512 || json.Unmarshal(raw, &after) != nil || after.Version != 1 || after.Scope != scope || !validQualityID(after.ID) || (after.Kind != "ping" && after.Kind != "tcping") {
			return result, quality.ErrInvalid
		}
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if _, err = authorizeQualityNodes(ctx, tx, ids, hidden, share); err != nil {
		return result, err
	}
	var floor int64
	if err = tx.QueryRowContext(ctx, `SELECT retained_from FROM network_quality_history_state WHERE singleton=1`).Scan(&floor); err != nil {
		return result, err
	}
	if floor < 0 || floor > 9223372035 {
		return result, ErrNetworkQualityUnavailable
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids)+5)
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, floor, after.Kind, after.Kind, after.ID, NetworkQualityTargetPageSize+1)
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT t.id,t.name,e.kind FROM network_schedule_epochs e JOIN targets t ON t.id=e.target_id WHERE e.node_id IN (`+placeholders+`) AND (e.ends_at IS NULL OR e.ends_at>?) AND (e.kind>? OR (e.kind=? AND t.id>?)) ORDER BY e.kind,t.id LIMIT ?`, args...)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var t NetworkQualityTargetAlias
		if err = rows.Scan(&t.ID, &t.Name, &t.Kind); err != nil {
			break
		}
		result.Targets = append(result.Targets, t)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return NetworkQualityTargetsPage{}, err
	}
	if len(result.Targets) > NetworkQualityTargetPageSize {
		result.Targets = result.Targets[:NetworkQualityTargetPageSize]
		last := result.Targets[len(result.Targets)-1]
		raw, _ := json.Marshal(qualityTargetCursor{Version: 1, Kind: last.Kind, ID: last.ID, Scope: scope})
		result.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	if err = tx.Commit(); err != nil {
		return NetworkQualityTargetsPage{}, err
	}
	return result, nil
}
