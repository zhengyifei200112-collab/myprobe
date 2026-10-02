package store

import (
	"context"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

// AgentConfig reads the current desired intervals without exposing credentials.
func (s *Store) AgentConfig(ctx context.Context, nodeID string) (protocol.Config, error) {
	var config protocol.Config
	err := s.db.QueryRowContext(ctx, "SELECT collection_seconds, report_seconds FROM nodes WHERE id = ?", nodeID).Scan(&config.CollectionSeconds, &config.ReportSeconds)
	return config, err
}
