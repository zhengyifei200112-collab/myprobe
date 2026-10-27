package store

import (
	"container/heap"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"
)

const timelineObservationBudget = 100000

var ErrTimelineCursor = errors.New("invalid timeline pagination or window")

// NodeEvent is an allowlist projection, never an incident/audit/report payload.
// Public readers only receive derived report observations and uptime hints.
type NodeEvent struct {
	ID          string    `json:"id"`
	Time        time.Time `json:"time"`
	Kind        string    `json:"kind"`
	IncidentID  string    `json:"incident_id,omitempty"`
	Approximate bool      `json:"approximate"`
}

type NodeTimeline struct {
	Events                []NodeEvent `json:"events"`
	Start                 time.Time   `json:"start"`
	End                   time.Time   `json:"end"`
	NextCursor            string      `json:"next_cursor,omitempty"`
	ObservationStart      *time.Time  `json:"observation_start,omitempty"`
	ObservationsTruncated bool        `json:"observations_truncated"`
	GapThresholdSeconds   int         `json:"gap_threshold_seconds"`
}

type timelineCursor struct {
	Node, Start, End, At, ID string
	Admin                    bool
}

func eventBefore(a, b NodeEvent) bool {
	return a.Time.Before(b.Time) || a.Time.Equal(b.Time) && a.ID < b.ID
}

type eventHeap []NodeEvent

func (h eventHeap) Len() int           { return len(h) }
func (h eventHeap) Less(i, j int) bool { return eventBefore(h[i], h[j]) }
func (h eventHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x any)        { *h = append(*h, x.(NodeEvent)) }
func (h *eventHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}

