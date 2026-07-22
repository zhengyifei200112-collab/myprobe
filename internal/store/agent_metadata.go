package store

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"time"

	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

func (s *Store) SaveAgentMetadata(ctx context.Context, nodeID string, hello protocol.Hello) error {
	if err := hello.Validate(); err != nil {
		return err
	}
	capabilities, _ := json.Marshal(hello.Capabilities)
	_, err := s.db.ExecContext(ctx, `INSERT INTO node_agent_metadata(node_id,hostname,operating_system,platform,platform_version,kernel_version,architecture,agent_version,capabilities_json,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(node_id) DO UPDATE SET hostname=excluded.hostname,operating_system=excluded.operating_system,platform=excluded.platform,platform_version=excluded.platform_version,kernel_version=excluded.kernel_version,architecture=excluded.architecture,agent_version=excluded.agent_version,capabilities_json=excluded.capabilities_json,updated_at=excluded.updated_at`,
		nodeID, strings.TrimSpace(hello.Hostname), strings.TrimSpace(hello.OS), strings.TrimSpace(hello.Platform), strings.TrimSpace(hello.PlatformVersion), strings.TrimSpace(hello.KernelVersion), strings.TrimSpace(hello.Architecture), strings.TrimSpace(hello.AgentVersion), string(capabilities), nowText())
	return err
}

func (s *Store) agentMetadata(ctx context.Context) (map[string]AgentMetadata, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT node_id,hostname,operating_system,platform,platform_version,kernel_version,architecture,agent_version,capabilities_json,updated_at FROM node_agent_metadata`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]AgentMetadata)
	for rows.Next() {
		var nodeID, capabilities, updated string
		var item AgentMetadata
		if err := rows.Scan(&nodeID, &item.Hostname, &item.OperatingSystem, &item.Platform, &item.PlatformVersion, &item.KernelVersion, &item.Architecture, &item.AgentVersion, &capabilities, &updated); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(capabilities), &item.Capabilities)
		item.UpdatedAt, _ = parseTime(updated)
		result[nodeID] = item
	}
	return result, rows.Err()
}

func attachAgentMetadata(nodes []Node, metadata map[string]AgentMetadata) {
	for index := range nodes {
		if item, ok := metadata[nodes[index].ID]; ok {
			copy := item
			nodes[index].Agent = &copy
		}
	}
}

func ComputeCommercialStatus(expiresAt *time.Time, now time.Time) *CommercialStatus {
	if expiresAt == nil {
		return nil
	}
	delta := expiresAt.Sub(now)
	expired := delta < 0
	if expired {
		delta = -delta
	}
	days := 0
	if delta > 0 {
		days = int(math.Ceil(delta.Hours() / 24))
	}
	return &CommercialStatus{Expired: expired, Days: days}
}
