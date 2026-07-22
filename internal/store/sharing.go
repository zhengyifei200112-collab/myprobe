package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

func (s *Store) CreateChartShare(ctx context.Context, name, passwordHash string, nodeIDs []string) (ChartShare, error) {
	name = strings.TrimSpace(name)
	nodeIDs = uniqueStrings(nodeIDs)
	if name == "" || passwordHash == "" || len(nodeIDs) == 0 {
		return ChartShare{}, errors.New("share name, password, and at least one node are required")
	}
	if err := s.validateNodeIDs(ctx, nodeIDs); err != nil {
		return ChartShare{}, err
	}
	raw, _ := json.Marshal(nodeIDs)
	now := time.Now().UTC()
	item := ChartShare{ID: randomID(), Name: name, PasswordHash: passwordHash, NodeIDs: nodeIDs, Enabled: true, CreatedAt: now, UpdatedAt: now}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ChartShare{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO chart_shares(id,name,password_hash,node_filter_json,enabled,created_at,updated_at) VALUES(?,?,?,?,1,?,?)`, item.ID, item.Name, item.PasswordHash, string(raw), formatTime(now), formatTime(now)); err != nil {
		return ChartShare{}, err
	}
	if err := replaceChartShareNodes(ctx, tx, item.ID, nodeIDs); err != nil {
		return ChartShare{}, err
	}
	if err := tx.Commit(); err != nil {
		return ChartShare{}, err
	}
	return item, nil
}

func (s *Store) UpdateChartShare(ctx context.Context, id, name string, passwordHash *string, nodeIDs []string, enabled bool) (ChartShare, error) {
	name = strings.TrimSpace(name)
	nodeIDs = uniqueStrings(nodeIDs)
	if name == "" || len(nodeIDs) == 0 {
		return ChartShare{}, errors.New("share name and at least one node are required")
	}
	if err := s.validateNodeIDs(ctx, nodeIDs); err != nil {
		return ChartShare{}, err
	}
	raw, _ := json.Marshal(nodeIDs)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ChartShare{}, err
	}
	defer tx.Rollback()
	var result sql.Result
	if passwordHash == nil {
		result, err = tx.ExecContext(ctx, `UPDATE chart_shares SET name=?,node_filter_json=?,enabled=?,updated_at=? WHERE id=?`, name, string(raw), enabled, nowText(), id)
	} else {
		result, err = tx.ExecContext(ctx, `UPDATE chart_shares SET name=?,password_hash=?,node_filter_json=?,enabled=?,updated_at=? WHERE id=?`, name, *passwordHash, string(raw), enabled, nowText(), id)
	}
	if err != nil {
		return ChartShare{}, err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return ChartShare{}, ErrNotFound
	}
	if err := replaceChartShareNodes(ctx, tx, id, nodeIDs); err != nil {
		return ChartShare{}, err
	}
	// Configuration changes, especially password rotation, disabling, or scope
	// reduction, must not leave previously issued viewer sessions reusable.
	if _, err := tx.ExecContext(ctx, `DELETE FROM chart_share_sessions WHERE share_id=?`, id); err != nil {
		return ChartShare{}, err
	}
	if err := tx.Commit(); err != nil {
		return ChartShare{}, err
	}
	return s.ChartShare(ctx, id)
}

func (s *Store) ChartShare(ctx context.Context, id string) (ChartShare, error) {
	var item ChartShare
	var raw, created, updated string
	var enabled int
	err := s.db.QueryRowContext(ctx, `SELECT id,name,password_hash,node_filter_json,enabled,created_at,updated_at FROM chart_shares WHERE id=?`, id).Scan(&item.ID, &item.Name, &item.PasswordHash, &raw, &enabled, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return ChartShare{}, ErrNotFound
	}
	if err != nil {
		return ChartShare{}, err
	}
	item.NodeIDs, err = s.chartShareNodeIDs(ctx, item.ID)
	if err != nil {
		return ChartShare{}, err
	}
	item.Enabled = enabled != 0
	item.CreatedAt, _ = parseTime(created)
	item.UpdatedAt, _ = parseTime(updated)
	return item, nil
}

func (s *Store) ListChartShares(ctx context.Context) ([]ChartShare, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,password_hash,node_filter_json,enabled,created_at,updated_at FROM chart_shares ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ChartShare, 0)
	for rows.Next() {
		var item ChartShare
		var raw, created, updated string
		var enabled int
		if err := rows.Scan(&item.ID, &item.Name, &item.PasswordHash, &raw, &enabled, &created, &updated); err != nil {
			return nil, err
		}
		item.Enabled = enabled != 0
		item.CreatedAt, _ = parseTime(created)
		item.UpdatedAt, _ = parseTime(updated)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for index := range items {
		items[index].NodeIDs, err = s.chartShareNodeIDs(ctx, items[index].ID)
		if err != nil {
			return nil, err
		}
	}
	return items, nil
}

func (s *Store) DeleteChartShare(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "chart_shares", id)
}

func (s *Store) CreateChartShareSession(ctx context.Context, shareID string, ttl time.Duration, now time.Time) (ChartShareSession, string, error) {
	token := randomToken(32)
	session := ChartShareSession{ID: randomID(), ShareID: shareID, ExpiresAt: now.Add(ttl)}
	result, err := s.db.ExecContext(ctx, `INSERT INTO chart_share_sessions(id,share_id,token_hash,expires_at,created_at)
		SELECT ?,id,?,?,? FROM chart_shares WHERE id=? AND enabled=1`, session.ID, tokenHash(token), formatTime(session.ExpiresAt), formatTime(now), shareID)
	if err != nil {
		return ChartShareSession{}, "", err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return ChartShareSession{}, "", ErrNotFound
	}
	return session, token, nil
}

func (s *Store) ChartShareSessionByToken(ctx context.Context, shareID, token string, now time.Time) (ChartShareSession, error) {
	var item ChartShareSession
	var expires string
	err := s.db.QueryRowContext(ctx, `SELECT ss.id,ss.share_id,ss.expires_at FROM chart_share_sessions ss
		JOIN chart_shares s ON s.id=ss.share_id AND s.enabled=1
		WHERE ss.share_id=? AND ss.token_hash=? AND ss.expires_at>?`, shareID, tokenHash(token), formatTime(now)).Scan(&item.ID, &item.ShareID, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return ChartShareSession{}, ErrNotFound
	}
	if err != nil {
		return ChartShareSession{}, err
	}
	item.ExpiresAt, _ = parseTime(expires)
	return item, nil
}

func (s *Store) DeleteChartShareSession(ctx context.Context, shareID, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM chart_share_sessions WHERE share_id=? AND token_hash=?`, shareID, tokenHash(token))
	return err
}

