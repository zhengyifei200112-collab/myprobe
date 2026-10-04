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

func TestNegotiatedHTTPDispatchAndIngestion(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "http.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	node, token, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	service, err := db.SaveHTTPService(ctx, store.HTTPService{Name: "fixture", Enabled: true, IntervalSeconds: 60, NodeIDs: []string{node.ID}, Spec: httpcheck.Spec{URL: "http://example.com", Method: "GET", StatusCodes: []int{200}, TimeoutMS: 5000, MaxBodyBytes: 1024}})
	if err != nil {
		t.Fatal(err)
	}
	g := New(db, NewHub())
	g.EnableHTTPProbes(true)
	server := httptest.NewServer(http.HandlerFunc(g.WebSocketV2))
	defer server.Close()
	conn, _, err := websocket.Dial(ctx, strings.Replace(server.URL, "http", "ws", 1), &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + token}}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	hello, _ := v2.NewEnvelope(v1.TypeHello, 1, v1.Hello{AgentVersion: "test", OS: "linux", Architecture: "amd64", CollectionSeconds: 5, ReportSeconds: 5, Capabilities: []string{httpcheck.Capability}})
	if err = wsjson.Write(ctx, conn, hello); err != nil {
		t.Fatal(err)
	}
	var frame v1.Envelope
	if err = wsjson.Read(ctx, conn, &frame); err != nil {
		t.Fatal(err)
	}
	welcome, err := v1.DecodePayload[v2.Welcome](frame)
	if err != nil || !v2.AllowsHTTP(welcome.Capabilities) {
		t.Fatalf("capability: %v", err)
	}
	if err = g.DispatchHTTP(ctx, node.ID, service.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err = wsjson.Read(ctx, conn, &frame); err != nil {
		t.Fatal(err)
	}
	if frame.Version != 2 || frame.Type != v2.TypeHTTPTask {
		t.Fatalf("task: %+v", frame)
	}
	task, err := v1.DecodePayload[httpcheck.Task](frame)
	if err != nil {
		t.Fatal(err)
	}
	result := httpcheck.Result{TaskID: task.ID, ServiceID: task.ServiceID, Revision: task.Revision, ScheduledAt: task.ScheduledAt, CompletedAt: time.Now().UTC(), Outcome: "success", StatusCode: 200, DurationMS: 1}
	response, _ := v2.NewEnvelope(v2.TypeHTTPResult, 2, result)
	for _, expected := range []string{v1.TypeAcknowledged, v1.TypeError} {
		if err = wsjson.Write(ctx, conn, response); err != nil {
			t.Fatal(err)
		}
		if err = wsjson.Read(ctx, conn, &frame); err != nil {
			t.Fatal(err)
		}
		if frame.Version != 2 || frame.Type != expected {
			t.Fatalf("reply: %+v", frame)
		}
	}
	// Persistence, not just an in-memory acknowledgement, consumed this result.
	if err = db.SaveHTTPResult(ctx, node.ID, result, time.Now().UTC()); err != store.ErrHTTPTaskRejected {
		t.Fatalf("not persisted: %v", err)
	}
	g.EnableHTTPProbes(false)
	if err = g.DispatchHTTP(ctx, node.ID, service.ID, time.Now().UTC()); err != ErrHTTPUnavailable {
		t.Fatalf("disabled: %v", err)
	}
}
