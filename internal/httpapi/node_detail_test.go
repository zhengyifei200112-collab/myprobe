package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/auth"
	"github.com/zhengyifei200112-collab/myprobe/internal/config"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestPublicNodeDetailUsesVisibilityAndPrivacyBoundary(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	node, token, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "detail fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SaveReport(ctx, node.ID, protocol.Report{CapturedAt: time.Now().UTC(), PublicIP: "198.51.100.123"}); err != nil {
		t.Fatal(err)
	}
	hub := agentgateway.NewHub()
	handler := New(config.Config{}, db, auth.New(db, time.Hour), agentgateway.New(db, hub), hub).Handler()
	get := func(id string) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/public/nodes/"+id, nil))
		return response
	}
	response := get(node.ID)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("detail: %d %s", response.Code, response.Body.String())
	}
	for _, private := range []string{token, "198.51.100.123", "token_hash"} {
		if strings.Contains(response.Body.String(), private) {
			t.Fatalf("detail leaked %q", private)
		}
	}
	var body struct {
		Node store.PublicNode `json:"node"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Node.Node.ID != node.ID || body.Node.Report == nil || body.Node.Report.PublicIP != "198.51.••.••" {
		t.Fatalf("public projection: %+v", body.Node)
	}
	_, err = db.UpdateNode(ctx, node.ID, store.UpdateNodeParams{Name: node.Name, Hidden: true, LatencyMode: "ping", CollectionSeconds: 5, ReportSeconds: 5})
	if err != nil {
		t.Fatal(err)
	}
	hidden, missing := get(node.ID), get("missing")
	if hidden.Code != http.StatusNotFound || missing.Code != http.StatusNotFound || hidden.Body.String() != missing.Body.String() {
		t.Fatalf("visibility: hidden=%d %s missing=%d %s", hidden.Code, hidden.Body.String(), missing.Code, missing.Body.String())
	}
}

func TestAdminNodeDetailsRequireSessionAndIncludeHiddenHistory(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	node, token, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "private fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpdateNode(ctx, node.ID, store.UpdateNodeParams{Name: node.Name, Hidden: true, LatencyMode: "ping", CollectionSeconds: 5, ReportSeconds: 5}); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	for i := 0; i < 2; i++ {
		if err := db.SaveReport(ctx, node.ID, protocol.Report{CapturedAt: start.Add(time.Duration(i) * 15 * time.Second), PublicIP: "198.51.100.123", CPU: protocol.CPUMetric{UsagePercent: 20}, Networks: []protocol.NetworkMetric{{Interface: "test", RXTotalBytes: uint64(100 + i*100)}}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.SaveAgentMetadata(ctx, node.ID, protocol.Hello{AgentVersion: "test-version", Hostname: "private-test-host", OS: "linux", Capabilities: []string{"metrics.v1"}}); err != nil {
		t.Fatal(err)
	}
	port := 443
	target, err := db.CreateTarget(ctx, store.CreateTargetParams{Name: "test target", Kind: protocol.TaskKindTCPing, Host: "example.com", Port: &port, IntervalSeconds: 30, TimeoutMS: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AssignTarget(ctx, node.ID, target.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveLatencyResult(ctx, node.ID, protocol.TaskKindTCPing, protocol.LatencyResult{TaskID: "test", TargetID: target.ID, Success: true, LatencyMS: 12, CompletedAt: start}); err != nil {
		t.Fatal(err)
	}
	authService := auth.New(db, time.Hour)
	if _, err := authService.Bootstrap(ctx, "admin", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	hub := agentgateway.NewHub()
	handler := New(config.Config{}, db, authService, agentgateway.New(db, hub), hub).Handler()
	get := func(path string, cookie *http.Cookie) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		if cookie != nil {
			request.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	base := "/api/v1/admin/nodes/" + node.ID
	for _, suffix := range []string{"", "/history?range=1h"} {
		if response := get(base+suffix, nil); response.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated detail: %d %s", response.Code, response.Body.String())
		}
	}
	cookie, _ := loginAdminForShare(t, handler)
	for _, suffix := range []string{"", "/history?range=1h"} {
		response := get(base+suffix, cookie)
		if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("admin detail: %d %s", response.Code, response.Body.String())
		}
		for _, secret := range []string{token, "198.51.100.123", "token_hash"} {
			if strings.Contains(response.Body.String(), secret) {
				t.Fatalf("private detail leaked secret %q", secret)
			}
		}
		if suffix == "" {
			var body struct {
				Node store.PublicNode `json:"node"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if !body.Node.Node.Hidden || body.Node.Node.Agent == nil || body.Node.Node.Agent.AgentVersion != "test-version" || body.Node.Report == nil {
				t.Fatalf("admin snapshot: %+v", body)
			}
		} else {
			var body struct {
				Metrics []store.MetricHistoryPoint  `json:"metrics"`
				Latency []store.LatencyHistoryPoint `json:"latency"`
				Traffic []store.TrafficHistoryPoint `json:"traffic"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if len(body.Metrics) != 2 || len(body.Latency) != 1 || len(body.Traffic) != 1 || body.Traffic[0].RXBytes != 100 {
				t.Fatalf("hidden history: %+v", body)
			}
		}
		public := get("/api/v1/public/nodes/"+node.ID+suffix, cookie)
		if public.Code != http.StatusNotFound {
			t.Fatalf("admin cookie widened public visibility: %d", public.Code)
		}
		missing := get("/api/v1/admin/nodes/missing"+suffix, cookie)
		if missing.Code != http.StatusNotFound {
			t.Fatalf("missing private node: %d", missing.Code)
		}
	}
	if err := authService.Logout(ctx, cookie.Value); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "/history?range=1h"} {
		if response := get(base+suffix, cookie); response.Code != http.StatusUnauthorized {
			t.Fatalf("revoked session still accessed detail: %d", response.Code)
		}
	}
}
