package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

var ErrNotFound = errors.New("not found")

type Store struct {
	db *sql.DB
}

func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("database path is required")
	}
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}
	dsn := sqliteDSN(path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	store := &Store{db: db}
	if err := store.Migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func sqliteDSN(path string) string {
	if path == ":memory:" {
		return "file:myprobe?mode=memory&cache=shared&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	}
	return "file:" + url.PathEscape(filepath.ToSlash(path)) +
		"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Health(ctx context.Context) error { return s.db.PingContext(ctx) }

func (s *Store) Migrate(ctx context.Context) error {
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		body, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		var applied int
		queryErr := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = ?", entry.Name()).Scan(&applied)
		if queryErr != nil && !strings.Contains(queryErr.Error(), "no such table") {
			return fmt.Errorf("check migration %s: %w", entry.Name(), queryErr)
		}
		if applied > 0 {
			continue
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", entry.Name(), err)
		}
		if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(?, ?)", entry.Name(), nowText()); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %s: %w", entry.Name(), err)
		}
		if err = tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func (s *Store) UserCount(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count)
	return count, err
}

func (s *Store) CreateUser(ctx context.Context, username, passwordHash string) (User, error) {
	now := time.Now().UTC()
	user := User{ID: randomID(), Username: username, PasswordHash: passwordHash, CreatedAt: now, UpdatedAt: now}
	_, err := s.db.ExecContext(ctx, `INSERT INTO users(id, username, password_hash, created_at, updated_at) VALUES(?, ?, ?, ?, ?)`,
		user.ID, user.Username, user.PasswordHash, formatTime(now), formatTime(now))
	return user, err
}

func (s *Store) UserByUsername(ctx context.Context, username string) (User, error) {
	var user User
	var created, updated string
	err := s.db.QueryRowContext(ctx, `SELECT id, username, password_hash, created_at, updated_at FROM users WHERE username = ?`, username).
		Scan(&user.ID, &user.Username, &user.PasswordHash, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	user.CreatedAt, _ = parseTime(created)
	user.UpdatedAt, _ = parseTime(updated)
	return user, nil
}

func (s *Store) CreateSession(ctx context.Context, userID string, ttl time.Duration) (Session, string, error) {
	token := randomToken(32)
	now := time.Now().UTC()
	session := Session{ID: randomID(), UserID: userID, CSRFToken: randomToken(24), ExpiresAt: now.Add(ttl)}
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions(id, user_id, token_hash, csrf_token, expires_at, created_at) VALUES(?, ?, ?, ?, ?, ?)`,
		session.ID, session.UserID, tokenHash(token), session.CSRFToken, formatTime(session.ExpiresAt), formatTime(now))
	return session, token, err
}

func (s *Store) SessionByToken(ctx context.Context, token string) (Session, error) {
	var session Session
	var expires string
	err := s.db.QueryRowContext(ctx, `SELECT id, user_id, csrf_token, expires_at FROM sessions WHERE token_hash = ? AND expires_at > ?`,
		tokenHash(token), nowText()).Scan(&session.ID, &session.UserID, &session.CSRFToken, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, err
	}
	session.ExpiresAt, _ = parseTime(expires)
	return session, nil
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash = ?", tokenHash(token))
	return err
}

func (s *Store) CreateNode(ctx context.Context, params CreateNodeParams) (Node, string, error) {
	if strings.TrimSpace(params.Name) == "" {
		return Node{}, "", errors.New("node name is required")
	}
	if params.ID == "" {
		params.ID = randomID()
	}
	if params.CollectionSeconds == 0 {
		params.CollectionSeconds = 5
	}
	if params.ReportSeconds == 0 {
		params.ReportSeconds = 5
	}
	tags, err := json.Marshal(params.Tags)
	if err != nil {
		return Node{}, "", err
	}
	now := time.Now().UTC()
	node := Node{
		ID: params.ID, Name: strings.TrimSpace(params.Name), Tags: append([]string(nil), params.Tags...),
		CountryCode: strings.ToUpper(strings.TrimSpace(params.CountryCode)), LatencyMode: "ping",
		CollectionSeconds: params.CollectionSeconds, ReportSeconds: params.ReportSeconds, CreatedAt: now, UpdatedAt: now,
	}
	token := randomToken(32)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Node{}, "", err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO nodes(id, name, tags_json, country_code, collection_seconds, report_seconds, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)`, node.ID, node.Name, string(tags), node.CountryCode, node.CollectionSeconds, node.ReportSeconds, formatTime(now), formatTime(now))
	if err != nil {
		return Node{}, "", err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO agent_tokens(id, node_id, token_hash, label, created_at) VALUES(?, ?, ?, 'primary', ?)`,
		randomID(), node.ID, tokenHash(token), formatTime(now))
	if err != nil {
		return Node{}, "", err
	}
	if err := tx.Commit(); err != nil {
		return Node{}, "", err
	}
	return node, token, nil
}

func (s *Store) AuthenticateAgent(ctx context.Context, token string) (Node, error) {
	if token == "" {
		return Node{}, ErrNotFound
	}
	var node Node
	var hidden, useSinceBoot int
	var tags, created, updated string
	var expiresAt, lastSeen sql.NullString
	var priceMinor sql.NullInt64
	var resetDay sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT n.id, n.name, n.sort_order, n.hidden, n.tags_json, n.country_code, n.currency,
		       n.price_minor, n.billing_cycle, n.expires_at, n.traffic_reset_day, n.use_since_boot,
		       n.latency_mode, n.custom_html, n.collection_seconds, n.report_seconds,
		       n.created_at, n.updated_at, n.last_seen_at
		FROM agent_tokens t JOIN nodes n ON n.id = t.node_id
		WHERE t.token_hash = ? AND t.revoked_at IS NULL`, tokenHash(token)).Scan(
		&node.ID, &node.Name, &node.SortOrder, &hidden, &tags, &node.CountryCode, &node.Currency,
		&priceMinor, &node.BillingCycle, &expiresAt, &resetDay, &useSinceBoot,
		&node.LatencyMode, &node.CustomHTML, &node.CollectionSeconds, &node.ReportSeconds,
		&created, &updated, &lastSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return Node{}, ErrNotFound
	}
	if err != nil {
		return Node{}, err
	}
	decodeNodeFields(&node, hidden, useSinceBoot, tags, created, updated, expiresAt, lastSeen, priceMinor, resetDay)
	_, _ = s.db.ExecContext(ctx, "UPDATE agent_tokens SET last_used_at = ? WHERE token_hash = ?", nowText(), tokenHash(token))
	return node, nil
}

func (s *Store) SaveReport(ctx context.Context, nodeID string, report protocol.Report) error {
	if err := report.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return err
	}
	var diskUsed, diskTotal uint64
	for _, disk := range report.Disks {
		diskUsed += disk.UsedBytes
		diskTotal += disk.TotalBytes
	}
	var rxRate, txRate float64
	var rxTotal, txTotal uint64
	for _, network := range report.Networks {
		rxRate += network.RXBytesPerS
		txRate += network.TXBytesPerS
		rxTotal += network.RXTotalBytes
		txTotal += network.TXTotalBytes
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO metric_latest(node_id, captured_at, report_json) VALUES(?, ?, ?)
		ON CONFLICT(node_id) DO UPDATE SET captured_at = excluded.captured_at, report_json = excluded.report_json`,
		nodeID, formatTime(report.CapturedAt), string(raw))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO metric_samples(
		node_id, captured_at, cpu_usage, memory_used, memory_total, disk_used, disk_total,
		net_rx_rate, net_tx_rate, net_rx_total, net_tx_total, report_json)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, nodeID, formatTime(report.CapturedAt), report.CPU.UsagePercent,
		report.Memory.UsedBytes, report.Memory.TotalBytes, diskUsed, diskTotal, rxRate, txRate, rxTotal, txTotal, string(raw))
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE nodes SET last_seen_at = ?, updated_at = ? WHERE id = ?", nowText(), nowText(), nodeID); err != nil {
		return err
	}
	if err = s.updateTrafficState(ctx, tx, nodeID, report.CapturedAt, rxTotal, txTotal); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListPublicNodes(ctx context.Context, now time.Time) ([]PublicNode, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT n.id, n.name, n.sort_order, n.hidden, n.tags_json, n.country_code,
		n.currency, n.price_minor, n.billing_cycle, n.expires_at, n.traffic_reset_day, n.use_since_boot,
		n.latency_mode, n.custom_html, n.collection_seconds, n.report_seconds, n.created_at, n.updated_at,
		n.last_seen_at, m.report_json
		FROM nodes n LEFT JOIN metric_latest m ON m.node_id = n.id
		WHERE n.hidden = 0 ORDER BY n.sort_order, n.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]PublicNode, 0)
	for rows.Next() {
		var node Node
		var hidden, useSinceBoot int
		var tags, created, updated string
		var expiresAt, lastSeen, reportJSON sql.NullString
		var priceMinor, resetDay sql.NullInt64
		if err := rows.Scan(&node.ID, &node.Name, &node.SortOrder, &hidden, &tags, &node.CountryCode,
			&node.Currency, &priceMinor, &node.BillingCycle, &expiresAt, &resetDay, &useSinceBoot,
			&node.LatencyMode, &node.CustomHTML, &node.CollectionSeconds, &node.ReportSeconds, &created, &updated,
			&lastSeen, &reportJSON); err != nil {
			return nil, err
		}
		decodeNodeFields(&node, hidden, useSinceBoot, tags, created, updated, expiresAt, lastSeen, priceMinor, resetDay)
		item := PublicNode{Node: node}
		if node.LastSeenAt != nil {
			threshold := time.Duration(max(node.ReportSeconds*3, 20)) * time.Second
			item.Online = now.Sub(*node.LastSeenAt) <= threshold
			item.Stale = now.Sub(*node.LastSeenAt) > time.Duration(max(node.ReportSeconds*2, 12))*time.Second
		}
		if reportJSON.Valid {
			var report protocol.Report
			if json.Unmarshal([]byte(reportJSON.String), &report) == nil {
				item.Report = &report
			}
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for index := range result {
		latency, err := s.ListLatestLatency(ctx, result[index].Node.ID)
		if err != nil {
			return nil, err
		}
		result[index].Latency = latency
		traffic, err := s.TrafficUsage(ctx, result[index].Node.ID, result[index].Node.TrafficResetDay, now)
		if err != nil {
			return nil, err
		}
		result[index].Traffic = traffic
	}
	return result, nil
}

func (s *Store) CreateTarget(ctx context.Context, params CreateTargetParams) (Target, error) {
	params.Name = strings.TrimSpace(params.Name)
	params.Host = strings.TrimSpace(params.Host)
	if params.Name == "" {
		return Target{}, errors.New("target name is required")
	}
	if params.IntervalSeconds == 0 {
		params.IntervalSeconds = 60
	}
	if params.TimeoutMS == 0 {
		params.TimeoutMS = 5000
	}
	port := 0
	if params.Port != nil {
		port = *params.Port
	}
	task := protocol.Task{ID: "validation", TargetID: "validation", Kind: params.Kind, Host: params.Host, Port: port, TimeoutMS: params.TimeoutMS, ExpiresAt: time.Now().UTC().Add(time.Minute)}
	if err := task.Validate(time.Now().UTC()); err != nil {
		return Target{}, err
	}
	if params.IntervalSeconds < 5 || params.IntervalSeconds > 86400 {
		return Target{}, errors.New("target interval must be between 5 and 86400 seconds")
	}
	now := time.Now().UTC()
	target := Target{ID: randomID(), Name: params.Name, Kind: params.Kind, Host: params.Host, Port: params.Port, IntervalSeconds: params.IntervalSeconds, TimeoutMS: params.TimeoutMS, Enabled: true, CreatedAt: now, UpdatedAt: now}
	_, err := s.db.ExecContext(ctx, `INSERT INTO targets(id, name, kind, host, port, interval_seconds, timeout_ms, enabled, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, 1, ?, ?)`, target.ID, target.Name, target.Kind, target.Host, target.Port, target.IntervalSeconds, target.TimeoutMS, formatTime(now), formatTime(now))
	return target, err
}

func (s *Store) CreateTargetGroup(ctx context.Context, name, kind string) (TargetGroup, error) {
	name = strings.TrimSpace(name)
	if name == "" || (kind != protocol.TaskKindPing && kind != protocol.TaskKindTCPing) {
		return TargetGroup{}, errors.New("target group name and valid kind are required")
	}
	now := time.Now().UTC()
	group := TargetGroup{ID: randomID(), Name: name, Kind: kind, CreatedAt: now, UpdatedAt: now}
	_, err := s.db.ExecContext(ctx, `INSERT INTO target_groups(id, name, kind, created_at, updated_at) VALUES(?, ?, ?, ?, ?)`, group.ID, group.Name, group.Kind, formatTime(now), formatTime(now))
	return group, err
}

func (s *Store) AddTargetToGroup(ctx context.Context, groupID, targetID string) error {
	result, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO target_group_members(group_id, target_id)
		SELECT g.id, t.id FROM target_groups g JOIN targets t ON t.id = ? AND t.kind = g.kind WHERE g.id = ?`, targetID, groupID)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		var exists int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM target_group_members WHERE group_id = ? AND target_id = ?`, groupID, targetID).Scan(&exists); err != nil || exists == 0 {
			return ErrNotFound
		}
	}
	return nil
}

func (s *Store) AssignTargetGroup(ctx context.Context, nodeID, groupID string) error {
	result, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO node_target_groups(node_id, group_id)
		SELECT n.id, g.id FROM nodes n CROSS JOIN target_groups g WHERE n.id = ? AND g.id = ?`, nodeID, groupID)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		var exists int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM node_target_groups WHERE node_id = ? AND group_id = ?`, nodeID, groupID).Scan(&exists); err != nil || exists == 0 {
			return ErrNotFound
		}
	}
	return nil
}

func (s *Store) ListTargets(ctx context.Context) ([]Target, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, kind, host, port, interval_seconds, timeout_ms, enabled, sort_order, created_at, updated_at FROM targets ORDER BY sort_order, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Target, 0)
	for rows.Next() {
		var item Target
		var port sql.NullInt64
		var enabled int
		var created, updated string
		if err := rows.Scan(&item.ID, &item.Name, &item.Kind, &item.Host, &port, &item.IntervalSeconds, &item.TimeoutMS, &enabled, &item.SortOrder, &created, &updated); err != nil {
			return nil, err
		}
		item.Enabled = enabled != 0
		item.CreatedAt, _ = parseTime(created)
		item.UpdatedAt, _ = parseTime(updated)
		if port.Valid {
			value := int(port.Int64)
			item.Port = &value
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) ListTargetGroups(ctx context.Context) ([]TargetGroup, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, kind, created_at, updated_at FROM target_groups ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]TargetGroup, 0)
	for rows.Next() {
		var item TargetGroup
		var created, updated string
		if err := rows.Scan(&item.ID, &item.Name, &item.Kind, &created, &updated); err != nil {
			return nil, err
		}
		item.CreatedAt, _ = parseTime(created)
		item.UpdatedAt, _ = parseTime(updated)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) ListTargetAssignments(ctx context.Context) ([]TargetAssignment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT ng.node_id, t.id, t.name, t.kind, t.host, t.port,
		t.interval_seconds, t.timeout_ms, t.enabled, t.sort_order, t.created_at, t.updated_at
		FROM node_target_groups ng
		JOIN target_group_members gm ON gm.group_id = ng.group_id
		JOIN targets t ON t.id = gm.target_id
		WHERE t.enabled = 1 ORDER BY ng.node_id, t.sort_order, t.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]TargetAssignment, 0)
	for rows.Next() {
		var assignment TargetAssignment
		var port sql.NullInt64
		var enabled int
		var created, updated string
		if err := rows.Scan(&assignment.NodeID, &assignment.Target.ID, &assignment.Target.Name, &assignment.Target.Kind,
			&assignment.Target.Host, &port, &assignment.Target.IntervalSeconds, &assignment.Target.TimeoutMS, &enabled,
			&assignment.Target.SortOrder, &created, &updated); err != nil {
			return nil, err
		}
		assignment.Target.Enabled = enabled != 0
		assignment.Target.CreatedAt, _ = parseTime(created)
		assignment.Target.UpdatedAt, _ = parseTime(updated)
		if port.Valid {
			value := int(port.Int64)
			assignment.Target.Port = &value
		}
		result = append(result, assignment)
	}
	return result, rows.Err()
}

func (s *Store) SaveLatencyResult(ctx context.Context, nodeID, kind string, result protocol.LatencyResult) error {
	if kind != protocol.TaskKindPing && kind != protocol.TaskKindTCPing {
		return errors.New("invalid latency kind")
	}
	if err := result.Validate(time.Now().UTC()); err != nil {
		return err
	}
	var latency any
	if result.Success {
		latency = result.LatencyMS
	}
	stored, err := s.db.ExecContext(ctx, `INSERT INTO latency_samples(node_id, target_id, kind, captured_at, success, latency_ms, error_class)
		SELECT ?, t.id, t.kind, ?, ?, ?, ? FROM targets t
		WHERE t.id = ? AND t.kind = ? AND EXISTS (
			SELECT 1 FROM target_group_members gm JOIN node_target_groups ng ON ng.group_id = gm.group_id
			WHERE gm.target_id = t.id AND ng.node_id = ?
		)`, nodeID, formatTime(result.CompletedAt), result.Success, latency, result.ErrorClass, result.TargetID, kind, nodeID)
	if err != nil {
		return err
	}
	count, _ := stored.RowsAffected()
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListLatestLatency(ctx context.Context, nodeID string) ([]LatestLatency, error) {
	rows, err := s.db.QueryContext(ctx, `WITH assigned AS (
		SELECT DISTINCT t.id, t.name, t.kind, t.sort_order
		FROM node_target_groups ng
		JOIN target_group_members gm ON gm.group_id = ng.group_id
		JOIN targets t ON t.id = gm.target_id
		WHERE ng.node_id = ? AND t.enabled = 1
	)
	SELECT a.id, a.name, a.kind, l.captured_at, l.success, l.latency_ms, l.error_class
	FROM assigned a LEFT JOIN latency_samples l ON l.id = (
		SELECT id FROM latency_samples WHERE node_id = ? AND target_id = a.id ORDER BY captured_at DESC LIMIT 1
	) ORDER BY a.sort_order, a.name`, nodeID, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]LatestLatency, 0)
	for rows.Next() {
		var item LatestLatency
		var captured, errorClass sql.NullString
		var success sql.NullInt64
		var latency sql.NullFloat64
		if err := rows.Scan(&item.TargetID, &item.Name, &item.Kind, &captured, &success, &latency, &errorClass); err != nil {
			return nil, err
		}
		if captured.Valid {
			value, parseErr := parseTime(captured.String)
			if parseErr == nil {
				item.UpdatedAt = &value
			}
		}
		if success.Valid {
			value := success.Int64 != 0
			item.Success = &value
		}
		if latency.Valid {
			value := latency.Float64
			item.LatencyMS = &value
		}
		if errorClass.Valid {
			item.Error = errorClass.String
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) MetricHistory(ctx context.Context, nodeID string, start time.Time, bucketSeconds int) ([]MetricHistoryPoint, error) {
	if bucketSeconds < 1 || bucketSeconds > 86400 {
		return nil, errors.New("invalid history bucket")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT (unixepoch(m.captured_at) / ?) * ? AS bucket,
		AVG(m.cpu_usage),
		AVG(CASE WHEN m.memory_total > 0 THEN 100.0 * m.memory_used / m.memory_total ELSE 0 END),
		AVG(CASE WHEN m.disk_total > 0 THEN 100.0 * m.disk_used / m.disk_total ELSE 0 END),
		AVG(m.net_rx_rate), AVG(m.net_tx_rate)
		FROM metric_samples m JOIN nodes n ON n.id = m.node_id
		WHERE m.node_id = ? AND n.hidden = 0 AND m.captured_at >= ?
		GROUP BY bucket ORDER BY bucket`, bucketSeconds, bucketSeconds, nodeID, formatTime(start))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]MetricHistoryPoint, 0)
	for rows.Next() {
		var point MetricHistoryPoint
		var unixSeconds int64
		if err := rows.Scan(&unixSeconds, &point.CPUPercent, &point.MemoryPercent, &point.DiskPercent, &point.RXBytesPerS, &point.TXBytesPerS); err != nil {
			return nil, err
		}
		point.Time = time.Unix(unixSeconds, 0).UTC()
		result = append(result, point)
	}
	return result, rows.Err()
}

