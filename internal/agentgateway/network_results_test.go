package agentgateway

import (
	"context"
	"database/sql"
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

func TestNetworkResultStorageFailureRestartAndDuplicateACK(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	path := filepath.Join(networkFixtureDir(t), "gateway-network.db")
	database, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	node, token, err := database.CreateNode(ctx, store.CreateNodeParams{Name: "Synthetic durable Agent"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := database.CreateTarget(ctx, store.CreateTargetParams{Name: "Synthetic durable target", Kind: protocol.TaskKindPing, Host: "example.test", TimeoutMS: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if err = database.AssignTarget(ctx, node.ID, target.ID); err != nil {
		t.Fatal(err)
	}
	dial := func(g *Gateway) *websocket.Conn {
		t.Helper()
		server := httptest.NewServer(http.HandlerFunc(g.WebSocket))
		t.Cleanup(server.Close)
		connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + token}}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { connection.Close(websocket.StatusNormalClosure, "") })
		hello, _ := protocol.NewEnvelope(protocol.TypeHello, 1, protocol.Hello{AgentVersion: "synthetic", Hostname: "synthetic", OS: "linux", Platform: "test", Architecture: "amd64"})
		if err := wsjson.Write(ctx, connection, hello); err != nil {
			t.Fatal(err)
		}
		var welcome protocol.Envelope
		if err := wsjson.Read(ctx, connection, &welcome); err != nil || welcome.Type != protocol.TypeWelcome {
			t.Fatal("welcome unavailable", err)
		}
		return connection
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	assertSamples := func(expected int) {
		t.Helper()
		var count int
		if err := raw.QueryRow(`SELECT COUNT(*) FROM latency_samples`).Scan(&count); err != nil || count != expected {
			t.Fatalf("samples = %d, expected %d: %v", count, expected, err)
		}
	}
	g := New(database, NewHub())
	connection := dial(g)
	task := protocol.Task{ID: "synthetic-durable-task", TargetID: target.ID, Kind: target.Kind, Host: target.Host, TimeoutMS: 1000, ExpiresAt: time.Now().UTC().Add(time.Minute)}
	if err := g.SendTask(ctx, node.ID, task); err != nil {
		t.Fatal(err)
	}
	var sent protocol.Envelope
	if err := wsjson.Read(ctx, connection, &sent); err != nil || sent.Type != protocol.TypeTask {
		t.Fatal("task unavailable", err)
	}
	if _, err := raw.Exec(`CREATE TRIGGER synthetic_gateway_result_failure BEFORE UPDATE ON network_probe_tasks WHEN NEW.state='completed' BEGIN SELECT RAISE(ABORT,'synthetic storage failure'); END`); err != nil {
		t.Fatal(err)
	}
	result := protocol.LatencyResult{TaskID: task.ID, TargetID: target.ID, Success: true, LatencyMS: 10, CompletedAt: time.Now().UTC()}
	write := func(conn *websocket.Conn, sequence uint64, result protocol.LatencyResult) protocol.Envelope {
		t.Helper()
		envelope, _ := protocol.NewEnvelope(protocol.TypePingResult, sequence, result)
		if err := wsjson.Write(ctx, conn, envelope); err != nil {
			t.Fatal(err)
		}
		var answer protocol.Envelope
		if err := wsjson.Read(ctx, conn, &answer); err != nil {
			t.Fatal(err)
		}
		return answer
	}
	if answer := write(connection, 2, result); answer.Type != protocol.TypeError {
		t.Fatal("failed storage was acknowledged")
	}
	assertSamples(0)
	connection.Close(websocket.StatusNormalClosure, "")
	if _, err := raw.Exec(`DROP TRIGGER synthetic_gateway_result_failure`); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reopened.Close() })
	second := New(reopened, NewHub())
	connection = dial(second)
	if second.Diagnostics().PendingResults != 0 {
		t.Fatal("fresh Gateway unexpectedly has process-local pending tasks")
	}
	for _, sequence := range []uint64{3, 4} {
		answer := write(connection, sequence, result)
		ack, err := protocol.DecodePayload[protocol.Acknowledgement](answer)
		if answer.Type != protocol.TypeAcknowledged || err != nil || ack.Sequence != sequence {
			t.Fatal("persisted retry did not receive matching ACK", err)
		}
		assertSamples(1)
	}
	result.LatencyMS = 11
	if answer := write(connection, 5, result); answer.Type != protocol.TypeError {
		t.Fatal("conflicting replay was accepted")
	}
	assertSamples(1)
}
