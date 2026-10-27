package store

import (
	"context"
	"errors"
	"time"
)

type HTTPServiceSummary struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Revision        int64     `json:"revision"`
	Enabled         bool      `json:"enabled"`
	IntervalSeconds int       `json:"interval_seconds"`
	NodeCount       int       `json:"node_count"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// ListHTTPServices is a bounded keyset page. Summaries omit private target URLs.
func (s *Store) ListHTTPServices(ctx context.Context, after string, limit int) ([]HTTPServiceSummary, string, error) {
	if limit < 1 || limit > 100 || len(after) > 128 {
		return nil, "", ErrInvalidHTTPService
	}
	rows, err := s.db.QueryContext(ctx, `SELECT s.id,s.name,s.revision,s.enabled,s.interval_seconds,s.updated_at,(SELECT COUNT(*) FROM http_service_nodes a WHERE a.service_id=s.id) FROM http_services s WHERE s.id>? ORDER BY s.id LIMIT ?`, after, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := []HTTPServiceSummary{}
	for rows.Next() {
		var item HTTPServiceSummary
		var updated string
		if err = rows.Scan(&item.ID, &item.Name, &item.Revision, &item.Enabled, &item.IntervalSeconds, &updated, &item.NodeCount); err != nil {
			return nil, "", err
		}
		if item.UpdatedAt, err = parseTime(updated); err != nil {
			return nil, "", err
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		next = items[len(items)-1].ID
	}
	return items, next, nil
}

func (s *Store) DeleteHTTPService(ctx context.Context, id string, revision int64) error {
	if id == "" || revision <= 0 {
		return ErrInvalidHTTPService
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM http_services WHERE id=? AND revision=?`, id, revision)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrServiceConflict
	}
	if count != 1 {
		return errors.New("unexpected service deletion count")
	}
	return nil
}
