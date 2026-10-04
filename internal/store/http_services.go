package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
)

var ErrServiceConflict = errors.New("service configuration changed")

// HTTPService contains private configuration. It must never be returned by a
// public endpoint: the target URL can reveal deployment-specific information.
type HTTPService struct {
	ID              string         `json:"id"`
	Revision        int64          `json:"revision"`
	Name            string         `json:"name"`
	Enabled         bool           `json:"enabled"`
	IntervalSeconds int            `json:"interval_seconds"`
	Spec            httpcheck.Spec `json:"spec"`
	NodeIDs         []string       `json:"node_ids"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

func (v HTTPService) validate() error {
	if strings.TrimSpace(v.Name) == "" || len(v.Name) > 128 || !utf8.ValidString(v.Name) || strings.IndexFunc(v.Name, unicode.IsControl) >= 0 {
		return errors.New("invalid service name")
	}
	if v.IntervalSeconds < 30 || v.IntervalSeconds > 86400 || v.Spec.TimeoutMS >= v.IntervalSeconds*1000 {
		return errors.New("invalid service interval")
	}
	if err := v.Spec.Validate(); err != nil {
		return err
	}
	if len(v.NodeIDs) == 0 || len(v.NodeIDs) > 100 {
		return errors.New("service requires 1 to 100 nodes")
	}
	seen := make(map[string]bool)
	for _, id := range v.NodeIDs {
		if id == "" || len(id) > 128 || seen[id] {
			return errors.New("invalid service node selection")
		}
		seen[id] = true
	}
	return nil
}

// SaveHTTPService uses revision zero only for creation. Updates atomically
// replace assignments and advance revision, invalidating older scheduled tasks.
func (s *Store) SaveHTTPService(ctx context.Context, v HTTPService) (HTTPService, error) {
	if err := v.validate(); err != nil {
		return HTTPService{}, err
	}
	if (v.ID == "") != (v.Revision == 0) || v.Revision < 0 || v.Revision >= 1<<63-1 {
		return HTTPService{}, ErrServiceConflict
	}
	body, err := json.Marshal(v.Spec)
	if err != nil {
		return HTTPService{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return HTTPService{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	if v.ID == "" {
		v.ID, v.Revision, v.CreatedAt = randomID(), 1, now
		_, err = tx.ExecContext(ctx, `INSERT INTO http_services(id,name,revision,enabled,interval_seconds,spec_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, v.ID, v.Name, v.Revision, v.Enabled, v.IntervalSeconds, string(body), formatTime(now), formatTime(now))
	} else {
		var result sql.Result
		result, err = tx.ExecContext(ctx, `UPDATE http_services SET name=?,revision=revision+1,enabled=?,interval_seconds=?,spec_json=?,updated_at=? WHERE id=? AND revision=?`, v.Name, v.Enabled, v.IntervalSeconds, string(body), formatTime(now), v.ID, v.Revision)
		if err == nil {
			var count int64
			count, err = result.RowsAffected()
			if err == nil && count != 1 {
				return HTTPService{}, ErrServiceConflict
			}
		}
		v.Revision++
	}
	if err != nil {
		return HTTPService{}, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM http_service_nodes WHERE service_id=?`, v.ID); err != nil {
		return HTTPService{}, err
	}
	for _, nodeID := range v.NodeIDs {
		if _, err = tx.ExecContext(ctx, `INSERT INTO http_service_nodes(service_id,node_id) VALUES(?,?)`, v.ID, nodeID); err != nil {
			return HTTPService{}, err
		}
	}
	var created string
	if err = tx.QueryRowContext(ctx, `SELECT created_at FROM http_services WHERE id=?`, v.ID).Scan(&created); err != nil {
		return HTTPService{}, err
	}
	v.CreatedAt, err = parseTime(created)
	if err != nil {
		return HTTPService{}, err
	}
	if err = tx.Commit(); err != nil {
		return HTTPService{}, err
	}
	v.UpdatedAt = now
	v.NodeIDs = append([]string(nil), v.NodeIDs...)
	sort.Strings(v.NodeIDs)
	return v, nil
}

func (s *Store) HTTPService(ctx context.Context, id string) (HTTPService, error) {
	// A read transaction keeps configuration and assignment revisions consistent.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return HTTPService{}, err
	}
	defer tx.Rollback()
	var v HTTPService
	var body, created, updated string
	err = tx.QueryRowContext(ctx, `SELECT id,name,revision,enabled,interval_seconds,spec_json,created_at,updated_at FROM http_services WHERE id=?`, id).Scan(&v.ID, &v.Name, &v.Revision, &v.Enabled, &v.IntervalSeconds, &body, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return HTTPService{}, ErrNotFound
	}
	if err != nil {
		return HTTPService{}, err
	}
	if err = json.Unmarshal([]byte(body), &v.Spec); err != nil {
		return HTTPService{}, err
	}
	v.CreatedAt, err = parseTime(created)
	if err != nil {
		return HTTPService{}, err
	}
	v.UpdatedAt, err = parseTime(updated)
	if err != nil {
		return HTTPService{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT node_id FROM http_service_nodes WHERE service_id=? ORDER BY node_id`, id)
	if err != nil {
		return HTTPService{}, err
	}
	defer rows.Close()
	v.NodeIDs = []string{}
	for rows.Next() {
		var nodeID string
		if err = rows.Scan(&nodeID); err != nil {
			return HTTPService{}, err
		}
		v.NodeIDs = append(v.NodeIDs, nodeID)
	}
	if err = rows.Err(); err != nil {
		return HTTPService{}, err
	}
	return v, nil
}
