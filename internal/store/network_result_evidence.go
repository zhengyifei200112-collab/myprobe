package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	check "github.com/zhengyifei200112-collab/myprobe/internal/protocol/networkcheck"
	v1 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

func currentNetworkConnection(ctx context.Context, tx *sql.Tx, node, connection string) error {
	if len(connection) != 32 {
		return ErrNetworkTaskRejected
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM node_configuration_evidence WHERE node_id=? AND connection_epoch=? AND wire_version=2`, node, connection).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return ErrNetworkTaskRejected
	}
	return nil
}

// Bind before the first possible socket write. An uncertain send never falls
// back to v1 or becomes retryable; a legacy result cannot satisfy this task.
func (s *Store) RequireNetworkEvidence(ctx context.Context, task NetworkTask, connection string) error {
	tx, err := s.networkWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = currentNetworkConnection(ctx, tx, task.NodeID, connection); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE network_probe_tasks SET evidence_required=1 WHERE id=? AND node_id=? AND lease_token=? AND state='sending' AND evidence_required=0 AND address_family=? AND packet_count=?`, task.Task.ID, task.NodeID, task.LeaseToken, task.AddressFamily, task.PacketCount)
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
	return tx.Commit()
}

// Admission and duplicate ACK permission are one SQLite transaction, fenced by
// the current receiving connection. Original dispatch may predate a reconnect;
// only the authenticated node/current generation may replay its durable result.
func (s *Store) AcceptNetworkProbeResult(ctx context.Context, node, connection string, result check.Result, now time.Time) (bool, error) {
	if result.Validate(now) != nil {
		return false, ErrNetworkTaskRejected
	}
	result.CompletedAt = result.CompletedAt.UTC()
	raw, err := json.Marshal(result)
	if err != nil {
		return false, ErrNetworkTaskRejected
	}
	digest := sha256.Sum256(raw)
	fingerprint := hex.EncodeToString(digest[:])
	tx, err := s.networkWrite(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err = currentNetworkConnection(ctx, tx, node, connection); err != nil {
		return false, err
	}
	var task NetworkTask
	var state, format string
	var epoch, previous sql.NullString
	var port sql.NullInt64
	var expiry int64
	var required int
	err = tx.QueryRowContext(ctx, `SELECT id,node_id,target_id,kind,host,port,timeout_ms,address_family,packet_count,expires_at_ns,epoch_id,state,result_format,evidence_required,result_sha FROM network_probe_tasks WHERE id=?`, result.TaskID).Scan(&task.Task.ID, &task.NodeID, &task.Task.TargetID, &task.Task.Kind, &task.Task.Host, &port, &task.Task.TimeoutMS, &task.AddressFamily, &task.PacketCount, &expiry, &epoch, &state, &format, &required, &previous)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNetworkTaskRejected
	}
	if err != nil {
		return false, err
	}
	task.Task.Port = int(port.Int64)
	task.Task.ExpiresAt = time.Unix(0, expiry).UTC()
	if task.NodeID != node || required != 1 || result.ValidateAgainst(task.ProbeTask(), now) != nil {
		return false, ErrNetworkTaskRejected
	}
	if state == "completed" {
		if format != "network.v1" || !previous.Valid || previous.String != fingerprint {
			return false, ErrNetworkTaskRejected
		}
		return false, tx.Commit()
	}
	if format != "v1" || (state != "sending" && state != "sent" && state != "sent_unknown") || now.UnixNano() > expiry {
		return false, ErrNetworkTaskRejected
	}
	if epoch.Valid {
		var active int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM network_schedule_epochs WHERE id=? AND ends_at IS NULL`, epoch.String).Scan(&active); err != nil {
			return false, err
		}
		if active != 1 {
			return false, ErrNetworkTaskRejected
		}
	}
	outcome := "not_executed"
	if result.Outcome != check.Unobserved {
		legacy := v1.LatencyResult{TaskID: result.TaskID, TargetID: result.TargetID, Success: result.Outcome == check.Success, ErrorClass: result.ErrorClass, CompletedAt: result.CompletedAt}
		if result.RTTMS != nil {
			legacy.LatencyMS = *result.RTTMS
		}
		if err = insertLatencyResult(ctx, tx, node, result.Kind, legacy); err != nil {
			if errors.Is(err, ErrNotFound) {
				return false, ErrNetworkTaskRejected
			}
			return false, err
		}
		outcome = result.Outcome
	}
	if _, err = tx.ExecContext(ctx, `UPDATE network_probe_tasks SET state='completed',received_at_ns=?,result_sha=?,result_json=?,result_format='network.v1',outcome=? WHERE id=?`, now.UnixNano(), fingerprint, string(raw), outcome, result.TaskID); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
