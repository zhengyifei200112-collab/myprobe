package agentgateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestGatewayRefreshesIntervalsOnExistingConnectionAndHTTP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	node, token, err := database.CreateNode(ctx, store.CreateNodeParams{Name: "configuration"})
	if err != nil {
		t.Fatal(err)
	}
	user, err := database.CreateUser(ctx, "config-admin", "test-hash")
	if err != nil {
		t.Fatal(err)
	}
	g := New(database, NewHub())
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", g.WebSocket)
	mux.HandleFunc("/hello", g.HTTPHello)
	server := httptest.NewServer(mux)
	defer server.Close()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/ws", &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + token}}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	hello, _ := protocol.NewEnvelope(protocol.TypeHello, 1, protocol.Hello{})
	if err := wsjson.Write(ctx, conn, hello); err != nil {
		t.Fatal(err)
	}
	var message protocol.Envelope
	if err := wsjson.Read(ctx, conn, &message); err != nil {
		t.Fatal(err)
	}
	if message.Type != protocol.TypeWelcome {
		t.Fatal("missing welcome")
	}
	collection, report := 2, 7
	preview, err := database.PreviewNodeBatch(ctx, user.ID, store.NodeBatchRequest{NodeIDs: []string{node.ID}, CollectionSeconds: &collection, ReportSeconds: &report}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ApplyNodeBatch(ctx, user.ID, preview.ID, "gateway-config-test", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	heartbeat, _ := protocol.NewEnvelope(protocol.TypeHeartbeat, 2, struct{}{})
	if err := wsjson.Write(ctx, conn, heartbeat); err != nil {
		t.Fatal(err)
	}
	if err := wsjson.Read(ctx, conn, &message); err != nil {
		t.Fatal(err)
	}
	if message.Type != protocol.TypeConfiguration {
		t.Fatalf("got %q, want config", message.Type)
	}
	settings, err := protocol.DecodePayload[protocol.Config](message)
	if err != nil || settings.CollectionSeconds != 2 || settings.ReportSeconds != 7 {
		t.Fatalf("settings=%+v err=%v", settings, err)
	}
	if err := wsjson.Read(ctx, conn, &message); err != nil {
		t.Fatal(err)
	}
	if message.Type != protocol.TypeAcknowledged {
		t.Fatal("missing heartbeat acknowledgement")
	}
	// No repeated config frame when nothing has changed.
	if err := wsjson.Write(ctx, conn, heartbeat); err != nil {
		t.Fatal(err)
	}
	if err := wsjson.Read(ctx, conn, &message); err != nil {
		t.Fatal(err)
	}
	if message.Type != protocol.TypeAcknowledged {
		t.Fatal("unchanged config was resent")
	}
	raw, _ := json.Marshal(hello)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/hello", strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var ack protocol.Acknowledgement
	if err := json.NewDecoder(response.Body).Decode(&ack); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || ack.Config == nil || ack.Config.CollectionSeconds != 2 || ack.Config.ReportSeconds != 7 {
		t.Fatalf("ack=%+v status=%d", ack, response.StatusCode)
	}
}
