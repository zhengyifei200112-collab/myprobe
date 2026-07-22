package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

const ConfigSnapshotVersion = 1

var portableID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

type ConfigSnapshot struct {
	Version      int                 `json:"version"`
	ExportedAt   time.Time           `json:"exported_at"`
	Nodes        []ConfigNode        `json:"nodes"`
	Targets      []ConfigTarget      `json:"targets"`
	TargetGroups []ConfigTargetGroup `json:"target_groups"`
	GroupMembers []TargetGroupMember `json:"group_members"`
	NodeGroups   []NodeTargetGroup   `json:"node_groups"`
}

type ConfigNode struct {
	ID                string        `json:"id"`
	Name              string        `json:"name"`
	SortOrder         int           `json:"sort_order"`
	Hidden            bool          `json:"hidden"`
	Tags              []string      `json:"tags"`
	CountryCode       string        `json:"country_code"`
	Currency          string        `json:"currency"`
	PriceMinor        *int64        `json:"price_minor,omitempty"`
	BillingCycle      string        `json:"billing_cycle"`
	ExpiresAt         *time.Time    `json:"expires_at,omitempty"`
	TrafficResetDay   *int          `json:"traffic_reset_day,omitempty"`
	UseSinceBoot      bool          `json:"use_since_boot"`
	LatencyMode       string        `json:"latency_mode"`
	CustomHTML        string        `json:"custom_html,omitempty"`
	CustomBadges      []CustomBadge `json:"custom_badges,omitempty"`
	CustomLinks       []CustomLink  `json:"custom_links,omitempty"`
	CollectionSeconds int           `json:"collection_seconds"`
	ReportSeconds     int           `json:"report_seconds"`
}

type ConfigTarget struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Kind            string `json:"kind"`
	Host            string `json:"host"`
	Port            *int   `json:"port,omitempty"`
	IntervalSeconds int    `json:"interval_seconds"`
	TimeoutMS       int    `json:"timeout_ms"`
	Enabled         bool   `json:"enabled"`
	SortOrder       int    `json:"sort_order"`
}

type ConfigTargetGroup struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type ConfigImportResult struct {
	NodesCreated   int               `json:"nodes_created"`
	NodesUpdated   int               `json:"nodes_updated"`
	TargetsAdded   int               `json:"targets_created"`
	TargetsUpdated int               `json:"targets_updated"`
	GroupsAdded    int               `json:"groups_created"`
	GroupsUpdated  int               `json:"groups_updated"`
	MembersAdded   int               `json:"memberships_created"`
	AgentTokens    map[string]string `json:"agent_tokens,omitempty"`
	DryRun         bool              `json:"dry_run"`
}

func (s *Store) ExportConfig(ctx context.Context, now time.Time) (ConfigSnapshot, error) {
	nodes, err := s.ListNodes(ctx)
	if err != nil {
		return ConfigSnapshot{}, err
	}
	targets, err := s.ListTargets(ctx)
	if err != nil {
		return ConfigSnapshot{}, err
	}
	groups, err := s.ListTargetGroups(ctx)
	if err != nil {
		return ConfigSnapshot{}, err
	}
	members, err := s.ListTargetGroupMembers(ctx)
	if err != nil {
		return ConfigSnapshot{}, err
	}
	nodeGroups, err := s.ListNodeTargetGroups(ctx)
	if err != nil {
		return ConfigSnapshot{}, err
	}
	snapshot := ConfigSnapshot{Version: ConfigSnapshotVersion, ExportedAt: now.UTC(), GroupMembers: members, NodeGroups: nodeGroups}
	for _, item := range nodes {
		snapshot.Nodes = append(snapshot.Nodes, ConfigNode{ID: item.ID, Name: item.Name, SortOrder: item.SortOrder, Hidden: item.Hidden, Tags: item.Tags, CountryCode: item.CountryCode, Currency: item.Currency, PriceMinor: item.PriceMinor, BillingCycle: item.BillingCycle, ExpiresAt: item.ExpiresAt, TrafficResetDay: item.TrafficResetDay, UseSinceBoot: item.UseSinceBoot, LatencyMode: item.LatencyMode, CustomHTML: item.CustomHTML, CustomBadges: item.CustomBadges, CustomLinks: item.CustomLinks, CollectionSeconds: item.CollectionSeconds, ReportSeconds: item.ReportSeconds})
	}
	for _, item := range targets {
		snapshot.Targets = append(snapshot.Targets, ConfigTarget{ID: item.ID, Name: item.Name, Kind: item.Kind, Host: item.Host, Port: item.Port, IntervalSeconds: item.IntervalSeconds, TimeoutMS: item.TimeoutMS, Enabled: item.Enabled, SortOrder: item.SortOrder})
	}
	for _, item := range groups {
		snapshot.TargetGroups = append(snapshot.TargetGroups, ConfigTargetGroup{ID: item.ID, Name: item.Name, Kind: item.Kind})
	}
	return snapshot, nil
}

