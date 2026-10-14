package store

import (
	"context"
	"database/sql"
	"sort"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/diagnostics"
)

type ConfigurationCounts struct {
	Nodes                int64 `json:"nodes"`
	Users                int64 `json:"users"`
	Targets              int64 `json:"latency_targets"`
	TargetGroups         int64 `json:"target_groups"`
	HTTPServices         int64 `json:"http_services"`
	NotificationChannels int64 `json:"notification_channels"`
	AlertRules           int64 `json:"alert_rules"`
	AlertPolicies        int64 `json:"alert_policies"`
}
type AgentVersionCount struct {
	Version diagnostics.VersionObservation `json:"version"`
	Count   int64                          `json:"count"`
}
type ConfigurationDiagnostic struct {
	Status               string               `json:"status"`
	AgentVersionScope    string               `json:"agent_version_scope"`
	ObservedAt           time.Time            `json:"observed_at"`
	Counts               *ConfigurationCounts `json:"counts,omitempty"`
	AgentVersions        []AgentVersionCount  `json:"agent_versions,omitempty"`
	AgentUnrecognized    *int64               `json:"agent_version_unrecognized,omitempty"`
	AgentNoHello         *int64               `json:"agent_no_hello,omitempty"`
	AgentVersionOverflow *int64               `json:"agent_version_overflow,omitempty"`
}

// ConfigurationDiagnostic queries counts and bounded version text only, never
// names, notes, URLs, tokens, sessions, channel ciphertext or metric samples.
// All DB fields here share one read-only snapshot; runtime sources are separate.
func (s *Store) ConfigurationDiagnostic(ctx context.Context) ConfigurationDiagnostic {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	result := ConfigurationDiagnostic{Status: "unavailable", ObservedAt: time.Now().UTC(), AgentVersionScope: "stored_hello_metadata_not_current_connection"}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result
	}
	defer tx.Rollback()
	counts := ConfigurationCounts{}
	err = tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM nodes),(SELECT COUNT(*) FROM users),(SELECT COUNT(*) FROM targets),(SELECT COUNT(*) FROM target_groups),(SELECT COUNT(*) FROM http_services),(SELECT COUNT(*) FROM notification_channels),(SELECT COUNT(*) FROM alert_rules),(SELECT COUNT(*) FROM alert_policies)`).Scan(&counts.Nodes, &counts.Users, &counts.Targets, &counts.TargetGroups, &counts.HTTPServices, &counts.NotificationChannels, &counts.AlertRules, &counts.AlertPolicies)
	if err != nil {
		return result
	}
	rows, err := tx.QueryContext(ctx, `SELECT substr(COALESCE(m.agent_version,''),1,129),m.node_id IS NOT NULL FROM nodes n LEFT JOIN node_agent_metadata m ON m.node_id=n.id`)
	if err != nil {
		return result
	}
	versions := map[diagnostics.VersionObservation]int64{}
	unknown, noHello, overflow := int64(0), int64(0), int64(0)
	for rows.Next() {
		var raw string
		var hasHello bool
		if err = rows.Scan(&raw, &hasHello); err != nil {
			rows.Close()
			return result
		}
		if !hasHello {
			noHello++
			continue
		}
		version := diagnostics.ObserveVersion(raw)
		if version.Status == "unavailable" {
			unknown++
			continue
		}
		if _, ok := versions[version]; !ok && len(versions) >= 32 {
			overflow++
			continue
		}
		versions[version]++
	}
	err = rows.Err()
	rows.Close()
	if err != nil || tx.Commit() != nil {
		return result
	}
	result.Status = "available"
	result.Counts = &counts
	result.AgentUnrecognized = &unknown
	result.AgentNoHello = &noHello
	result.AgentVersionOverflow = &overflow
	for version, count := range versions {
		result.AgentVersions = append(result.AgentVersions, AgentVersionCount{Version: version, Count: count})
	}
	sort.Slice(result.AgentVersions, func(i, j int) bool {
		a, b := result.AgentVersions[i].Version, result.AgentVersions[j].Version
		if a.Status != b.Status {
			return a.Status < b.Status
		}
		if a.Base != b.Base {
			return a.Base < b.Base
		}
		if a.Prerelease != b.Prerelease {
			return !a.Prerelease
		}
		return !a.BuildMetadata && b.BuildMetadata
	})
	return result
}
