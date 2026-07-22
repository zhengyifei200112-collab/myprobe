package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"github.com/zhengyifei200112-collab/myprobe/internal/sanitize"
)

func (s *Store) ListNodes(ctx context.Context) ([]Node, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, sort_order, hidden, tags_json, country_code, currency,
		price_minor, billing_cycle, expires_at, traffic_reset_day, use_since_boot, latency_mode, custom_html, custom_badges_json, custom_links_json,
		collection_seconds, report_seconds, created_at, updated_at, last_seen_at FROM nodes ORDER BY sort_order, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Node, 0)
	for rows.Next() {
		var n Node
		var hidden, boot int
		var tags, badges, links, created, updated string
		var expires, seen sql.NullString
		var price, reset sql.NullInt64
		if err := rows.Scan(&n.ID, &n.Name, &n.SortOrder, &hidden, &tags, &n.CountryCode, &n.Currency, &price, &n.BillingCycle, &expires, &reset, &boot, &n.LatencyMode, &n.CustomHTML, &badges, &links, &n.CollectionSeconds, &n.ReportSeconds, &created, &updated, &seen); err != nil {
			return nil, err
		}
		decodeNodeFields(&n, hidden, boot, tags, badges, links, created, updated, expires, seen, price, reset)
		items = append(items, n)
	}
	return items, rows.Err()
}

func (s *Store) UpdateNode(ctx context.Context, id string, p UpdateNodeParams) (Node, error) {
	p.Name = strings.TrimSpace(p.Name)
	p.CountryCode = strings.ToUpper(strings.TrimSpace(p.CountryCode))
	p.Currency = strings.ToUpper(strings.TrimSpace(p.Currency))
	if p.Name == "" || p.CollectionSeconds < 1 || p.CollectionSeconds > 3600 || p.ReportSeconds < 1 || p.ReportSeconds > 3600 {
		return Node{}, errors.New("invalid node configuration")
	}
	if p.LatencyMode != protocol.TaskKindPing && p.LatencyMode != protocol.TaskKindTCPing {
		return Node{}, errors.New("invalid latency mode")
	}
	if p.TrafficResetDay != nil && (*p.TrafficResetDay < 1 || *p.TrafficResetDay > 31) {
		return Node{}, errors.New("invalid traffic reset day")
	}
	customHTML, badges, links, err := normalizeCustomDisplay(p.CustomHTML, p.CustomBadges, p.CustomLinks)
	if err != nil {
		return Node{}, err
	}
	tags, _ := json.Marshal(p.Tags)
	badgesJSON, _ := json.Marshal(badges)
	linksJSON, _ := json.Marshal(links)
	now := nowText()
	result, err := s.db.ExecContext(ctx, `UPDATE nodes SET name=?, sort_order=?, hidden=?, tags_json=?, country_code=?, currency=?, price_minor=?, billing_cycle=?, expires_at=?, traffic_reset_day=?, use_since_boot=?, latency_mode=?, custom_html=?, custom_badges_json=?, custom_links_json=?, collection_seconds=?, report_seconds=?, updated_at=? WHERE id=?`,
		p.Name, p.SortOrder, p.Hidden, string(tags), p.CountryCode, p.Currency, p.PriceMinor, p.BillingCycle, nullableTime(p.ExpiresAt), p.TrafficResetDay, p.UseSinceBoot, p.LatencyMode, customHTML, string(badgesJSON), string(linksJSON), p.CollectionSeconds, p.ReportSeconds, now, id)
	if err != nil {
		return Node{}, err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return Node{}, ErrNotFound
	}
	items, err := s.ListNodes(ctx)
	if err != nil {
		return Node{}, err
	}
	for _, item := range items {
		if item.ID == id {
			return item, nil
		}
	}
	return Node{}, ErrNotFound
}

func normalizeCustomDisplay(customHTML string, badges []CustomBadge, links []CustomLink) (string, []CustomBadge, []CustomLink, error) {
	if len(badges) > 12 || len(links) > 8 {
		return "", nil, nil, errors.New("too many custom badges or links")
	}
	allowedColors := map[string]bool{"gray": true, "blue": true, "green": true, "orange": true, "red": true}
	cleanBadges := make([]CustomBadge, 0, len(badges))
	for _, item := range badges {
		item.Label = strings.TrimSpace(item.Label)
		item.Color = strings.ToLower(strings.TrimSpace(item.Color))
		if item.Label == "" || len(item.Label) > 40 || !allowedColors[item.Color] {
			return "", nil, nil, errors.New("invalid custom badge")
		}
		cleanBadges = append(cleanBadges, item)
	}
	cleanLinks := make([]CustomLink, 0, len(links))
	for _, item := range links {
		item.Label = strings.TrimSpace(item.Label)
		item.URL = strings.TrimSpace(item.URL)
		parsed, err := url.ParseRequestURI(item.URL)
		if err != nil || item.Label == "" || len(item.Label) > 60 || len(item.URL) > 2048 || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return "", nil, nil, errors.New("invalid custom link")
		}
		cleanLinks = append(cleanLinks, item)
	}
	cleanHTML, err := sanitize.HTML(customHTML)
	if err != nil {
		return "", nil, nil, err
	}
	return cleanHTML, cleanBadges, cleanLinks, nil
}

