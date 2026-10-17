package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/auth"
	"github.com/zhengyifei200112-collab/myprobe/internal/config"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestNodeTimelineRejectsInvalidWindowAndPagination(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "timeline input fixture"})
	if err != nil {
		t.Fatal(err)
	}
	hub := agentgateway.NewHub()
	handler := New(config.Config{}, db, auth.New(db, time.Hour), agentgateway.New(db, hub), hub).Handler()
	for _, query := range []string{
		"range=invalid&limit=50", "start=invalid&end=invalid&limit=50", "range=1h&limit=0", "range=1h&limit=101", "range=1h&limit=1&limit=2", "range=1h&cursor=x&cursor=y", "range=1h&cursor=x", "range=1h&start=2026-01-01T00:00:00Z&end=2026-01-01T00:00:01Z", "start=2026-01-01T00:00:00Z&end=9999-01-01T00:00:00Z",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/public/nodes/"+node.ID+"/events?"+query, nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("query %q accepted: %d %s", query, response.Code, response.Body.String())
		}
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("timeline response can be cached")
		}
	}
	if _, err := db.UpdateNode(ctx, node.ID, store.UpdateNodeParams{Name: node.Name, Hidden: true, LatencyMode: "ping", CollectionSeconds: 5, ReportSeconds: 5}); err != nil {
		t.Fatal(err)
	}
	var first string
	for _, id := range []string{node.ID, "missing"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/public/nodes/"+id+"/events?range=1h", nil))
		if response.Code != http.StatusNotFound || first != "" && first != response.Body.String() {
			t.Fatalf("hidden/missing mismatch: %d %s", response.Code, response.Body.String())
		}
		first = response.Body.String()
	}
	// The new API is intentionally absent from share routes; an existing share
	// token cannot widen its chart authorization into administrative event access.
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/share/nodes/"+node.ID+"/events", nil))
	if response.Code == http.StatusOK {
		t.Fatal("share acquired unscoped events")
	}
}