func (s *Store) ImportConfig(ctx context.Context, snapshot ConfigSnapshot, dryRun bool) (ConfigImportResult, error) {
	if err := validateConfigSnapshot(snapshot); err != nil {
		return ConfigImportResult{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ConfigImportResult{}, err
	}
	defer tx.Rollback()
	result := ConfigImportResult{AgentTokens: make(map[string]string), DryRun: dryRun}
	now := nowText()
	for _, item := range snapshot.Nodes {
		exists, err := rowExists(ctx, tx, "nodes", item.ID)
		if err != nil {
			return result, err
		}
		tags, _ := json.Marshal(item.Tags)
		customHTML, badges, links, normalizeErr := normalizeCustomDisplay(item.CustomHTML, item.CustomBadges, item.CustomLinks)
		if normalizeErr != nil {
			return result, fmt.Errorf("import node %s: %w", item.ID, normalizeErr)
		}
		badgesJSON, _ := json.Marshal(badges)
		linksJSON, _ := json.Marshal(links)
		_, err = tx.ExecContext(ctx, `INSERT INTO nodes(id,name,sort_order,hidden,tags_json,country_code,currency,price_minor,billing_cycle,expires_at,traffic_reset_day,use_since_boot,latency_mode,custom_html,custom_badges_json,custom_links_json,collection_seconds,report_seconds,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,sort_order=excluded.sort_order,hidden=excluded.hidden,tags_json=excluded.tags_json,country_code=excluded.country_code,currency=excluded.currency,price_minor=excluded.price_minor,billing_cycle=excluded.billing_cycle,expires_at=excluded.expires_at,traffic_reset_day=excluded.traffic_reset_day,use_since_boot=excluded.use_since_boot,latency_mode=excluded.latency_mode,custom_html=excluded.custom_html,custom_badges_json=excluded.custom_badges_json,custom_links_json=excluded.custom_links_json,collection_seconds=excluded.collection_seconds,report_seconds=excluded.report_seconds,updated_at=excluded.updated_at`,
			item.ID, strings.TrimSpace(item.Name), item.SortOrder, item.Hidden, string(tags), strings.ToUpper(strings.TrimSpace(item.CountryCode)), strings.ToUpper(strings.TrimSpace(item.Currency)), item.PriceMinor, strings.TrimSpace(item.BillingCycle), nullableTime(item.ExpiresAt), item.TrafficResetDay, item.UseSinceBoot, item.LatencyMode, customHTML, string(badgesJSON), string(linksJSON), item.CollectionSeconds, item.ReportSeconds, now, now)
		if err != nil {
			return result, fmt.Errorf("import node %s: %w", item.ID, err)
		}
		if exists {
			result.NodesUpdated++
		} else {
			result.NodesCreated++
			token := randomToken(32)
			if _, err := tx.ExecContext(ctx, `INSERT INTO agent_tokens(id,node_id,token_hash,label,created_at) VALUES(?,?,?,'imported',?)`, randomID(), item.ID, tokenHash(token), now); err != nil {
				return result, err
			}
			result.AgentTokens[item.ID] = token
		}
	}
	for _, item := range snapshot.Targets {
		exists, err := rowExists(ctx, tx, "targets", item.ID)
		if err != nil {
			return result, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO targets(id,name,kind,host,port,interval_seconds,timeout_ms,enabled,sort_order,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,kind=excluded.kind,host=excluded.host,port=excluded.port,interval_seconds=excluded.interval_seconds,timeout_ms=excluded.timeout_ms,enabled=excluded.enabled,sort_order=excluded.sort_order,updated_at=excluded.updated_at`, item.ID, strings.TrimSpace(item.Name), item.Kind, strings.TrimSpace(item.Host), item.Port, item.IntervalSeconds, item.TimeoutMS, item.Enabled, item.SortOrder, now, now)
		if err != nil {
			return result, fmt.Errorf("import target %s: %w", item.ID, err)
		}
		if exists {
			result.TargetsUpdated++
		} else {
			result.TargetsAdded++
		}
	}
	for _, item := range snapshot.TargetGroups {
		exists, err := rowExists(ctx, tx, "target_groups", item.ID)
		if err != nil {
			return result, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO target_groups(id,name,kind,created_at,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,kind=excluded.kind,updated_at=excluded.updated_at`, item.ID, strings.TrimSpace(item.Name), item.Kind, now, now)
		if err != nil {
			return result, fmt.Errorf("import target group %s: %w", item.ID, err)
		}
		if exists {
			result.GroupsUpdated++
		} else {
			result.GroupsAdded++
		}
	}
	for _, item := range snapshot.GroupMembers {
		changed, err := insertPair(ctx, tx, "target_group_members", "group_id", item.GroupID, "target_id", item.TargetID)
		if err != nil {
			return result, fmt.Errorf("import target group membership: %w", err)
		}
		if changed {
			result.MembersAdded++
		}
	}
	for _, item := range snapshot.NodeGroups {
		changed, err := insertPair(ctx, tx, "node_target_groups", "node_id", item.NodeID, "group_id", item.GroupID)
		if err != nil {
			return result, fmt.Errorf("import node group assignment: %w", err)
		}
		if changed {
			result.MembersAdded++
		}
	}
	var mismatched int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM target_group_members gm JOIN target_groups g ON g.id=gm.group_id JOIN targets t ON t.id=gm.target_id WHERE g.kind<>t.kind`).Scan(&mismatched); err != nil {
		return result, err
	}
	if mismatched != 0 {
		return result, errors.New("configuration creates target/group kind mismatches")
	}
	if dryRun {
		result.AgentTokens = nil
		return result, nil
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}

func validateConfigSnapshot(snapshot ConfigSnapshot) error {
	if snapshot.Version != ConfigSnapshotVersion {
		return fmt.Errorf("unsupported config version %d", snapshot.Version)
	}
	if len(snapshot.Nodes) > 10000 || len(snapshot.Targets) > 10000 || len(snapshot.TargetGroups) > 10000 || len(snapshot.GroupMembers)+len(snapshot.NodeGroups) > 100000 {
		return errors.New("configuration exceeds import limits")
	}
	seen := make(map[string]string)
	checkID := func(kind, id string) error {
		if !portableID.MatchString(id) {
			return fmt.Errorf("invalid %s id %q", kind, id)
		}
		key := kind + ":" + id
		if seen[key] != "" {
			return fmt.Errorf("duplicate %s id %q", kind, id)
		}
		seen[key] = id
		return nil
	}
	for _, item := range snapshot.Nodes {
		if err := checkID("node", item.ID); err != nil {
			return err
		}
		if strings.TrimSpace(item.Name) == "" || len(item.Name) > 200 || item.CollectionSeconds < 1 || item.CollectionSeconds > 3600 || item.ReportSeconds < 1 || item.ReportSeconds > 3600 {
			return fmt.Errorf("invalid node %s", item.ID)
		}
		if item.LatencyMode != protocol.TaskKindPing && item.LatencyMode != protocol.TaskKindTCPing {
			return fmt.Errorf("invalid latency mode for node %s", item.ID)
		}
		if item.TrafficResetDay != nil && (*item.TrafficResetDay < 1 || *item.TrafficResetDay > 31) {
			return fmt.Errorf("invalid traffic reset day for node %s", item.ID)
		}
	}
	for _, item := range snapshot.Targets {
		if err := checkID("target", item.ID); err != nil {
			return err
		}
		port := 0
		if item.Port != nil {
			port = *item.Port
		}
		task := protocol.Task{ID: "validation", TargetID: item.ID, Kind: item.Kind, Host: strings.TrimSpace(item.Host), Port: port, TimeoutMS: item.TimeoutMS, ExpiresAt: time.Now().UTC().Add(time.Minute)}
		if strings.TrimSpace(item.Name) == "" || item.IntervalSeconds < 5 || item.IntervalSeconds > 86400 || task.Validate(time.Now().UTC()) != nil {
			return fmt.Errorf("invalid target %s", item.ID)
		}
	}
	for _, item := range snapshot.TargetGroups {
		if err := checkID("group", item.ID); err != nil {
			return err
		}
		if strings.TrimSpace(item.Name) == "" || (item.Kind != protocol.TaskKindPing && item.Kind != protocol.TaskKindTCPing) {
			return fmt.Errorf("invalid target group %s", item.ID)
		}
	}
	return nil
}

func rowExists(ctx context.Context, tx *sql.Tx, table, id string) (bool, error) {
	var count int
	err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE id=?", id).Scan(&count)
	return count == 1, err
}

func insertPair(ctx context.Context, tx *sql.Tx, table, left string, leftValue any, right string, rightValue any) (bool, error) {
	result, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO "+table+"("+left+","+right+") VALUES(?,?)", leftValue, rightValue)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count > 0, err
}
