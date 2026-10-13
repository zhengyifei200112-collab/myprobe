package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
)

type HTTPObserverSnapshot struct {
	NodeID        string
	NodeName      string
	ReportSeconds int
	LastSeenAt    *time.Time
	Capabilities  []string
	Observation   *HTTPObservation
}

type HTTPHealthSnapshot struct {
	Service   HTTPServiceSummary
	Observers []HTTPObserverSnapshot
}

// HTTPHealthSnapshots reads bounded service definitions, current assignments and
// the latest planned observation in one SQLite statement/snapshot. It never reads
// target specifications or network addresses.
func (s *Store) HTTPHealthSnapshots(ctx context.Context, ids []string, now time.Time) ([]HTTPHealthSnapshot, error) {
	if len(ids) > 100 || now.IsZero() {
		return nil, ErrInvalidHTTPService
	}
	if len(ids) == 0 {
		return []HTTPHealthSnapshot{}, nil
	}
	args := make([]any, 0, len(ids)+1)
	args = append(args, now.UTC().Format("2006-01-02T15:04:05.000000000"))
	unique := map[string]bool{}
	for _, id := range ids {
		if id == "" || len(id) > 128 || unique[id] {
			return nil, ErrInvalidHTTPService
		}
		unique[id] = true
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx, httpHealthQuery(len(ids)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := map[string]*HTTPHealthSnapshot{}
	rowCount := 0
	for rows.Next() {
		rowCount++
		if rowCount > 10000 {
			return nil, errors.New("service health observer limit exceeded")
		}
		var service HTTPServiceSummary
		var updated string
		var nodeID, nodeName, lastSeen, capabilities, taskID, scheduled, expires, raw sql.NullString
		var reportSeconds sql.NullInt64
		if err = rows.Scan(&service.ID, &service.Name, &service.Revision, &service.Enabled, &service.IntervalSeconds, &updated, &nodeID, &nodeName, &reportSeconds, &lastSeen, &capabilities, &taskID, &scheduled, &expires, &raw); err != nil {
			return nil, err
		}
		if service.UpdatedAt, err = parseTime(updated); err != nil {
			return nil, err
		}
		snapshot := byID[service.ID]
		if snapshot == nil {
			snapshot = &HTTPHealthSnapshot{Service: service, Observers: []HTTPObserverSnapshot{}}
			byID[service.ID] = snapshot
		}
		if !nodeID.Valid {
			continue
		}
		if len(snapshot.Observers) >= 100 {
			return nil, errors.New("service health observer limit exceeded")
		}
		observer := HTTPObserverSnapshot{NodeID: nodeID.String, NodeName: nodeName.String, ReportSeconds: int(reportSeconds.Int64)}
		if lastSeen.Valid {
			seen, parseErr := parseTime(lastSeen.String)
			if parseErr != nil {
				return nil, parseErr
			}
			observer.LastSeenAt = &seen
		}
		if capabilities.Valid && json.Unmarshal([]byte(capabilities.String), &observer.Capabilities) != nil {
			return nil, errors.New("invalid observer metadata")
		}
		if taskID.Valid {
			observation := &HTTPObservation{TaskID: taskID.String, NodeID: nodeID.String, Revision: service.Revision}
			if observation.ScheduledAt, err = parseTime(scheduled.String); err != nil {
				return nil, err
			}
			if observation.ExpiresAt, err = parseTime(expires.String); err != nil {
				return nil, err
			}
			if raw.Valid {
				observation.Result = &httpcheck.Result{}
				if err = json.Unmarshal([]byte(raw.String), observation.Result); err != nil {
					return nil, err
				}
			}
			observer.Observation = observation
		}
		snapshot.Observers = append(snapshot.Observers, observer)
		snapshot.Service.NodeCount++
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	snapshots := make([]HTTPHealthSnapshot, 0, len(ids))
	for _, id := range ids {
		if snapshot := byID[id]; snapshot != nil {
			snapshots = append(snapshots, *snapshot)
		}
	}
	return snapshots, nil
}

func httpHealthQuery(serviceCount int) string {
	const timeKey = `substr(substr(scheduled_at,1,19)||'.'||rtrim(substr(scheduled_at,21),'Z')||'000000000',1,29)`
	return `SELECT s.id,s.name,s.revision,s.enabled,s.interval_seconds,s.updated_at,n.id,n.name,n.report_seconds,n.last_seen_at,m.capabilities_json,t.id,t.scheduled_at,t.expires_at,t.result_json
 FROM http_services s LEFT JOIN http_service_nodes a ON a.service_id=s.id
 LEFT JOIN nodes n ON n.id=a.node_id LEFT JOIN node_agent_metadata m ON m.node_id=n.id
 LEFT JOIN http_tasks t ON t.id=(SELECT id FROM http_tasks WHERE service_id=s.id AND node_id=n.id AND revision=s.revision AND ` + timeKey + `<=? ORDER BY ` + timeKey + ` DESC,id DESC LIMIT 1)
 WHERE s.id IN (` + strings.TrimSuffix(strings.Repeat("?,", serviceCount), ",") + `) ORDER BY s.id,n.id LIMIT 10001`
}
