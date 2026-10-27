package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode"

	quality "github.com/zhengyifei200112-collab/myprobe/internal/networkquality"
)

type NetworkQualityBatchQuery struct {
	NodeIDs          []string
	TargetID, Kind   string
	Start, End, AsOf time.Time
}

type NetworkQualityNodeStats struct {
	NodeID, Name string
	Stats        NetworkQualityStats
}

type NetworkQualityBatch struct {
	TargetID, TargetName, Kind string
	Nodes                      []NetworkQualityNodeStats
}

func validQualityID(id string) bool {
	return len(id) > 0 && len(id) <= 128 && strings.TrimSpace(id) == id && strings.IndexFunc(id, unicode.IsControl) < 0
}

func (s *Store) PublicNetworkQualityBatch(ctx context.Context, q NetworkQualityBatchQuery) (NetworkQualityBatch, error) {
	return s.readNetworkQualityBatch(ctx, q, false, "")
}
func (s *Store) AdminNetworkQualityBatch(ctx context.Context, q NetworkQualityBatchQuery) (NetworkQualityBatch, error) {
	return s.readNetworkQualityBatch(ctx, q, true, "")
}

// The route must authenticate its share session first. Membership and enabled
// state are checked again in the same snapshot as names and all statistics.
// Explicit share membership preserves the existing private-share semantics,
// including hidden nodes; it never grants access to an unlisted node.
func (s *Store) SharedNetworkQualityBatch(ctx context.Context, shareID string, q NetworkQualityBatchQuery) (NetworkQualityBatch, error) {
	if !validQualityID(shareID) {
		return NetworkQualityBatch{}, quality.ErrInvalid
	}
	return s.readNetworkQualityBatch(ctx, q, true, shareID)
}

func (s *Store) readNetworkQualityBatch(ctx context.Context, q NetworkQualityBatchQuery, hidden bool, share string) (NetworkQualityBatch, error) {
	var result NetworkQualityBatch
	if len(q.NodeIDs) < 1 || len(q.NodeIDs) > 8 || !validQualityID(q.TargetID) || !validNetworkQualityWindow(q.Kind, q.Start, q.End, q.AsOf) {
		return result, quality.ErrInvalid
	}
	seen := make(map[string]bool, len(q.NodeIDs))
	for _, id := range q.NodeIDs {
		if !validQualityID(id) || seen[id] {
			return result, quality.ErrInvalid
		}
		seen[id] = true
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	// Authorize the complete batch before reading the target or any result. A
	// mixed authorized/unauthorized request never returns a partial projection.
	for _, id := range q.NodeIDs {
		var name string
		err = tx.QueryRowContext(ctx, `SELECT name FROM nodes WHERE id=? AND (? OR hidden=0)
 AND (?='' OR EXISTS(SELECT 1 FROM chart_share_nodes n JOIN chart_shares s ON s.id=n.share_id WHERE n.share_id=? AND n.node_id=nodes.id AND s.enabled=1))`, id, hidden, share, share).Scan(&name)
		if errors.Is(err, sql.ErrNoRows) {
			return NetworkQualityBatch{}, ErrNotFound
		}
		if err != nil {
			return NetworkQualityBatch{}, err
		}
		result.Nodes = append(result.Nodes, NetworkQualityNodeStats{NodeID: id, Name: name})
	}
	assigned := false
	for _, id := range q.NodeIDs {
		var exists bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM network_schedule_epochs WHERE node_id=? AND target_id=? AND kind=?)`, id, q.TargetID, q.Kind).Scan(&exists); err != nil {
			return NetworkQualityBatch{}, err
		}
		assigned = assigned || exists
	}
	if !assigned {
		return NetworkQualityBatch{}, ErrNotFound
	}
	if err = tx.QueryRowContext(ctx, `SELECT name FROM targets WHERE id=?`, q.TargetID).Scan(&result.TargetName); err != nil {
		return NetworkQualityBatch{}, err
	}
	result.TargetID, result.Kind = q.TargetID, q.Kind
	for i := range result.Nodes {
		node := &result.Nodes[i]
		node.Stats, err = readNetworkQualityTx(ctx, tx, NetworkQualityQuery{NodeID: node.NodeID, TargetID: q.TargetID, Kind: q.Kind, Start: q.Start, End: q.End, AsOf: q.AsOf}, hidden)
		if errors.Is(err, ErrNotFound) {
			// The node was authorized in this snapshot; only an absent epoch can
			// produce this status. Do not turn absent plans into perfect coverage.
			node.Stats = NetworkQualityStats{RequestedStart: q.Start.UTC(), RequestedEnd: q.End.UTC(), Availability: "no_plan_history", QuantileMethod: "unavailable"}
			continue
		}
		if err != nil {
			return NetworkQualityBatch{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return NetworkQualityBatch{}, err
	}
	return result, nil
}
