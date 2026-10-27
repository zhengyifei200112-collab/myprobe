package store

import (
	"context"
	"time"
)

// HTTPAssignment contains scheduling metadata only, never a target URL.
type HTTPAssignment struct {
	ServiceID       string
	NodeID          string
	Revision        int64
	IntervalSeconds int
	Anchor          time.Time
}

func (s *Store) ListHTTPAssignments(ctx context.Context) ([]HTTPAssignment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT s.id,a.node_id,s.revision,s.interval_seconds,s.updated_at
		FROM http_services s JOIN http_service_nodes a ON a.service_id=s.id
		WHERE s.enabled=1 ORDER BY s.id,a.node_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []HTTPAssignment{}
	for rows.Next() {
		var item HTTPAssignment
		var anchor string
		if err = rows.Scan(&item.ServiceID, &item.NodeID, &item.Revision, &item.IntervalSeconds, &anchor); err != nil {
			return nil, err
		}
		if item.Anchor, err = parseTime(anchor); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
