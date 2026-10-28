package store

import (
	"context"
	"testing"
	"time"
)

func TestTrafficHistoryExactAndEqualTimestampCounterOrder(t *testing.T) {
	s, node, _ := historyFixture(t)
	s.db.SetMaxOpenConns(1)
	// Intentionally interleave exact instants and equivalent spellings. Resets
	// make both the chronological and the previous text tie ordering observable.
	for _, row := range []struct {
		at string
		rx int
	}{
		{"2026-01-01T00:00:00.100000000Z", 40},
		{"2026-01-01T00:00:00Z", 100},
		{"2026-01-01T00:00:00.0Z", 10},
		{"2026-01-01T00:00:00.000Z", 80},
		{"2026-01-01T00:00:00.9Z", 15},
		{"2026-01-01T00:00:00.100Z", 80},
		{"2026-01-01T00:00:00.100000000Z", 30},
		{"2026-01-01T00:00:01Z", 25},
	} {
		_, err := s.db.Exec(`INSERT INTO metric_samples(node_id,captured_at,cpu_usage,memory_used,memory_total,disk_used,disk_total,net_rx_rate,net_tx_rate,net_rx_total,net_tx_total,report_json) VALUES(?,?,0,0,0,0,0,0,0,?,?,'{}')`, node.ID, row.at, row.rx, 2*row.rx)
		if err != nil {
			t.Fatal(err)
		}
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := s.db.Exec(`INSERT INTO traffic_rollups VALUES(?,60,?,500,1000)`, node.ID, formatTime(base)); err != nil {
		t.Fatal(err)
	}
	// Raw deltas: 10+90+40+30+50+15+10=245; retained traffic adds 500.
	for _, mode := range []string{"OFF", "ON"} {
		if _, err := s.db.Exec("PRAGMA reverse_unordered_selects=" + mode); err != nil {
			t.Fatal(err)
		}
		got, err := s.TrafficHistoryRange(context.Background(), node.ID, base, base.Add(2*time.Minute), 60)
		if err != nil || len(got) != 1 || got[0].RXBytes != 745 || got[0].TXBytes != 1490 || got[0].Total != 2235 || !got[0].Time.Equal(base) {
			t.Fatalf("reversed=%s counter order/reset/retained totals: %+v, %v", mode, got, err)
		}
	}
}