func (s *Store) ShareLoginAllowed(ctx context.Context, key string, now time.Time) (bool, time.Duration, error) {
	var blocked sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT blocked_until FROM chart_share_login_attempts WHERE attempt_key=?`, key).Scan(&blocked)
	if errors.Is(err, sql.ErrNoRows) {
		return true, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	if !blocked.Valid {
		return true, 0, nil
	}
	value, err := parseTime(blocked.String)
	if err != nil || !value.After(now) {
		return true, 0, nil
	}
	return false, value.Sub(now), nil
}

func (s *Store) RecordFailedShareLogin(ctx context.Context, key string, now time.Time) (bool, time.Duration, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, 0, err
	}
	defer tx.Rollback()
	var attempts int
	var windowText string
	var blocked sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT attempts,window_started_at,blocked_until FROM chart_share_login_attempts WHERE attempt_key=?`, key).Scan(&attempts, &windowText, &blocked)
	if errors.Is(err, sql.ErrNoRows) {
		attempts = 0
		windowText = formatTime(now)
	} else if err != nil {
		return false, 0, err
	}
	windowStart, _ := parseTime(windowText)
	if now.Sub(windowStart) >= 10*time.Minute {
		attempts = 0
		windowStart = now
	}
	attempts++
	var blockedUntil *time.Time
	if attempts >= 5 {
		value := now.Add(15 * time.Minute)
		blockedUntil = &value
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO chart_share_login_attempts(attempt_key,attempts,window_started_at,blocked_until,updated_at)
		VALUES(?,?,?,?,?) ON CONFLICT(attempt_key) DO UPDATE SET attempts=excluded.attempts,window_started_at=excluded.window_started_at,
		blocked_until=excluded.blocked_until,updated_at=excluded.updated_at`, key, attempts, formatTime(windowStart), nullableTime(blockedUntil), formatTime(now))
	if err != nil {
		return false, 0, err
	}
	if err := tx.Commit(); err != nil {
		return false, 0, err
	}
	if blockedUntil != nil {
		return true, blockedUntil.Sub(now), nil
	}
	return false, 0, nil
}

func (s *Store) ClearShareLoginAttempts(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM chart_share_login_attempts WHERE attempt_key=?`, key)
	return err
}

func (s *Store) NodeIsPublic(ctx context.Context, nodeID string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes WHERE id=? AND hidden=0`, nodeID).Scan(&count)
	return count > 0, err
}

func (s *Store) ListChartShareNodes(ctx context.Context, nodeIDs []string, now time.Time) ([]PublicNode, error) {
	nodes, err := s.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]Node, len(nodes))
	for _, node := range nodes {
		byID[node.ID] = node
	}
	items := make([]PublicNode, 0, len(nodeIDs))
	for _, id := range nodeIDs {
		node, ok := byID[id]
		if !ok {
			continue
		}
		item := PublicNode{Node: node}
		if node.LastSeenAt != nil {
			item.Online = now.Sub(*node.LastSeenAt) <= time.Duration(max(node.ReportSeconds*3, 20))*time.Second
			item.Stale = now.Sub(*node.LastSeenAt) > time.Duration(max(node.ReportSeconds*2, 12))*time.Second
		}
		report, err := s.LatestReport(ctx, node.ID)
		if err != nil {
			return nil, err
		}
		if report != nil {
			copy := protocol.Report(*report)
			item.Report = &copy
		}
		item.Latency, err = s.ListLatestLatency(ctx, node.ID)
		if err != nil {
			return nil, err
		}
		item.Traffic, err = s.TrafficUsage(ctx, node.ID, node.TrafficResetDay, now)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Store) validateNodeIDs(ctx context.Context, nodeIDs []string) error {
	for _, id := range nodeIDs {
		var exists int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes WHERE id=?`, id).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return ErrNotFound
		}
	}
	return nil
}

func replaceChartShareNodes(ctx context.Context, tx *sql.Tx, shareID string, nodeIDs []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM chart_share_nodes WHERE share_id=?`, shareID); err != nil {
		return err
	}
	for _, nodeID := range nodeIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO chart_share_nodes(share_id,node_id) VALUES(?,?)`, shareID, nodeID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) chartShareNodeIDs(ctx context.Context, shareID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT node_id FROM chart_share_nodes WHERE share_id=? ORDER BY node_id`, shareID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]string, 0)
	for rows.Next() {
		var nodeID string
		if err := rows.Scan(&nodeID); err != nil {
			return nil, err
		}
		items = append(items, nodeID)
	}
	return items, rows.Err()
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
