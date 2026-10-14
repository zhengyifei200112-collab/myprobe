package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// NodeDiagnostics deliberately excludes hostnames, addresses and credentials.
type NodeDiagnostics struct {
	NodeID              string     `json:"node_id"`
	ReportSeconds       int        `json:"report_seconds"`
	LastSeenAt          *time.Time `json:"last_seen_at,omitempty"`
	AgentEvidenceStatus string     `json:"agent_evidence_status"`
	AgentVersion        string     `json:"agent_version,omitempty"`
	Capabilities        []string   `json:"capabilities,omitempty"`
	HelloReceivedAt     *time.Time `json:"hello_received_at,omitempty"`
}

func (s *Store) NodeDiagnostics(ctx context.Context, id string) (NodeDiagnostics, error) {
	result := NodeDiagnostics{AgentEvidenceStatus: "unavailable"}
	var seen, version, capabilities, hello sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT n.id,n.report_seconds,n.last_seen_at,
		m.agent_version,m.capabilities_json,m.updated_at FROM nodes n
		LEFT JOIN node_agent_metadata m ON m.node_id=n.id WHERE n.id=?`, id).
		Scan(&result.NodeID, &result.ReportSeconds, &seen, &version, &capabilities, &hello)
	if err != nil {
		return NodeDiagnostics{}, err
	}
	if seen.Valid {
		value, err := parseTime(seen.String)
		if err != nil {
			return NodeDiagnostics{}, err
		}
		result.LastSeenAt = &value
	}
	if hello.Valid {
		value, err := parseTime(hello.String)
		if err != nil {
			return NodeDiagnostics{}, err
		}
		if err := json.Unmarshal([]byte(capabilities.String), &result.Capabilities); err != nil {
			return NodeDiagnostics{}, err
		}
		result.HelloReceivedAt, result.AgentVersion = &value, version.String
		result.AgentEvidenceStatus = "advertised"
	}
	return result, nil
}
