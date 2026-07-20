package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentclient"
	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/auth"
	"github.com/zhengyifei200112-collab/myprobe/internal/collector"
	"github.com/zhengyifei200112-collab/myprobe/internal/config"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestAgentExecutesTCPTaskEndToEnd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	node, token, err := database.CreateNode(ctx, store.CreateNodeParams{Name: "agent"})
	if err != nil {
		t.Fatal(err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			_ = connection.Close()
		}
	}()
	port := listener.Addr().(*net.TCPAddr).Port
	target, err := database.CreateTarget(ctx, store.CreateTargetParams{Name: "local TCP", Kind: protocol.TaskKindTCPing, Host: "127.0.0.1", Port: &port, IntervalSeconds: 30, TimeoutMS: 1000})
	if err != nil {
		t.Fatal(err)
	}
	group, _ := database.CreateTargetGroup(ctx, "local", protocol.TaskKindTCPing)
	if err := database.AddTargetToGroup(ctx, group.ID, target.ID); err != nil {
		t.Fatal(err)
	}
	if err := database.AssignTargetGroup(ctx, node.ID, group.ID); err != nil {
		t.Fatal(err)
	}

	hub := agentgateway.NewHub()
	gateway := agentgateway.New(database, hub)
	api := New(config.Config{}, database, auth.New(database, time.Hour), gateway, hub)
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client, err := agentclient.New(agentclient.Config{ServerURL: server.URL, Token: token, CollectionPeriod: time.Second, ReportPeriod: time.Second, AgentVersion: "test"}, collector.New(collector.Config{}), logger)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- client.Run(ctx) }()

	task := protocol.Task{ID: "e2e-task", Kind: protocol.TaskKindTCPing, TargetID: target.ID, Host: target.Host, Port: port, TimeoutMS: 1000, ExpiresAt: time.Now().UTC().Add(time.Minute)}
	deadline := time.Now().Add(5 * time.Second)
	for {
		err = gateway.SendTask(ctx, node.ID, task)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("agent did not connect: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	for {
		latest, listErr := database.ListLatestLatency(ctx, node.ID)
		if listErr == nil && len(latest) == 1 && latest[0].Success != nil {
			if !*latest[0].Success || latest[0].LatencyMS == nil {
				t.Fatalf("latency result = %#v", latest[0])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("latency result was not stored: %#v, error = %v", latest, listErr)
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("agent did not stop")
	}
}
