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
