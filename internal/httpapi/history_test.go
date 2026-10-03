package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/auth"
	"github.com/zhengyifei200112-collab/myprobe/internal/config"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestPublicHistoryUsesBoundedRanges(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	node, _, _ := database.CreateNode(ctx, store.CreateNodeParams{Name: "history"})
	// Keep the fixture strictly before the half-open end, including on hosts
	// whose clock returns the same instant for several successive reads.
	report := protocol.Report{CapturedAt: time.Now().UTC().Add(-time.Second), CPU: protocol.CPUMetric{UsagePercent: 25}, Memory: protocol.MemoryMetric{TotalBytes: 100, UsedBytes: 50, UsagePercent: 50}}
	if err := database.SaveReport(ctx, node.ID, report); err != nil {
		t.Fatal(err)
	}
	hub := agentgateway.NewHub()
	handler := New(config.Config{}, database, auth.New(database, time.Hour), agentgateway.New(database, hub), hub).Handler()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/public/nodes/"+node.ID+"/history?range=1h", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	var body struct {
		Bucket  int                        `json:"bucket_seconds"`
		Metrics []store.MetricHistoryPoint `json:"metrics"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Bucket != 15 || len(body.Metrics) != 1 {
		t.Fatalf("body = %#v; captured=%s response=%s", body, report.CapturedAt.Format(time.RFC3339Nano), response.Body.String())
	}

	year := httptest.NewRecorder()
	handler.ServeHTTP(year, httptest.NewRequest(http.MethodGet, "/api/v1/public/nodes/"+node.ID+"/history?range=1y", nil))
	if year.Code != http.StatusOK {
		t.Fatalf("one-year range status = %d", year.Code)
	}
	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, httptest.NewRequest(http.MethodGet, "/api/v1/public/nodes/"+node.ID+"/history?range=2y", nil))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid range status = %d", invalid.Code)
	}
	node.Hidden = true
	if _, err := database.UpdateNode(ctx, node.ID, store.UpdateNodeParams{Name: node.Name, Hidden: true, Tags: node.Tags, CountryCode: node.CountryCode, Currency: node.Currency, PriceMinor: node.PriceMinor, BillingCycle: node.BillingCycle, ExpiresAt: node.ExpiresAt, TrafficResetDay: node.TrafficResetDay, UseSinceBoot: node.UseSinceBoot, LatencyMode: node.LatencyMode, CollectionSeconds: node.CollectionSeconds, ReportSeconds: node.ReportSeconds}); err != nil {
		t.Fatal(err)
	}
	hidden := httptest.NewRecorder()
	handler.ServeHTTP(hidden, httptest.NewRequest(http.MethodGet, "/api/v1/public/nodes/"+node.ID+"/history?range=1h", nil))
	if hidden.Code != http.StatusNotFound {
		t.Fatalf("hidden history status = %d", hidden.Code)
	}
}

func TestPublicHistoryAbsoluteWindowExcludesLaterSamples(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	node, _, err := database.CreateNode(ctx, store.CreateNodeParams{Name: "absolute history"})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Add(-time.Hour).Truncate(time.Minute)
	end := start.Add(time.Minute)
	for i, offset := range []time.Duration{-time.Second, 0, 30 * time.Second, time.Minute, 2 * time.Minute} {
		if err := database.SaveReport(ctx, node.ID, protocol.Report{
			CapturedAt: start.Add(offset), CPU: protocol.CPUMetric{UsagePercent: float64(i * 10)},
			Networks: []protocol.NetworkMetric{{Interface: "test", RXTotalBytes: uint64(i * 100)}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	hub := agentgateway.NewHub()
	handler := New(config.Config{}, database, auth.New(database, time.Hour), agentgateway.New(database, hub), hub).Handler()
	query := url.Values{"start": {start.Format(time.RFC3339Nano)}, "end": {end.Format(time.RFC3339Nano)}}
	path := "/api/v1/public/nodes/" + node.ID + "/history?"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path+query.Encode(), nil))
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("response: %d %s", response.Code, response.Body.String())
	}
	var body struct {
		Range    string                      `json:"range"`
		Start    time.Time                   `json:"start"`
		End      time.Time                   `json:"end"`
		Interval string                      `json:"interval"`
		Policy   string                      `json:"rollup_boundary_policy"`
		Metrics  []store.MetricHistoryPoint  `json:"metrics"`
		Traffic  []store.TrafficHistoryPoint `json:"traffic"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Range != "custom" || !body.Start.Equal(start) || !body.End.Equal(end) || body.Interval != "[start,end)" || body.Policy != "complete_buckets_only" {
		t.Fatalf("window metadata: %+v", body)
	}
	if len(body.Metrics) != 2 || body.Metrics[0].CPUPercent != 10 || body.Metrics[1].CPUPercent != 20 || len(body.Traffic) != 1 || body.Traffic[0].RXBytes != 100 {
		t.Fatalf("out-of-window samples affected history: %+v", body)
	}
	query.Set("range", "1h")
	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, httptest.NewRequest(http.MethodGet, path+query.Encode(), nil))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("mixed selectors: %d %s", invalid.Code, invalid.Body.String())
	}
}
