package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	report := protocol.Report{CapturedAt: time.Now().UTC(), CPU: protocol.CPUMetric{UsagePercent: 25}, Memory: protocol.MemoryMetric{TotalBytes: 100, UsedBytes: 50, UsagePercent: 50}}
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
		t.Fatalf("body = %#v", body)
	}

	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, httptest.NewRequest(http.MethodGet, "/api/v1/public/nodes/"+node.ID+"/history?range=1y", nil))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid range status = %d", invalid.Code)
	}
}
