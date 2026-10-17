package store

import (
	"context"
	"encoding/hex"
	"errors"
	"time"
)

// RecordAgentHeartbeat refreshes contact, never metric samples/ingress or
// configuration revisions. The Gateway also holds its live-session fence;
// the durable epoch predicate rejects replacement even across DB writers.
func (s *Store) RecordAgentHeartbeat(ctx context.Context, nodeID, epoch string, now time.Time) error {
	if raw, err := hex.DecodeString(epoch); err != nil || len(raw) != 16 || now.IsZero() {
		return errors.New("invalid_agent_contact")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE nodes SET last_seen_at=? WHERE id=? AND EXISTS(SELECT 1 FROM node_configuration_evidence WHERE node_id=nodes.id AND connection_epoch=?)`, formatTime(now), nodeID, epoch)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil {
		return err
	} else if count != 1 {
		return ErrConfigurationSuperseded
	}
	return nil
}
