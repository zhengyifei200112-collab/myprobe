package agentclient

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestJointHistoryValidation(t *testing.T) {
	anchor := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	for _, rangeName := range []string{"30d", "1y"} {
		t.Run(rangeName, func(t *testing.T) {
			days, seconds := 30, 3600
			if rangeName == "1y" {
				days, seconds = 365, 43200
			}
			step := time.Duration(seconds) * time.Second
			var metrics []store.MetricHistoryPoint
			var latency []store.LatencyHistoryPoint
			var traffic []store.TrafficHistoryPoint
			var rx uint64
			ms := 10.0
			for at := anchor.Add(-time.Duration(days) * 24 * time.Hour); at.Before(anchor); at = at.Add(step) {
				metrics = append(metrics, store.MetricHistoryPoint{Time: at, CPUPercent: 17, MemoryPercent: 300.0 / 2048 * 100})
				rx += uint64(seconds/15) * 1000
				traffic = append(traffic, store.TrafficHistoryPoint{Time: at, RXBytes: rx, TXBytes: 2 * rx, Total: 3 * rx})
				for target := range 3 {
					latency = append(latency, store.LatencyHistoryPoint{Time: at, TargetID: fmt.Sprint(target), Kind: "tcping", LatencyMS: &ms, SuccessRate: 100})
				}
			}
			body := func(m []store.MetricHistoryPoint, l []store.LatencyHistoryPoint, tr []store.TrafficHistoryPoint) []byte {
				raw, err := json.Marshal(map[string]any{"range": rangeName, "bucket_seconds": seconds, "metrics": m, "latency": l, "traffic": tr})
				if err != nil {
					t.Fatal(err)
				}
				return raw
			}
			if err := validateJointHistory(body(metrics, latency, traffic), rangeName, anchor, anchor); err != nil {
				t.Fatal(err)
			}
			// A long run's API window moves forward and legitimately omits old buckets.
			dayBuckets := int(24 * time.Hour / step)
			if err := validateJointHistory(body(metrics[dayBuckets:], latency[3*dayBuckets:], traffic[dayBuckets:]), rangeName, anchor, anchor.Add(24*time.Hour)); err != nil {
				t.Fatalf("moving 24-hour query window: %v", err)
			}
			cases := map[string][]byte{
				"empty":                  body(nil, nil, nil),
				"missing older layer":    body(metrics[len(metrics)/2:], latency, traffic),
				"missing latency target": body(metrics, append(append([]store.LatencyHistoryPoint{}, latency[:100]...), latency[101:]...), traffic),
				"duplicate metric":       body(append(append([]store.MetricHistoryPoint{}, metrics...), metrics[100]), latency, traffic),
				"missing traffic bucket": body(metrics, latency, append(append([]store.TrafficHistoryPoint{}, traffic[:100]...), traffic[101:]...)),
			}
			wrongMetric := append([]store.MetricHistoryPoint{}, metrics...)
			wrongMetric[100].CPUPercent = 18
			cases["wrong metric"] = body(wrongMetric, latency, traffic)
			wrongTraffic := append([]store.TrafficHistoryPoint{}, traffic...)
			wrongTraffic[100].RXBytes++
			cases["wrong traffic"] = body(metrics, latency, wrongTraffic)
			for name, raw := range cases {
				t.Run(name, func(t *testing.T) {
					if validateJointHistory(raw, rangeName, anchor, anchor) == nil {
						t.Fatal("invalid history accepted")
					}
				})
			}
		})
	}
}
