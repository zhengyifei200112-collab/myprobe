package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

var (
	ErrNetworkTaskRejected   = errors.New("network task rejected")
	ErrNetworkTaskDispatched = errors.New("network task already dispatched")
)

type NetworkAssignment struct {
	EpochID, NodeID, TargetID string
	ScheduledAt               time.Time
	Eligible                  bool
}

type NetworkTask struct {
	Task                        protocol.Task
	NodeID, EpochID, LeaseToken string
	ScheduledAt, DispatchUntil  time.Time
}

// A crashed sender may have written the task before it disappeared. Expired
// leases therefore become unknown, not ready-to-resend. Bound each cleanup.
func (s *Store) ExpireNetworkDispatchLeases(ctx context.Context, now time.Time) error {
	var found int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM network_probe_tasks WHERE state='sending' AND lease_until_ns<=? LIMIT 1`, now.UnixNano()).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE network_probe_tasks SET state='sent_unknown',dispatch_reason='dispatch_unknown'
 WHERE id IN (SELECT id FROM network_probe_tasks WHERE state='sending' AND lease_until_ns<=? ORDER BY lease_until_ns,id LIMIT 1000)`, now.UnixNano())
	return err
}

// Enumerate active plans, including not-yet-due ones for diagnostics. A missing
// row means a missed slot, not a backlog to replay after the dispatch window.
func (s *Store) ListNetworkAssignments(ctx context.Context, now time.Time) ([]NetworkAssignment, error) {
	rows, err := s.db.QueryContext(ctx, `WITH slots AS (
 SELECT e.id,e.node_id,e.target_id,e.starts_at+((?-e.starts_at)/e.interval_seconds)*e.interval_seconds AS slot,e.interval_seconds
 FROM network_schedule_epochs e WHERE e.ends_at IS NULL AND e.starts_at<=?
 ) SELECT s.id,s.node_id,s.target_id,s.slot,
 (? < (s.slot+min(5,s.interval_seconds))*1000000000 AND
 (t.id IS NULL OR (t.state='ready' AND t.retry_after_ns<=?)))
 FROM slots s LEFT JOIN network_probe_tasks t ON t.epoch_id=s.id AND t.scheduled_at=s.slot
 ORDER BY s.node_id,s.target_id`, now.Unix(), now.Unix(), now.UnixNano(), now.UnixNano())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]NetworkAssignment, 0)
	for rows.Next() {
		var a NetworkAssignment
		var slot int64
		var eligible int
		if err := rows.Scan(&a.EpochID, &a.NodeID, &a.TargetID, &slot, &eligible); err != nil {
			return nil, err
		}
		a.ScheduledAt = time.Unix(slot, 0).UTC()
		a.Eligible = eligible != 0
		result = append(result, a)
	}
	return result, rows.Err()
}

