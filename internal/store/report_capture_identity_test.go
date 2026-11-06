package store

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

func TestReportReplayUsesCaptureInstantAcrossStoredFractionWidths(t *testing.T) {
	for _, ns := range []int{0, 500000000, 123456789} {
		at := time.Date(2026, 1, 2, 12, 0, 0, ns, time.UTC)
		t.Run(strconv.Itoa(ns), func(t *testing.T) {
			ctx := context.Background()
			s, err := Open(ctx, filepath.Join(httpFixtureDir(t), "capture.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			node, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "Synthetic capture identity"})
			if err != nil {
				t.Fatal(err)
			}
			report := protocol.Report{CapturedAt: at, Networks: []protocol.NetworkMetric{{Interface: "eth0", RXTotalBytes: 100}}}
			if err := s.SaveReport(ctx, node.ID, report); err != nil {
				t.Fatal(err)
			}
			// Legacy UTC text can retain trailing fractional zeros. Its generated
			// nanosecond identity is unchanged, as is the original report payload.
			storedTime := at.Format("2006-01-02T15:04:05.000000000Z")
			if _, err := s.db.Exec(`UPDATE metric_samples SET captured_at=? WHERE node_id=?`, storedTime, node.ID); err != nil {
				t.Fatal(err)
			}
			var latestBefore string
			if err := s.db.QueryRow(`SELECT report_json FROM metric_latest WHERE node_id=?`, node.ID).Scan(&latestBefore); err != nil {
				t.Fatal(err)
			}
			if err := s.SaveReport(ctx, node.ID, report); err != nil {
				t.Fatal(err)
			}
			assertCount(t, s, `SELECT COUNT(*) FROM metric_samples`, 1)
			var latestAfter, keptTime string
			if err := s.db.QueryRow(`SELECT report_json FROM metric_latest WHERE node_id=?`, node.ID).Scan(&latestAfter); err != nil || latestAfter != latestBefore {
				t.Fatalf("replay changed latest report: %v", err)
			}
			if err := s.db.QueryRow(`SELECT captured_at FROM metric_samples WHERE node_id=?`, node.ID).Scan(&keptTime); err != nil || keptTime != storedTime {
				t.Fatalf("replay rewrote stored history time: %v", err)
			}
			conflicting := report
			conflicting.CPU.UsagePercent = 99
			if err := s.SaveReport(ctx, node.ID, conflicting); err == nil {
				t.Fatal("different payload for the same instant was acknowledged")
			}
			late := report
			late.CapturedAt = at.Add(-time.Nanosecond)
			if err := s.SaveReport(ctx, node.ID, late); err != nil {
				t.Fatal("distinct nanosecond capture rejected", err)
			}
			assertCount(t, s, `SELECT COUNT(*) FROM metric_samples`, 2)
			if err := s.db.QueryRow(`SELECT report_json FROM metric_latest WHERE node_id=?`, node.ID).Scan(&latestAfter); err != nil || latestAfter != latestBefore {
				t.Fatalf("late nanosecond capture moved latest: %v", err)
			}
		})
	}
}
