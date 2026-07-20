package store

import (
	"context"
	"testing"
	"time"

	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

func TestBillingPeriodClipsResetDayToShortMonths(t *testing.T) {
	day := 31
	start, end := billingPeriod(time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC), &day)
	if start != time.Date(2026, time.February, 28, 0, 0, 0, 0, time.UTC) || end != time.Date(2026, time.March, 31, 0, 0, 0, 0, time.UTC) {
		t.Fatalf("period=%s..%s", start, end)
	}
	start, end = billingPeriod(time.Date(2028, time.March, 1, 0, 0, 0, 0, time.UTC), &day)
	if start.Day() != 29 || start.Month() != time.February || end.Day() != 31 {
		t.Fatalf("leap period=%s..%s", start, end)
	}
}

func TestTrafficUsageAndHistoryFromStoredSamples(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	node, _, err := database.CreateNode(ctx, CreateNodeParams{Name: "traffic"})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	values := [][2]uint64{{100, 200}, {150, 260}, {20, 10}, {45, 30}}
	for index, value := range values {
		report := protocol.Report{CapturedAt: base.Add(time.Duration(index) * time.Minute), Networks: []protocol.NetworkMetric{{Interface: "eth0", RXTotalBytes: value[0], TXTotalBytes: value[1]}}}
		if err := database.SaveReport(ctx, node.ID, report); err != nil {
			t.Fatal(err)
		}
	}
	usage, err := database.TrafficUsage(ctx, node.ID, nil, base.Add(4*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if usage.RXBytes != 95 || usage.TXBytes != 90 {
		t.Fatalf("usage=%#v", usage)
	}
	history, err := database.TrafficHistory(ctx, node.ID, base, base.Add(4*time.Minute), 60)
	if err != nil || len(history) != 3 {
		t.Fatalf("history=%#v err=%v", history, err)
	}
	last := history[len(history)-1]
	if last.Total != 185 {
		t.Fatalf("last=%#v", last)
	}
	var states int
	if err := database.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM traffic_state WHERE node_id=?", node.ID).Scan(&states); err != nil || states != 1 {
		t.Fatalf("traffic state count=%d err=%v", states, err)
	}
	nextMonth := protocol.Report{CapturedAt: time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC), Networks: []protocol.NetworkMetric{{Interface: "eth0", RXTotalBytes: 500, TXTotalBytes: 700}}}
	if err := database.SaveReport(ctx, node.ID, nextMonth); err != nil {
		t.Fatal(err)
	}
	nextMonth.CapturedAt = nextMonth.CapturedAt.Add(time.Minute)
	nextMonth.Networks[0].RXTotalBytes = 530
	nextMonth.Networks[0].TXTotalBytes = 750
	if err := database.SaveReport(ctx, node.ID, nextMonth); err != nil {
		t.Fatal(err)
	}
	usage, err = database.TrafficUsage(ctx, node.ID, nil, nextMonth.CapturedAt)
	if err != nil || usage.RXBytes != 30 || usage.TXBytes != 50 {
		t.Fatalf("new period usage=%#v err=%v", usage, err)
	}
}

func TestTrafficDeltaHandlesCounterReset(t *testing.T) {
	samples := []trafficSample{{rx: 100, tx: 200}, {rx: 150, tx: 260}, {rx: 20, tx: 10}, {rx: 45, tx: 30}}
	rx, tx := trafficDelta(samples)
	if rx != 95 || tx != 90 {
		t.Fatalf("delta rx=%d tx=%d", rx, tx)
	}
}
