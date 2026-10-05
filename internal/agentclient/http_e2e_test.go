package agentclient

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/collector"
	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
	"github.com/zhengyifei200112-collab/myprobe/internal/scheduler"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestHTTPPeriodicCheckThroughRealAgentAndGateway(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "http-e2e.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	node, token, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	gateway := agentgateway.New(db, agentgateway.NewHub())
	gateway.EnableHTTPProbes(true)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/agent/ws", gateway.WebSocketV2)
	server := httptest.NewServer(mux)
	defer server.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client, err := New(Config{ServerURL: server.URL, Token: token, AgentVersion: "fixture", CollectionPeriod: time.Second, ReportPeriod: time.Second}, collector.New(collector.Config{}), logger)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); client.connectAndRead(ctx) }()
	defer func() {
		cancel()
		client.clearConnection(nil)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("agent failed to stop")
		}
	}()
	for client.currentConnection() == nil && ctx.Err() == nil {
		time.Sleep(time.Millisecond)
	}
	if ctx.Err() != nil {
		t.Fatal("handshake timed out")
	}
	// No target socket is opened: the real executor denies loopback by policy.
	service, err := db.SaveHTTPService(ctx, store.HTTPService{Name: "policy fixture", Enabled: true, IntervalSeconds: 30, NodeIDs: []string{node.ID}, Spec: httpcheck.Spec{URL: "http://127.0.0.1/", Method: "GET", StatusCodes: []int{200}, TimeoutMS: 1000, MaxBodyBytes: 1024}})
	if err != nil {
		t.Fatal(err)
	}
	scheduled := make(chan struct{})
	go func() { defer close(scheduled); scheduler.NewHTTP(db, gateway, logger).Run(ctx) }()
	defer func() {
		cancel()
		select {
		case <-scheduled:
		case <-time.After(3 * time.Second):
			t.Error("scheduler failed to stop")
		}
	}()
	for ctx.Err() == nil {
		observations, err := db.RecentHTTPResults(ctx, service.ID, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(observations) > 0 && observations[0].Result != nil {
			if len(observations) != 1 || observations[0].Result.Outcome != "unobserved" || observations[0].Result.ErrorClass != "policy_denied" {
				t.Fatalf("unexpected result: %+v", observations)
			}
			stats, err := db.HTTPServiceStatistics(ctx, service.ID, node.ID, service.UpdatedAt, service.UpdatedAt.Add(30*time.Second), service.UpdatedAt.Add(5*time.Minute))
			if err != nil || stats.Expected != 1 || stats.Missing != 1 || stats.Unobserved != 1 || stats.Failure != 0 {
				t.Fatalf("policy denial counted as service failure: %+v %v", stats, err)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("periodic HTTP result was not persisted")
}