// NodeTimelineRange uses one read transaction for visibility and every source.
// Cursors bind the node, scope and fixed [start,end) window. They are navigation
// state, not authorization; current visibility/session checks still apply.
func (s *Store) NodeTimelineRange(ctx context.Context, nodeID string, start, end time.Time, admin bool, cursor string, limit int) (NodeTimeline, error) {
	start, end = start.UTC(), end.UTC()
	p := NodeTimeline{Events: []NodeEvent{}, Start: start, End: end}
	if nodeID == "" || len(nodeID) > 128 || !start.Before(end) || start.Year() < 1970 || end.Year() > 9999 || end.Sub(start) > 365*24*time.Hour || limit < 1 || limit > 100 {
		return p, ErrTimelineCursor
	}
	var before *NodeEvent
	if cursor != "" {
		if len(cursor) > 2048 {
			return p, ErrTimelineCursor
		}
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		var c timelineCursor
		if err != nil || json.Unmarshal(raw, &c) != nil || c.Node != nodeID || c.Admin != admin || c.Start != formatCapturedTime(start) || c.End != formatCapturedTime(end) || len(c.ID) < 1 || len(c.ID) > 256 {
			return p, ErrTimelineCursor
		}
		at, err := time.Parse(time.RFC3339Nano, c.At)
		if err != nil || at.Before(start) || !at.Before(end) {
			return p, ErrTimelineCursor
		}
		before = &NodeEvent{Time: at, ID: c.ID}
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return p, err
	}
	defer tx.Rollback()
	var hidden bool
	var collection, report int
	err = tx.QueryRowContext(ctx, `SELECT hidden,collection_seconds,report_seconds FROM nodes WHERE id=?`, nodeID).Scan(&hidden, &collection, &report)
	if errors.Is(err, sql.ErrNoRows) || err == nil && hidden && !admin {
		return p, ErrNotFound
	}
	if err != nil {
		return p, err
	}
	// Older configuration intervals are not persisted with each report. Label
	// gaps approximate and use both current intervals to avoid treating normal
	// slow collection or an Agent reconnect as host downtime/reboot.
	p.GapThresholdSeconds = max(20, 3*max(collection, report))
	items := &eventHeap{}
	add := func(e NodeEvent) {
		if e.Time.Before(start) || !e.Time.Before(end) || before != nil && !eventBefore(e, *before) {
			return
		}
		heap.Push(items, e)
		if items.Len() > limit+1 {
			heap.Pop(items)
		}
	}
	if err := timelineObservations(ctx, tx, nodeID, &p, add); err != nil {
		return p, err
	}
	if admin {
		at, id := formatCapturedTime(end), ""
		if before != nil {
			at, id = formatCapturedTime(before.Time), before.ID
		}
		rows, err := tx.QueryContext(ctx, timelinePrivateSQL, sql.Named("node", nodeID), sql.Named("start", formatCapturedTime(start)), sql.Named("end", formatCapturedTime(end)), sql.Named("before", at), sql.Named("id", id), sql.Named("limit", limit+1))
		if err != nil {
			return p, err
		}
		for rows.Next() {
			var e NodeEvent
			var at string
			if err = rows.Scan(&e.ID, &at, &e.Kind, &e.IncidentID, &e.Approximate); err != nil {
				break
			}
			e.Time, err = time.Parse(time.RFC3339Nano, at)
			if err != nil {
				break
			}
			add(e)
		}
		rowErr := rows.Err()
		rows.Close()
		if err != nil {
			return p, err
		}
		if rowErr != nil {
			return p, rowErr
		}
	}
	p.Events = append(p.Events, (*items)...)
	sort.Slice(p.Events, func(i, j int) bool { return eventBefore(p.Events[j], p.Events[i]) })
	if len(p.Events) > limit {
		p.Events = p.Events[:limit]
		last := p.Events[len(p.Events)-1]
		raw, _ := json.Marshal(timelineCursor{Node: nodeID, Admin: admin, Start: formatCapturedTime(start), End: formatCapturedTime(end), At: formatCapturedTime(last.Time), ID: last.ID})
		p.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return p, tx.Commit()
}

type timelineSample struct {
	at     time.Time
	uptime uint64
}

func timelineObservations(ctx context.Context, tx *sql.Tx, nodeID string, p *NodeTimeline, add func(NodeEvent)) error {
	// Bound work even when raw retention is behind. Fetch one extra predecessor
	// so truncation cannot invent an initial observation, a gap or reboot hint.
	rows, err := tx.QueryContext(ctx, `SELECT captured_sort_ns,CAST(json_extract(report_json,'$.uptime_seconds') AS TEXT) FROM metric_samples WHERE node_id=? AND captured_sort_ns>=? AND captured_sort_ns<? ORDER BY captured_sort_ns DESC,id DESC LIMIT ?`, nodeID, formatCapturedTime(p.Start), formatCapturedTime(p.End), timelineObservationBudget+1)
	if err != nil {
		return err
	}
	points := make([]timelineSample, 0)
	rawCount := 0
	for rows.Next() {
		rawCount++
		var stamp string
		var uptime sql.NullString
		if err = rows.Scan(&stamp, &uptime); err != nil {
			break
		}
		at, parseErr := time.Parse(time.RFC3339Nano, stamp)
		if parseErr != nil {
			err = parseErr
			break
		}
		u, _ := strconv.ParseUint(uptime.String, 10, 64)
		if len(points) > 0 && points[len(points)-1].at.Equal(at) {
			continue // Keep the highest legacy row id for this exact capture.
		}
		points = append(points, timelineSample{at, u})
	}
	rowErr := rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if rowErr != nil {
		return rowErr
	}
	var previous *timelineSample
	if rawCount > timelineObservationBudget && len(points) > 0 {
		p.ObservationsTruncated = true
		previous = &points[len(points)-1]
		points = points[:len(points)-1]
	} else {
		var stamp string
		var uptime sql.NullString
		err = tx.QueryRowContext(ctx, `SELECT captured_sort_ns,CAST(json_extract(report_json,'$.uptime_seconds') AS TEXT) FROM metric_samples WHERE node_id=? AND captured_sort_ns<? ORDER BY captured_sort_ns DESC,id DESC LIMIT 1`, nodeID, formatCapturedTime(p.Start)).Scan(&stamp, &uptime)
		if err == nil {
			at, parseErr := time.Parse(time.RFC3339Nano, stamp)
			if parseErr != nil {
				return parseErr
			}
			u, _ := strconv.ParseUint(uptime.String, 10, 64)
			previous = &timelineSample{at, u}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	gap := time.Duration(p.GapThresholdSeconds) * time.Second
	emit := func(kind string, at time.Time) {
		add(NodeEvent{ID: fmt.Sprintf("report:%s:%s", kind, formatCapturedTime(at)), Time: at, Kind: kind, Approximate: true})
	}
	for i := len(points) - 1; i >= 0; i-- {
		point := &points[i]
		if previous != nil && previous.at.Equal(point.at) {
			continue // Legacy duplicate captures are one observation, not reboot evidence.
		}
		if p.ObservationStart == nil {
			stamp := point.at
			p.ObservationStart = &stamp
		}
		if previous == nil {
			emit("report_observed", point.at)
		} else {
			if point.at.Sub(previous.at) > gap {
				emit("report_gap", previous.at.Add(gap))
				emit("report_resumed", point.at)
			}
			if previous.uptime > 0 && point.uptime > 0 && point.uptime < previous.uptime {
				emit("reboot_hint", point.at)
			}
		}
		previous = point
	}
	if previous != nil && p.End.Sub(previous.at) > gap {
		emit("report_gap", previous.at.Add(gap))
	}
	return nil
}

// All source payloads and user-provided text remain private to their existing
// dedicated APIs. Even the administrator timeline returns only identifiers,
// fixed kinds and evidence flags; an incident link loads its own auth boundary.
const timelinePrivateSQL = `WITH events(id,at,kind,incident_id,approximate) AS (
 SELECT 'incident:'||id||':observed',started_sort_ns,'incident_observed',id,approximate_start FROM alert_incidents WHERE node_id=:node
 UNION ALL SELECT 'incident:'||id||':fired',fired_sort_ns,'incident_fired',id,approximate_start FROM alert_incidents WHERE node_id=:node AND fired_at IS NOT NULL
 UNION ALL SELECT 'incident:'||id||':closed',resolved_sort_ns,CASE WHEN resolution_reason='recovered' THEN 'incident_recovered' ELSE 'incident_closed' END,id,approximate_start FROM alert_incidents WHERE node_id=:node AND resolved_at IS NOT NULL
 UNION ALL SELECT 'maintenance:'||w.id||':start',strftime('%Y-%m-%dT%H:%M:%S',w.starts_at_ms/1000,'unixepoch')||printf('.%03d000000Z',w.starts_at_ms%1000),'maintenance_started','',0 FROM maintenance_windows w JOIN maintenance_window_nodes n ON n.window_id=w.id WHERE n.node_id=:node AND (w.canceled_at_ms IS NULL OR w.canceled_at_ms>w.starts_at_ms)
 UNION ALL SELECT 'maintenance:'||w.id||':end',strftime('%Y-%m-%dT%H:%M:%S',w.ends_at_ms/1000,'unixepoch')||printf('.%03d000000Z',w.ends_at_ms%1000),'maintenance_ended','',0 FROM maintenance_windows w JOIN maintenance_window_nodes n ON n.window_id=w.id WHERE n.node_id=:node AND (w.canceled_at_ms IS NULL OR w.canceled_at_ms>=w.ends_at_ms)
 UNION ALL SELECT 'maintenance:'||w.id||':cancel',strftime('%Y-%m-%dT%H:%M:%S',w.canceled_at_ms/1000,'unixepoch')||printf('.%03d000000Z',w.canceled_at_ms%1000),'maintenance_canceled','',0 FROM maintenance_windows w JOIN maintenance_window_nodes n ON n.window_id=w.id WHERE n.node_id=:node AND w.canceled_at_ms IS NOT NULL
 UNION ALL SELECT 'audit:'||id,created_sort_ns,'configuration_saved','',0 FROM audit_log WHERE object_type='node' AND object_id=:node
 UNION ALL SELECT 'audit:'||id,created_sort_ns,'configuration_saved','',0 FROM audit_log WHERE object_type='nodes' AND action='batch_update' AND EXISTS(SELECT 1 FROM json_each(CASE WHEN json_valid(details_json) THEN details_json ELSE '{}' END,'$.changes') WHERE json_extract(CASE WHEN type='object' THEN value ELSE '{}' END,'$.node_id')=:node)
 UNION ALL SELECT 'audit:'||id,created_sort_ns,'node_order_saved','',0 FROM audit_log WHERE object_type='nodes' AND action='reorder' AND EXISTS(SELECT 1 FROM json_each(CASE WHEN json_valid(details_json) THEN details_json ELSE '{}' END,'$.before') WHERE json_extract(CASE WHEN type='object' THEN value ELSE '{}' END,'$.id')=:node)
)
SELECT id,at,kind,incident_id,approximate FROM events WHERE at>=:start AND at<:end AND (at<:before OR (at=:before AND id<:id)) ORDER BY at DESC,id DESC LIMIT :limit`
