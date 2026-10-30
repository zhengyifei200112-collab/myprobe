package store

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestHistoryBatchDoesNotRecreatePromotedCounterAnchors(t *testing.T) {
	for _, marked := range []bool{true, false} {
		name := "legacy-unmarked"
		if marked {
			name = "previously-marked"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			s, err := Open(ctx, filepath.Join(httpFixtureDir(t), "anchors.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { s.Close() })
			now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
			old := now.Add(-20 * time.Minute)
			policy := RetentionPolicy{Raw: 5 * time.Minute, OneMinute: 10 * time.Minute, FiveMinute: 30 * time.Minute}
			var inactive []string
			for i := 0; i < historyMinuteGroups+1; i++ {
				inactive = append(inactive, fmt.Sprintf("anchor-%02d", i))
			}
			active := "zz-active"
			for _, id := range append(append([]string{}, inactive...), active) {
				if _, _, err := s.CreateNode(ctx, CreateNodeParams{ID: id, Name: "Synthetic " + id}); err != nil {
					t.Fatal(err)
				}
				for i, cpu := range []float64{10, 20, 90} {
					report := historyCapacityReport(old.Add(time.Duration(i)*10*time.Second), i)
					report.CPU.UsagePercent = cpu
					report.Networks[0].RXTotalBytes = uint64(100 + 10*i)
					report.Networks[0].TXTotalBytes = 2 * report.Networks[0].RXTotalBytes
					if err := s.SaveReport(ctx, id, report); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := s.ApplyRetention(ctx, now, policy); err != nil {
				t.Fatal(err)
			}
			assertCount(t, s, "SELECT COUNT(*) FROM metric_rollups WHERE bucket_seconds=60", 0)
			assertCount(t, s, "SELECT COUNT(*) FROM metric_rollups WHERE bucket_seconds=300", len(inactive)+1)
			assertCount(t, s, "SELECT COUNT(*) FROM history_retention_anchors", len(inactive)+1)
			before := make(map[string][]MetricHistoryPoint)
			for _, id := range inactive {
				points, err := s.MetricHistoryRange(ctx, id, old.Add(-time.Minute), old.Add(5*time.Minute), 300)
				if err != nil || len(points) != 1 || math.Abs(points[0].CPUPercent-40) > 1e-9 {
					t.Fatal("invalid independently known weighted baseline", id, points, err)
				}
				before[id] = points
			}
			if !marked {
				// Model a pre-049 full-maintenance database. The first normal
				// startup pass must restore known markers before serving history.
				if _, err := s.db.Exec("DELETE FROM history_retention_anchors"); err != nil {
					t.Fatal(err)
				}
			}
			fresh := now.Add(-8 * time.Minute)
			for i, offset := range []time.Duration{0, 30 * time.Second, time.Minute} {
				report := historyCapacityReport(fresh.Add(offset), i+3)
				report.CPU.UsagePercent = float64(40 + i*10)
				report.Networks[0].RXTotalBytes = []uint64{170, 190, 200}[i]
				report.Networks[0].TXTotalBytes = 2 * report.Networks[0].RXTotalBytes
				if err := s.SaveReport(ctx, active, report); err != nil {
					t.Fatal(err)
				}
			}
			drained := false
			for pass := 0; pass < 6; pass++ {
				batch, err := s.RetainHistory(ctx, now, policy)
				if err != nil {
					t.Fatal(err)
				}
				for _, id := range inactive {
					after, err := s.MetricHistoryRange(ctx, id, old.Add(-time.Minute), old.Add(5*time.Minute), 300)
					if err != nil || !reflect.DeepEqual(after, before[id]) {
						t.Fatalf("pass %d reweighted inactive %s: before=%+v after=%+v error=%v", pass, id, before[id], after, err)
					}
				}
				assertCount(t, s, "SELECT COUNT(*) FROM metric_rollups WHERE bucket_seconds=60 AND node_id<>'zz-active'", 0)
				assertCount(t, s, "SELECT COUNT(*) FROM traffic_rollups WHERE bucket_seconds=60 AND node_id<>'zz-active'", 0)
				if !batch.More {
					drained = true
					break
				}
			}
			if !drained {
				t.Fatal("active backlog did not drain")
			}
			for i, want := range []int64{70, 10} {
				var rx, tx int64
				if err := s.db.QueryRow("SELECT rx_bytes,tx_bytes FROM traffic_rollups WHERE node_id=? AND bucket_seconds=60 AND bucket_at=?", active, formatTime(fresh.Add(time.Duration(i)*time.Minute))).Scan(&rx, &tx); err != nil || rx != want || tx != 2*want {
					t.Fatalf("anchor missing from LAG input: minute=%d rx=%d tx=%d want=%d error=%v", i, rx, tx, want, err)
				}
			}
		})
	}
}
