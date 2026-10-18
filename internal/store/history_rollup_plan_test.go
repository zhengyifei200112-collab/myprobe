package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestHistoryRollupSourcesSeekBothTimeBounds(t *testing.T) {
	s, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	args, err := historyQueryArgs("synthetic-node", now.Add(-30*24*time.Hour), now, 3600, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ table, query, bounds string }{
		{"metric_rollups", metricHistoryQuery, "node_id=? AND bucket_seconds=? AND bucket_at>? AND bucket_at<?"},
		{"latency_rollups", latencyHistoryQuery, "node_id=? AND target_id=? AND kind=? AND bucket_seconds=? AND bucket_at>? AND bucket_at<?"},
		{"traffic_rollups", trafficHistoryRangeQuery, "node_id=? AND bucket_seconds=? AND bucket_at>? AND bucket_at<?"},
	} {
		t.Run(item.table, func(t *testing.T) {
			rows, err := s.db.Query(`EXPLAIN QUERY PLAN `+item.query, args...)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var plan strings.Builder
			ranged := false
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				plan.WriteString(detail + "\n")
				if strings.Contains(detail, item.table) && strings.Contains(detail, item.bounds) {
					ranged = true
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if !ranged {
				t.Fatalf("history can scan a node's full retained rollups instead of the requested range:\n%s", plan.String())
			}
		})
	}
}
