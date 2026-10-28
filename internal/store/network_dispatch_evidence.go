package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	v1 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

// A compatibility projection for historical counters, explicitly sourced from
// the dispatcher. It is never an Agent receipt, sample, or ACK authorization.
type networkDispatchEvidence struct {
	v1.LatencyResult
	Source string `json:"source"`
}

func (s *Store) RecordUnsupportedNetworkDispatch(ctx context.Context, task NetworkTask, now time.Time) error {
	raw, err := json.Marshal(networkDispatchEvidence{LatencyResult: v1.LatencyResult{TaskID: task.Task.ID, TargetID: task.Task.TargetID, ErrorClass: "unsupported", CompletedAt: now.UTC()}, Source: "server_dispatch"})
	if err != nil {
		return err
	}
	digest := sha256.Sum256(raw)
	result, err := s.db.ExecContext(ctx, `UPDATE network_probe_tasks SET state='completed',outcome='not_executed',received_at_ns=?,result_sha=?,result_json=?,result_format='dispatch.v1' WHERE id=? AND node_id=? AND target_id=? AND lease_token=? AND state='sending' AND sent_at_ns IS NULL AND evidence_required=0 AND address_family=? AND packet_count=?`, now.UnixNano(), hex.EncodeToString(digest[:]), string(raw), task.Task.ID, task.NodeID, task.Task.TargetID, task.LeaseToken, task.AddressFamily, task.PacketCount)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrNetworkTaskRejected
	}
	return nil
}
