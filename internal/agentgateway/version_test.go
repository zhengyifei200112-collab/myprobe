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
	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
	v1 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	v2 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v2"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestVersionedSocketHandshakeAndIsolation(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(string(rune('0'+version)), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			db, err := store.Open(ctx, filepath.Join(t.TempDir(), "version.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			_, token, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			g := New(db, NewHub())
			handler := g.WebSocket
			if version == 2 {
				handler = g.WebSocketV2
			}
			server := httptest.NewServer(http.HandlerFunc(handler))
			defer server.Close()
			connection, _, err := websocket.Dial(ctx, strings.Replace(server.URL, "http", "ws", 1), &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + token}}})
			if err != nil {
				t.Fatal(err)
			}
			defer connection.CloseNow()
			hello, _ := v1.NewEnvelope(v1.TypeHello, 1, v1.Hello{AgentVersion: "test", OS: "linux", Architecture: "amd64", CollectionSeconds: 5, ReportSeconds: 5, Capabilities: []string{httpcheck.Capability}})
			hello.Version = version
			if err := wsjson.Write(ctx, connection, hello); err != nil {
				t.Fatal(err)
			}
			var reply v1.Envelope
			if err := wsjson.Read(ctx, connection, &reply); err != nil {
				t.Fatal(err)
			}
			if reply.Version != version || reply.Type != v1.TypeWelcome {
				t.Fatalf("welcome: %+v", reply)
			}
			if version == 2 {
				welcome, err := v1.DecodePayload[v2.Welcome](reply)
				if err != nil || v2.AllowsHTTP(welcome.Capabilities) {
					t.Fatal("advertised incomplete executor")
				}
			}
			heartbeat, _ := v1.NewEnvelope(v1.TypeHeartbeat, 2, struct{}{})
			heartbeat.Version = version
			if err := wsjson.Write(ctx, connection, heartbeat); err != nil {
				t.Fatal(err)
			}
			if err := wsjson.Read(ctx, connection, &reply); err != nil {
				t.Fatal(err)
			}
			if reply.Version != version || reply.Type != v1.TypeAcknowledged {
				t.Fatalf("ack: %+v", reply)
			}
			heartbeat.Version = 3 - version
			if err := wsjson.Write(ctx, connection, heartbeat); err != nil {
				t.Fatal(err)
			}
			if err := wsjson.Read(ctx, connection, &reply); err != nil {
				t.Fatal(err)
			}
			if reply.Version != version || reply.Type != v1.TypeError {
				t.Fatal("cross-version frame accepted")
			}
		})
	}
}
