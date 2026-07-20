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
	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/auth"
	"github.com/zhengyifei200112-collab/myprobe/internal/config"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestAgentWebSocketHandshake(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	_, token, err := database.CreateNode(ctx, store.CreateNodeParams{Name: "test"})
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
}
