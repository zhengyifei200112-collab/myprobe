package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
)

// HTTPObservation never includes the private task specification.
type HTTPObservation struct {
	TaskID      string            `json:"task_id"`
	NodeID      string            `json:"node_id"`
	Revision    int64             `json:"revision"`
	ScheduledAt time.Time         `json:"scheduled_at"`
	ExpiresAt   time.Time         `json:"expires_at"`
	Result      *httpcheck.Result `json:"result"`
}

func (s *Store) RecentHTTPResults(ctx context.Context, serviceID string, limit int) ([]HTTPObservation, error) {
	if limit < 1 || limit > 100 {
		return nil, ErrInvalidHTTPService
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var exists int
	if err = tx.QueryRowContext(ctx, `SELECT 1 FROM http_services WHERE id=?`, serviceID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	var floor int64
	if err = tx.QueryRowContext(ctx, `SELECT retained_from_ns FROM http_history_state WHERE id=1`).Scan(&floor); err != nil {
		return nil, err
	}
	const timeKey = `substr(substr(scheduled_at,1,19)||'.'||rtrim(substr(scheduled_at,21),'Z')||'000000000',1,29)`
	rows, err := tx.QueryContext(ctx, `SELECT id,node_id,revision,scheduled_at,expires_at,result_json FROM http_tasks WHERE service_id=? AND `+timeKey+`>=? ORDER BY `+timeKey+` DESC,id DESC LIMIT ?`, serviceID, time.Unix(0, floor).UTC().Format("2006-01-02T15:04:05.000000000"), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []HTTPObservation{}
	for rows.Next() {
		var v HTTPObservation
		var scheduled, expires string
		var body sql.NullString
		if err = rows.Scan(&v.TaskID, &v.NodeID, &v.Revision, &scheduled, &expires, &body); err != nil {
			return nil, err
		}
		if v.ScheduledAt, err = parseTime(scheduled); err != nil {
			return nil, err
		}
		if v.ExpiresAt, err = parseTime(expires); err != nil {
			return nil, err
		}
		if body.Valid {
			v.Result = &httpcheck.Result{}
			if err = json.Unmarshal([]byte(body.String), v.Result); err != nil {
				return nil, err
			}
		}
		items = append(items, v)
	}
	return items, rows.Err()
}
