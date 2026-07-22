package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/gin-gonic/gin"
	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/auth"
	"github.com/zhengyifei200112-collab/myprobe/internal/config"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestSecurityHeadersDisallowInlineScripts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(securityHeaders())
	router.GET("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	policy := response.Header().Get("Content-Security-Policy")
	if !strings.Contains(policy, "script-src 'self'") || strings.Contains(policy, "'unsafe-eval'") || strings.Contains(policy, "script-src 'self' 'unsafe-inline'") {
		t.Fatalf("content security policy = %q", policy)
	}
}

func TestAgentWebSocketHandshake(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	node, token, err := database.CreateNode(ctx, store.CreateNodeParams{Name: "test"})
	if err != nil {
		t.Fatal(err)
	}
	hub := agentgateway.NewHub()
	gateway := agentgateway.New(database, hub)
	api := New(config.Config{}, database, auth.New(database, time.Hour), gateway, hub)
	server := httptest.NewServer(api.Handler())
	defer server.Close()

	endpoint := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/agent/ws"
	connection, response, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{
		HTTPHeader:      http.Header{"Authorization": []string{"Bearer " + token}},
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	hello, _ := protocol.NewEnvelope(protocol.TypeHello, 1, protocol.Hello{AgentVersion: "test"})
	if err := wsjson.Write(ctx, connection, hello); err != nil {
		t.Fatal(err)
	}
	var welcome protocol.Envelope
	if err := wsjson.Read(ctx, connection, &welcome); err != nil {
		t.Fatalf("read welcome (extensions=%q): %v", response.Header.Get("Sec-WebSocket-Extensions"), err)
	}
	if welcome.Type != protocol.TypeWelcome {
		t.Fatalf("message type = %q", welcome.Type)
	}

	port := 443
	target, err := database.CreateTarget(ctx, store.CreateTargetParams{Name: "HTTPS", Kind: protocol.TaskKindTCPing, Host: "example.com", Port: &port, IntervalSeconds: 30, TimeoutMS: 1000})
	if err != nil {
		t.Fatal(err)
	}
	group, err := database.CreateTargetGroup(ctx, "TCP", protocol.TaskKindTCPing)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AddTargetToGroup(ctx, group.ID, target.ID); err != nil {
		t.Fatal(err)
	}
	if err := database.AssignTargetGroup(ctx, node.ID, group.ID); err != nil {
		t.Fatal(err)
	}
	task := protocol.Task{ID: "task-1", Kind: protocol.TaskKindTCPing, TargetID: target.ID, Host: target.Host, Port: port, TimeoutMS: 1000, ExpiresAt: time.Now().UTC().Add(time.Minute)}
	if err := gateway.SendTask(ctx, node.ID, task); err != nil {
		t.Fatal(err)
	}
	var taskEnvelope protocol.Envelope
	if err := wsjson.Read(ctx, connection, &taskEnvelope); err != nil {
		t.Fatal(err)
	}
	if taskEnvelope.Type != protocol.TypeTask {
		t.Fatalf("message type = %q, want task", taskEnvelope.Type)
	}
	result, _ := protocol.NewEnvelope(protocol.TypeTCPingResult, 2, protocol.LatencyResult{TaskID: task.ID, TargetID: target.ID, Success: true, LatencyMS: 12.5, CompletedAt: time.Now().UTC()})
	if err := wsjson.Write(ctx, connection, result); err != nil {
		t.Fatal(err)
	}
	var acknowledgement protocol.Envelope
	if err := wsjson.Read(ctx, connection, &acknowledgement); err != nil {
		t.Fatal(err)
	}
	if acknowledgement.Type != protocol.TypeAcknowledged {
		t.Fatalf("message type = %q, want ack", acknowledgement.Type)
	}
	latest, err := database.ListLatestLatency(ctx, node.ID)
	if err != nil || len(latest) != 1 || latest[0].LatencyMS == nil || *latest[0].LatencyMS != 12.5 {
		t.Fatalf("latest latency = %#v, error = %v", latest, err)
	}
}
