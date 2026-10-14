package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

var historyRetentionQueries = []struct{ name, query string }{
	{"raw_metrics", rollupRawMetricsQuery}, {"raw_latency", rollupRawLatencyQuery}, {"raw_traffic", rollupRawTrafficQuery},
	{"minute_metrics", rollupMinuteMetricsQuery}, {"minute_latency", rollupMinuteLatencyQuery}, {"minute_traffic", rollupMinuteTrafficQuery},
}

func TestHistoryRetentionQueryPlans(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, query := range historyRetentionQueries {
		rows, err := s.db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+query.query, formatTime(time.Now().UTC()))
		if err != nil {
			t.Fatal(err)
		}
		pointLookup, sourceRange := false, false
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			t.Log(query.name, detail)
			if strings.Contains(detail, "SEARCH r") && strings.Contains(detail, "_epoch_lookup") && strings.Contains(detail, "<expr>=?") {
				pointLookup = true
			}
			if strings.Contains(detail, "_retention_time") && strings.Contains(detail, "<?") {
				sourceRange = true
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if !pointLookup || !sourceRange {
			t.Fatal(fmt.Sprintf("%s lacks an exact bucket lookup or eligible source range", query.name))
		}
	}
}
