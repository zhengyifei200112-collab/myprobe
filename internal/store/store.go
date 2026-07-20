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