func (s *Store) LatencyHistory(ctx context.Context, nodeID string, start time.Time, bucketSeconds int) ([]LatencyHistoryPoint, error) {
	if bucketSeconds < 1 || bucketSeconds > 86400 {
		return nil, errors.New("invalid history bucket")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT (unixepoch(l.captured_at) / ?) * ? AS bucket,
		l.target_id, t.name, l.kind,
		AVG(CASE WHEN l.success = 1 THEN l.latency_ms END), 100.0 * AVG(l.success)
		FROM latency_samples l
		JOIN targets t ON t.id = l.target_id
		JOIN nodes n ON n.id = l.node_id
		WHERE l.node_id = ? AND n.hidden = 0 AND l.captured_at >= ?
		GROUP BY bucket, l.target_id, t.name, l.kind ORDER BY bucket, t.sort_order, t.name`, bucketSeconds, bucketSeconds, nodeID, formatTime(start))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]LatencyHistoryPoint, 0)
	for rows.Next() {
		var point LatencyHistoryPoint
		var unixSeconds int64
		var latency sql.NullFloat64
		if err := rows.Scan(&unixSeconds, &point.TargetID, &point.Name, &point.Kind, &latency, &point.SuccessRate); err != nil {
			return nil, err
		}
		point.Time = time.Unix(unixSeconds, 0).UTC()
		if latency.Valid {
			value := latency.Float64
			point.LatencyMS = &value
		}
		result = append(result, point)
	}
	return result, rows.Err()
}

func decodeNodeFields(node *Node, hidden, useSinceBoot int, tags, created, updated string, expiresAt, lastSeen sql.NullString, priceMinor, resetDay sql.NullInt64) {
	node.Hidden = hidden != 0
	node.UseSinceBoot = useSinceBoot != 0
	_ = json.Unmarshal([]byte(tags), &node.Tags)
	node.CreatedAt, _ = parseTime(created)
	node.UpdatedAt, _ = parseTime(updated)
	if expiresAt.Valid {
		value, err := parseTime(expiresAt.String)
		if err == nil {
			node.ExpiresAt = &value
		}
	}
	if lastSeen.Valid {
		value, err := parseTime(lastSeen.String)
		if err == nil {
			node.LastSeenAt = &value
		}
	}
	if priceMinor.Valid {
		value := priceMinor.Int64
		node.PriceMinor = &value
	}
	if resetDay.Valid {
		value := int(resetDay.Int64)
		node.TrafficResetDay = &value
	}
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func randomID() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(buffer)
}

func randomToken(size int) string {
	buffer := make([]byte, size)
	if _, err := rand.Read(buffer); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(buffer)
}

func formatTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }
func nowText() string                   { return formatTime(time.Now().UTC()) }
func parseTime(value string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, value)
}
