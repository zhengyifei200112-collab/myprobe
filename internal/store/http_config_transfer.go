package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
)

// Configuration transfer excludes revision history, task results and local Agent
// address allowances. The URL and assertions remain private administrator data.
type ConfigHTTPService struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	Enabled         bool           `json:"enabled"`
	IntervalSeconds int            `json:"interval_seconds"`
	Spec            httpcheck.Spec `json:"spec"`
	NodeIDs         []string       `json:"node_ids"`
}

func (v ConfigHTTPService) service() HTTPService {
	return HTTPService{ID: v.ID, Name: v.Name, Enabled: v.Enabled, IntervalSeconds: v.IntervalSeconds, Spec: v.Spec, NodeIDs: v.NodeIDs}
}

func (s *Store) exportHTTPServices(ctx context.Context) ([]ConfigHTTPService, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT s.id,s.name,s.enabled,s.interval_seconds,s.spec_json,(SELECT json_group_array(node_id) FROM (SELECT node_id FROM http_service_nodes WHERE service_id=s.id ORDER BY node_id)) FROM http_services s ORDER BY s.id LIMIT 1001`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ConfigHTTPService{}
	for rows.Next() {
		var v ConfigHTTPService
		var spec, nodes string
		if err = rows.Scan(&v.ID, &v.Name, &v.Enabled, &v.IntervalSeconds, &spec, &nodes); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(spec), &v.Spec); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(nodes), &v.NodeIDs); err != nil {
			return nil, err
		}
		items = append(items, v)
		if len(items) > 1000 {
			return nil, errors.New("too many HTTP services to export")
		}
	}
	return items, rows.Err()
}

func importHTTPService(ctx context.Context, tx *sql.Tx, v ConfigHTTPService, now string) (bool, error) {
	var revision int64
	err := tx.QueryRowContext(ctx, `SELECT revision FROM http_services WHERE id=?`, v.ID).Scan(&revision)
	created := errors.Is(err, sql.ErrNoRows)
	if err != nil && !created {
		return false, err
	}
	if revision >= 1<<63-1 {
		return false, ErrServiceConflict
	}
	revision++
	spec, err := json.Marshal(v.Spec)
	if err != nil {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO http_services(id,name,revision,enabled,interval_seconds,spec_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,revision=excluded.revision,enabled=excluded.enabled,interval_seconds=excluded.interval_seconds,spec_json=excluded.spec_json,updated_at=excluded.updated_at`, v.ID, v.Name, revision, v.Enabled, v.IntervalSeconds, string(spec), now, now)
	if err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM http_service_nodes WHERE service_id=?`, v.ID); err != nil {
		return false, err
	}
	for _, nodeID := range v.NodeIDs {
		if _, err = tx.ExecContext(ctx, `INSERT INTO http_service_nodes(service_id,node_id) VALUES(?,?)`, v.ID, nodeID); err != nil {
			return false, ErrInvalidHTTPService
		}
	}
	anchor, err := parseTime(now)
	if err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE http_schedule_epochs SET end_ns=? WHERE service_id=? AND end_ns IS NULL`, anchor.UnixNano(), v.ID); err != nil {
		return false, err
	}
	if v.Enabled {
		for _, nodeID := range v.NodeIDs {
			if _, err = tx.ExecContext(ctx, `INSERT INTO http_schedule_epochs(service_id,node_id,revision,start_ns,interval_seconds) VALUES(?,?,?,?,?)`, v.ID, nodeID, revision, anchor.UnixNano(), v.IntervalSeconds); err != nil {
				return false, err
			}
		}
	}
	return created, nil
}
