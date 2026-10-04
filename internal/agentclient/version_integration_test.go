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
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestAgentReportsThroughNewAndLegacyServers(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(string(rune('0'+version)), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			db, err := store.Open(ctx, filepath.Join(t.TempDir(), "agent.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			node, token, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "protocol fixture"})
			if err != nil {
				t.Fatal(err)
			}
			gateway := agentgateway.New(db, agentgateway.NewHub())
			mux := http.NewServeMux()
			mux.HandleFunc("/api/v1/agent/ws", gateway.WebSocket)
			mux.HandleFunc("/api/v1/agent/report", gateway.HTTPReport)
			if version == 2 {
				mux.HandleFunc("/api/v2/agent/ws", gateway.WebSocketV2)
			}
			server := httptest.NewServer(mux)
			defer server.Close()
			client, err := New(Config{ServerURL: server.URL, Token: token, AgentVersion: "integration-fixture", CollectionPeriod: time.Second, ReportPeriod: time.Second}, collector.New(collector.Config{}), slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() { client.connectAndRead(ctx); close(done) }()
			defer func() {
				cancel()
				client.clearConnection(nil)
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					t.Error("client failed to stop")
				}
			}()
			for client.currentConnection() == nil && ctx.Err() == nil {
				time.Sleep(time.Millisecond)
			}
			if client.currentConnection() == nil {
				t.Fatal("no completed handshake")
			}
			_, selected := client.currentConnectionVersion()
			if selected != version {
				t.Fatalf("selected %d want %d", selected, version)
			}
			report := protocol.Report{CapturedAt: time.Now().UTC(), CPU: protocol.CPUMetric{UsagePercent: 17}}
			if err := client.sendReport(ctx, report); err != nil {
				t.Fatal(err)
			}
			for ctx.Err() == nil {
				nodes, err := db.ListNodes(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if len(nodes) == 1 && nodes[0].LastSeenAt != nil {
					break
				}
				time.Sleep(time.Millisecond)
			}
			if ctx.Err() != nil {
				t.Fatal("socket report was not persisted")
			}
			client.clearConnection(nil)
			select {
			case <-done:
			case <-ctx.Done():
				t.Fatal("socket did not close")
			}
			report.CapturedAt = report.CapturedAt.Add(time.Second)
			if err := client.sendReport(ctx, report); err != nil {
				t.Fatalf("v1 HTTP fallback after v%d: %v", version, err)
			}
			nodes, err := db.ListNodes(ctx)
			if err != nil || len(nodes) != 1 || nodes[0].ID != node.ID || nodes[0].Agent == nil || nodes[0].Agent.AgentVersion != "integration-fixture" {
				t.Fatal("handshake metadata not retained")
			}
		})
	}
}
