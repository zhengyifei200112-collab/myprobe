package agentgateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestDiagnosticsActualWebSocketLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	node, token, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "diagnostic fixture"})
	if err != nil {
		t.Fatal(err)
	}
	g := New(db, NewHub())
	server := httptest.NewServer(http.HandlerFunc(g.WebSocket))
	defer server.Close()
	dial := func() *websocket.Conn {
		connection, _, err := websocket.Dial(ctx, strings.Replace(server.URL, "http", "ws", 1), &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + token}}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { connection.CloseNow() })
		return connection
	}
	hello := func(connection *websocket.Conn) {
		envelope, err := protocol.NewEnvelope(protocol.TypeHello, 1, protocol.Hello{AgentVersion: "test", OS: "linux", Architecture: "amd64", CollectionSeconds: 5, ReportSeconds: 5, Capabilities: []string{"metrics.v1"}})
		if err != nil {
			t.Fatal(err)
		}
		if err := wsjson.Write(ctx, connection, envelope); err != nil {
			t.Fatal(err)
		}
		var welcome protocol.Envelope
		if err := wsjson.Read(ctx, connection, &welcome); err != nil {
			t.Fatal(err)
		}
		if welcome.Type != protocol.TypeWelcome {
			t.Fatalf("first frame: %s", welcome.Type)
		}
	}
	first := dial()
	if g.NodeConnected(node.ID) || g.Diagnostics().AgentConnections != 0 {
		t.Fatal("socket without hello counted as registered Agent")
	}
	hello(first)
	if !g.NodeConnected(node.ID) || g.Diagnostics().AgentConnections != 1 {
		t.Fatal("registered Agent not counted")
	}
	// Read concurrently so the replaced connection can complete its close handshake.
	closed := make(chan error, 1)
	go func() { _, _, err := first.Read(ctx); closed <- err }()
	second := dial()
	hello(second)
	select {
	case err := <-closed:
		if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
			t.Fatalf("replacement close: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("old connection did not close")
	}
	if !g.NodeConnected(node.ID) || g.Diagnostics().AgentConnections != 1 {
		t.Fatal("old disconnect removed replacement")
	}
	task := protocol.Task{ID: "diagnostic-task", TargetID: "diagnostic-target", Kind: protocol.TaskKindPing, Host: "example.com", TimeoutMS: 1000, ExpiresAt: time.Now().UTC().Add(time.Minute)}
	if err := g.SendTask(ctx, node.ID, task); err != nil {
		t.Fatal(err)
	}
	var received protocol.Envelope
	if err := wsjson.Read(ctx, second, &received); err != nil {
		t.Fatal(err)
	}
	if received.Type != protocol.TypeTask || g.Diagnostics().PendingResults != 1 {
		t.Fatal("sent task not reflected in diagnostics")
	}
	second.CloseNow()
	for g.NodeConnected(node.ID) && ctx.Err() == nil {
		time.Sleep(time.Millisecond)
	}
	if g.Diagnostics().AgentConnections != 0 {
		t.Fatal("disconnected Agent retained")
	}
	if g.Diagnostics().PendingResults != 1 {
		t.Fatal("disconnect falsely treated as completed result")
	}
}