// Acquire the SQLite writer before checking identity/state. This serializes
// competing claims/results without holding a Gateway mutex over database IO.
func (s *Store) networkWrite(ctx context.Context) (*sql.Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE network_schedule_metadata SET known_since=known_since WHERE singleton=1`); err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func (s *Store) ClaimNetworkTask(ctx context.Context, epochID string, slot, now time.Time) (NetworkTask, error) {
	tx, err := s.networkWrite(ctx)
	if err != nil {
		return NetworkTask{}, err
	}
	defer tx.Rollback()
	var node, target, kind, host string
	var port sql.NullInt64
	var start int64
	var interval, timeout int
	err = tx.QueryRowContext(ctx, `SELECT node_id,target_id,kind,host,port,starts_at,interval_seconds,timeout_ms FROM network_schedule_epochs WHERE id=? AND ends_at IS NULL`, epochID).Scan(&node, &target, &kind, &host, &port, &start, &interval, &timeout)
	if errors.Is(err, sql.ErrNoRows) {
		return NetworkTask{}, ErrNetworkTaskRejected
	}
	if err != nil {
		return NetworkTask{}, err
	}
	if slot.Nanosecond() != 0 || slot.Unix() < start || (slot.Unix()-start)%int64(interval) != 0 {
		return NetworkTask{}, ErrNetworkTaskRejected
	}
	dispatchUntil := slot.Add(time.Duration(min(5, interval)) * time.Second)
	if now.Before(slot) || !now.Before(dispatchUntil) {
		return NetworkTask{}, ErrNetworkTaskRejected
	}
	result := NetworkTask{NodeID: node, EpochID: epochID, ScheduledAt: slot.UTC(), DispatchUntil: dispatchUntil, LeaseToken: randomID(), Task: protocol.Task{ID: randomID(), TargetID: target, Kind: kind, Host: host, Port: int(port.Int64), TimeoutMS: timeout, ExpiresAt: slot.Add(time.Duration(timeout)*time.Millisecond + 15*time.Second)}}
	var state string
	var retry int64
	err = tx.QueryRowContext(ctx, `SELECT id,state,retry_after_ns FROM network_probe_tasks WHERE epoch_id=? AND scheduled_at=?`, epochID, slot.Unix()).Scan(&result.Task.ID, &state, &retry)
	if err == nil {
		if state != "ready" || retry > now.UnixNano() {
			return NetworkTask{}, ErrNetworkTaskDispatched
		}
		_, err = tx.ExecContext(ctx, `UPDATE network_probe_tasks SET state='sending',lease_token=?,lease_until_ns=? WHERE id=?`, result.LeaseToken, dispatchUntil.UnixNano(), result.Task.ID)
	} else if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, `INSERT INTO network_probe_tasks(id,source,epoch_id,node_id,target_id,scheduled_at,kind,host,port,timeout_ms,dispatch_until_ns,expires_at_ns,created_at_ns,state,lease_token,lease_until_ns)
 VALUES(?,'scheduled',?,?,?,?,?,?,?,?,?,?,?,'sending',?,?)`, result.Task.ID, epochID, node, target, slot.Unix(), kind, host, port, timeout, dispatchUntil.UnixNano(), result.Task.ExpiresAt.UnixNano(), now.UnixNano(), result.LeaseToken, dispatchUntil.UnixNano())
	}
	if err != nil {
		return NetworkTask{}, err
	}
	if err = tx.Commit(); err != nil {
		return NetworkTask{}, err
	}
	return result, nil
}

// Direct internal diagnostic tasks have no planned slot and never enter the
// coverage denominator. They use the same durable result/ACK transaction.
func (s *Store) RecordManualNetworkTask(ctx context.Context, node string, task protocol.Task, now time.Time) (NetworkTask, error) {
	if task.Validate(now) != nil {
		return NetworkTask{}, ErrNetworkTaskRejected
	}
	tx, err := s.networkWrite(ctx)
	if err != nil {
		return NetworkTask{}, err
	}
	defer tx.Rollback()
	var assigned int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM node_targets nt JOIN targets t ON t.id=nt.target_id WHERE nt.node_id=? AND t.id=? AND t.kind=?`, node, task.TargetID, task.Kind).Scan(&assigned); err != nil {
		return NetworkTask{}, err
	}
	if assigned != 1 {
		return NetworkTask{}, ErrNetworkTaskRejected
	}
	var exists int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM network_probe_tasks WHERE id=?`, task.ID).Scan(&exists); err != nil {
		return NetworkTask{}, err
	}
	if exists != 0 {
		return NetworkTask{}, ErrNetworkTaskDispatched
	}
	result := NetworkTask{NodeID: node, Task: task, LeaseToken: randomID(), DispatchUntil: task.ExpiresAt}
	_, err = tx.ExecContext(ctx, `INSERT INTO network_probe_tasks(id,source,node_id,target_id,kind,host,port,timeout_ms,dispatch_until_ns,expires_at_ns,created_at_ns,state,lease_token,lease_until_ns)
 VALUES(?,'manual',?,?,?,?,?,?,?,?,?,'sending',?,?)`, task.ID, node, task.TargetID, task.Kind, task.Host, sqlPort(task.Port), task.TimeoutMS, task.ExpiresAt.UnixNano(), task.ExpiresAt.UnixNano(), now.UnixNano(), result.LeaseToken, task.ExpiresAt.UnixNano())
	if err != nil {
		return NetworkTask{}, err
	}
	if err = tx.Commit(); err != nil {
		return NetworkTask{}, err
	}
	return result, nil
}

func sqlPort(port int) any {
	if port == 0 {
		return nil
	}
	return port
}

// Only a confirmed absence of a session can retry. A write failure may have
// delivered the task, so it remains unknown and must never cause a second send.
func (s *Store) FinishNetworkDispatch(ctx context.Context, task NetworkTask, reason string, now time.Time) error {
	state := "sent"
	retry := int64(0)
	var sent any = now.UnixNano()
	switch reason {
	case "":
	case "offline":
		state = "ready"
		retry = now.Add(time.Second).UnixNano()
		sent = nil
	case "write_error":
		state = "sent_unknown"
		sent = nil
	default:
		return ErrNetworkTaskRejected
	}
	stored, err := s.db.ExecContext(ctx, `UPDATE network_probe_tasks SET state=?,dispatch_reason=?,retry_after_ns=?,sent_at_ns=? WHERE id=? AND lease_token=? AND state IN ('sending','sent_unknown')`, state, reason, retry, sent, task.Task.ID, task.LeaseToken)
	if err != nil {
		return err
	}
	count, err := stored.RowsAffected()
	if err != nil {
		return err
	}
	if count != 0 {
		return nil
	}
	var current string
	if err = s.db.QueryRowContext(ctx, `SELECT state FROM network_probe_tasks WHERE id=?`, task.Task.ID).Scan(&current); err != nil {
		return err
	}
	if current == "completed" {
		return nil
	}
	return ErrNetworkTaskDispatched
}

// Return true only for a newly committed result. An identical replay can be
// ACKed after a lost response, without inserting or publishing another sample.
func (s *Store) AcceptNetworkResult(ctx context.Context, node, kind string, result protocol.LatencyResult, now time.Time) (bool, error) {
	if (kind != protocol.TaskKindPing && kind != protocol.TaskKindTCPing) || result.Validate(now) != nil {
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
	var boundNode, target, boundKind, state string
	var epoch, previous sql.NullString
	var expiry int64
	err = tx.QueryRowContext(ctx, `SELECT node_id,target_id,kind,state,epoch_id,expires_at_ns,result_sha FROM network_probe_tasks WHERE id=?`, result.TaskID).Scan(&boundNode, &target, &boundKind, &state, &epoch, &expiry, &previous)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNetworkTaskRejected
	}
	if err != nil {
		return false, err
	}
	if boundNode != node || target != result.TargetID || boundKind != kind {
		return false, ErrNetworkTaskRejected
	}
	if state == "completed" {
		if previous.String != fingerprint {
			return false, ErrNetworkTaskRejected
		}
		return false, tx.Commit()
	}
	if (state != "sending" && state != "sent" && state != "sent_unknown") || now.UnixNano() > expiry {
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
	if err = insertLatencyResult(ctx, tx, node, kind, result); err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, ErrNetworkTaskRejected
		}
		return false, err
	}
	outcome := "failure"
	if result.Success {
		outcome = "success"
	} else if result.ErrorClass == "busy" || result.ErrorClass == "unsupported" || result.ErrorClass == "invalid_task" {
		outcome = "not_executed"
	}
	_, err = tx.ExecContext(ctx, `UPDATE network_probe_tasks SET state='completed',received_at_ns=?,result_sha=?,result_json=?,outcome=? WHERE id=?`, now.UnixNano(), fingerprint, string(raw), outcome, result.TaskID)
	if err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
