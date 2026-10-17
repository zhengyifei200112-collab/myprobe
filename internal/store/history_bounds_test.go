package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

func historyFixture(t *testing.T) (*Store, Node, Target) {
	t.Helper()
	ctx := context.Background()
	database, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	node, _, err := database.CreateNode(ctx, CreateNodeParams{Name: "bounded history"})
	if err != nil {
		t.Fatal(err)
	}
	port := 443
	target, err := database.CreateTarget(ctx, CreateTargetParams{Name: "test", Kind: protocol.TaskKindTCPing, Host: "example.com", Port: &port, IntervalSeconds: 30, TimeoutMS: 1000})
	if err != nil {
		t.Fatal(err)
	}
	return database, node, target
}

func TestBoundedHistoryUsesExactNanosecondOrder(t *testing.T) {
	database, node, target := historyFixture(t)
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, offset := range []time.Duration{-1, 0, 1, 500 * time.Millisecond, time.Second - 1, time.Second, time.Second + 1} {
		at := base.Add(offset)
		value := float64((i + 1) * 10)
		if err := database.SaveReport(ctx, node.ID, protocol.Report{CapturedAt: at,
			CPU:      protocol.CPUMetric{UsagePercent: value},
			Networks: []protocol.NetworkMetric{{Interface: "test", RXTotalBytes: uint64(i * 100)}},
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := database.db.ExecContext(ctx, `INSERT INTO latency_samples(node_id,target_id,kind,captured_at,success,latency_ms,error_class) VALUES(?,?,?,?,1,?,'')`, node.ID, target.ID, protocol.TaskKindTCPing, formatTime(at), value); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		name       string
		start, end time.Duration
		average    float64
		traffic    uint64
	}{
		{"whole second", 0, time.Second, 35, 300},
		{"fractional boundaries", 1, time.Second - 1, 35, 100},
		{"one nanosecond", time.Second, time.Second + 1, 60, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			start, end := base.Add(test.start), base.Add(test.end)
			metrics, err := database.MetricHistoryRange(ctx, node.ID, start, end, 1)
			if err != nil || len(metrics) != 1 || metrics[0].CPUPercent != test.average || !metrics[0].Time.Equal(start.Truncate(time.Second)) {
				t.Fatalf("metrics = %+v, %v", metrics, err)
			}
			latency, err := database.LatencyHistoryRange(ctx, node.ID, start, end, 1)
			if err != nil || len(latency) != 1 || latency[0].LatencyMS == nil || *latency[0].LatencyMS != test.average || latency[0].SuccessRate != 100 || !latency[0].Time.Equal(metrics[0].Time) {
				t.Fatalf("latency = %+v, %v", latency, err)
			}
			traffic, err := database.TrafficHistoryRange(ctx, node.ID, start, end, 1)
			if err != nil {
				t.Fatal(err)
			}
			if test.traffic == 0 {
				if len(traffic) != 0 {
					t.Fatalf("single counter must not invent traffic: %+v", traffic)
				}
			} else if len(traffic) != 1 || traffic[0].RXBytes != test.traffic {
				t.Fatalf("traffic = %+v, want %d", traffic, test.traffic)
			}
		})
	}
}

func TestBoundedHistoryExcludesPartialRetentionBuckets(t *testing.T) {
	for _, seconds := range []int{60, 300} {
		t.Run(fmt.Sprint(seconds), func(t *testing.T) {
			database, node, target := historyFixture(t)
			ctx := context.Background()
			base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			size := time.Duration(seconds) * time.Second
			for i := 0; i < 3; i++ {
				at := formatTime(base.Add(time.Duration(i) * size))
				value := (i + 1) * 10
				for _, insert := range []struct {
					query string
					args  []any
				}{
					{`INSERT INTO metric_rollups VALUES(?,?,?,2,?,0,0,0,0)`, []any{node.ID, seconds, at, 2 * value}},
					{`INSERT INTO latency_rollups VALUES(?,?,?,?,?,2,1,1,?)`, []any{node.ID, target.ID, protocol.TaskKindTCPing, seconds, at, value}},
					{`INSERT INTO traffic_rollups VALUES(?,?,?,?,0)`, []any{node.ID, seconds, at, value * 10}},
				} {
					if _, err := database.db.ExecContext(ctx, insert.query, insert.args...); err != nil {
						t.Fatal(err)
					}
				}
			}
			for _, test := range []struct {
				name       string
				start, end time.Duration
				count      int
			}{
				{"partial both edges", size / 2, 5 * size / 2, 1},
				{"exact complete bucket", size, 2 * size, 1},
				{"no complete bucket", size + 1, 2 * size, 0},
				{"end short by nanosecond", size, 2*size - 1, 0},
			} {
				t.Run(test.name, func(t *testing.T) {
					start, end := base.Add(test.start), base.Add(test.end)
					metrics, err := database.MetricHistoryRange(ctx, node.ID, start, end, seconds)
					if err != nil || len(metrics) != test.count || (test.count > 0 && metrics[0].CPUPercent != 20) {
						t.Fatalf("metrics = %+v, %v", metrics, err)
					}
					latency, err := database.LatencyHistoryRange(ctx, node.ID, start, end, seconds)
					if err != nil || len(latency) != test.count || (test.count > 0 && (latency[0].LatencyMS == nil || *latency[0].LatencyMS != 20 || latency[0].SuccessRate != 50)) {
						t.Fatalf("latency = %+v, %v", latency, err)
					}
					traffic, err := database.TrafficHistoryRange(ctx, node.ID, start, end, seconds)
					if err != nil || len(traffic) != test.count || (test.count > 0 && traffic[0].RXBytes != 200) {
						t.Fatalf("traffic = %+v, %v", traffic, err)
					}
				})
			}
		})
	}
}

func TestHiddenHistoryScopeCoversRawAndRetainedSources(t *testing.T) {
	database, node, target := historyFixture(t)
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 2; i++ {
		if err := database.SaveReport(ctx, node.ID, protocol.Report{CapturedAt: base.Add(time.Duration(120+i*15) * time.Second), CPU: protocol.CPUMetric{UsagePercent: 40}, Networks: []protocol.NetworkMetric{{Interface: "test", RXTotalBytes: uint64(i * 200)}}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, insert := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO metric_rollups VALUES(?,60,?,2,40,0,0,0,0)`, []any{node.ID, formatTime(base)}},
		{`INSERT INTO latency_rollups VALUES(?,?,?,60,?,2,1,1,20)`, []any{node.ID, target.ID, protocol.TaskKindTCPing, formatTime(base)}},
		{`INSERT INTO traffic_rollups VALUES(?,60,?,100,0)`, []any{node.ID, formatTime(base)}},
		{`UPDATE nodes SET hidden=1 WHERE id=?`, []any{node.ID}},
	} {
		if _, err := database.db.ExecContext(ctx, insert.query, insert.args...); err != nil {
			t.Fatal(err)
		}
	}
	end := base.Add(3 * time.Minute)
	metrics, err := database.MetricHistoryRange(ctx, node.ID, base, end, 60)
	if err != nil || len(metrics) != 0 {
		t.Fatalf("public metrics: %+v, %v", metrics, err)
	}
	latency, err := database.LatencyHistoryRange(ctx, node.ID, base, end, 60)
	if err != nil || len(latency) != 0 {
		t.Fatalf("public latency: %+v, %v", latency, err)
	}
	traffic, err := database.TrafficHistoryRange(ctx, node.ID, base, end, 60)
	if err != nil || len(traffic) != 0 {
		t.Fatalf("public traffic: %+v, %v", traffic, err)
	}
	metrics, err = database.AdminMetricHistoryRange(ctx, node.ID, base, end, 60)
	if err != nil || len(metrics) != 2 || metrics[0].CPUPercent != 20 || metrics[1].CPUPercent != 40 {
		t.Fatalf("admin metrics: %+v, %v", metrics, err)
	}
	latency, err = database.AdminLatencyHistoryRange(ctx, node.ID, base, end, 60)
	if err != nil || len(latency) != 1 || latency[0].SuccessRate != 50 {
		t.Fatalf("admin latency: %+v, %v", latency, err)
	}
	traffic, err = database.AdminTrafficHistoryRange(ctx, node.ID, base, end, 60)
	if err != nil || len(traffic) != 2 || traffic[1].RXBytes != 300 {
		t.Fatalf("admin traffic: %+v, %v", traffic, err)
	}
}