func (s *Store) DeleteNode(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "nodes", id)
}

func (s *Store) RotateAgentToken(ctx context.Context, nodeID string) (string, error) {
	token := randomToken(32)
	now := nowText()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	if result, err := tx.ExecContext(ctx, "UPDATE agent_tokens SET revoked_at=? WHERE node_id=? AND revoked_at IS NULL", now, nodeID); err != nil {
		return "", err
	} else if count, _ := result.RowsAffected(); count == 0 {
		return "", ErrNotFound
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO agent_tokens(id,node_id,token_hash,label,created_at) VALUES(?,?,?,'rotated',?)`, randomID(), nodeID, tokenHash(token), now); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return token, nil
}

func (s *Store) UpdateTarget(ctx context.Context, id string, p UpdateTargetParams) (Target, error) {
	port := 0
	if p.Port != nil {
		port = *p.Port
	}
	task := protocol.Task{ID: "validation", TargetID: id, Kind: p.Kind, Host: strings.TrimSpace(p.Host), Port: port, TimeoutMS: p.TimeoutMS, ExpiresAt: time.Now().UTC().Add(time.Minute)}
	if strings.TrimSpace(p.Name) == "" || p.IntervalSeconds < 5 || p.IntervalSeconds > 86400 || task.Validate(time.Now().UTC()) != nil {
		return Target{}, errors.New("invalid target configuration")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE targets SET name=?,kind=?,host=?,port=?,interval_seconds=?,timeout_ms=?,enabled=?,sort_order=?,updated_at=? WHERE id=?`, strings.TrimSpace(p.Name), p.Kind, strings.TrimSpace(p.Host), p.Port, p.IntervalSeconds, p.TimeoutMS, p.Enabled, p.SortOrder, nowText(), id)
	if err != nil {
		return Target{}, err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return Target{}, ErrNotFound
	}
	items, err := s.ListTargets(ctx)
	if err != nil {
		return Target{}, err
	}
	for _, item := range items {
		if item.ID == id {
			return item, nil
		}
	}
	return Target{}, ErrNotFound
}

func (s *Store) DeleteTarget(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "targets", id)
}
func (s *Store) UpdateTargetGroup(ctx context.Context, id, name, kind string) (TargetGroup, error) {
	name = strings.TrimSpace(name)
	if name == "" || (kind != protocol.TaskKindPing && kind != protocol.TaskKindTCPing) {
		return TargetGroup{}, errors.New("invalid group")
	}
	result, err := s.db.ExecContext(ctx, "UPDATE target_groups SET name=?,kind=?,updated_at=? WHERE id=?", name, kind, nowText(), id)
	if err != nil {
		return TargetGroup{}, err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return TargetGroup{}, ErrNotFound
	}
	items, err := s.ListTargetGroups(ctx)
	if err != nil {
		return TargetGroup{}, err
	}
	for _, item := range items {
		if item.ID == id {
			return item, nil
		}
	}
	return TargetGroup{}, ErrNotFound
}
func (s *Store) DeleteTargetGroup(ctx context.Context, id string) error {
	return deleteByID(ctx, s.db, "target_groups", id)
}
func (s *Store) RemoveTargetFromGroup(ctx context.Context, groupID, targetID string) error {
	return deletePair(ctx, s.db, "target_group_members", "group_id", groupID, "target_id", targetID)
}
func (s *Store) UnassignTargetGroup(ctx context.Context, nodeID, groupID string) error {
	return deletePair(ctx, s.db, "node_target_groups", "node_id", nodeID, "group_id", groupID)
}

func (s *Store) LogAudit(ctx context.Context, userID, action, objectType, objectID, remoteIP string, details any) error {
	raw, _ := json.Marshal(details)
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit_log(user_id,action,object_type,object_id,remote_ip,details_json,created_at) VALUES(?,?,?,?,?,?,?)`, userID, action, objectType, objectID, remoteIP, string(raw), nowText())
	return err
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatTime(*value)
}
func deleteByID(ctx context.Context, db *sql.DB, table, id string) error {
	result, err := db.ExecContext(ctx, "DELETE FROM "+table+" WHERE id=?", id)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return ErrNotFound
	}
	return nil
}
func deletePair(ctx context.Context, db *sql.DB, table, left string, leftValue any, right string, rightValue any) error {
	result, err := db.ExecContext(ctx, "DELETE FROM "+table+" WHERE "+left+"=? AND "+right+"=?", leftValue, rightValue)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return ErrNotFound
	}
	return nil
}
