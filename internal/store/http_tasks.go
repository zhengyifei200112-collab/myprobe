package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
)

var ErrHTTPTaskRejected = errors.New("HTTP task or result rejected")

// CreateHTTPTask snapshots server-owned configuration before dispatch. The
// conditional insert rechecks revision and assignment against concurrent edits.
func (s *Store) CreateHTTPTask(ctx context.Context, serviceID, nodeID string, scheduledAt, now time.Time) (httpcheck.Task, error) {
	v, err := s.HTTPService(ctx, serviceID)
	if err != nil {
		return httpcheck.Task{}, err
	}
	if scheduledAt.Before(v.UpdatedAt) {
		return httpcheck.Task{}, ErrHTTPTaskRejected
	}
	task := httpcheck.Task{ID: randomID(), ServiceID: v.ID, Revision: uint64(v.Revision), ScheduledAt: scheduledAt.UTC(), ExpiresAt: now.UTC().Add(time.Duration(v.Spec.TimeoutMS) * time.Millisecond), Spec: v.Spec}
	if err = task.Validate(now); err != nil {
		return httpcheck.Task{}, err
	}
	body, err := json.Marshal(task)
	if err != nil {
		return httpcheck.Task{}, err
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO http_tasks(id,service_id,node_id,revision,scheduled_at,expires_at,task_json)
		SELECT ?,s.id,?,s.revision,?,?,? FROM http_services s
		WHERE s.id=? AND s.revision=? AND s.enabled=1
		AND EXISTS(SELECT 1 FROM http_service_nodes a WHERE a.service_id=s.id AND a.node_id=?)
		ON CONFLICT(service_id,node_id,revision,scheduled_at) DO NOTHING`, task.ID, nodeID, formatTime(task.ScheduledAt), formatTime(task.ExpiresAt), string(body), v.ID, v.Revision, nodeID)
	if err != nil {
		return httpcheck.Task{}, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return httpcheck.Task{}, err
	}
	if count != 1 {
		return httpcheck.Task{}, ErrHTTPTaskRejected
	}
	return task, nil
}

// SaveHTTPResult takes the node identity from the authenticated transport, never
// from the payload. A single conditional write admits only the first valid result
// while checking that the service revision and assignment are still current.
func (s *Store) SaveHTTPResult(ctx context.Context, nodeID string, value httpcheck.Result, now time.Time) error {
	var body string
	err := s.db.QueryRowContext(ctx, `SELECT task_json FROM http_tasks WHERE id=? AND node_id=?`, value.TaskID, nodeID).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrHTTPTaskRejected
	}
	if err != nil {
		return err
	}
	var task httpcheck.Task
	if err = json.Unmarshal([]byte(body), &task); err != nil {
		return err
	}
	if err = value.ValidateFor(task, now); err != nil {
		return ErrHTTPTaskRejected
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE http_tasks SET result_json=?,received_at=?
		WHERE id=? AND node_id=? AND result_json IS NULL
		AND EXISTS(SELECT 1 FROM http_services s JOIN http_service_nodes a ON a.service_id=s.id
		WHERE s.id=http_tasks.service_id AND s.revision=http_tasks.revision AND s.enabled=1 AND a.node_id=http_tasks.node_id)`, string(encoded), formatTime(now), task.ID, nodeID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrHTTPTaskRejected
	}
	return nil
}
