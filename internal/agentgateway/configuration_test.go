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
	v1 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	v2 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v2"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestNegotiatedSamplingAcknowledgementAndLiveRevision(t *testing.T) {
	testSamplingWriteAndAcknowledgement(t, false)
}

func TestBatchSamplingAcknowledgementAndLiveRevision(t *testing.T) {
	testSamplingWriteAndAcknowledgement(t, true)
}

func testSamplingWriteAndAcknowledgement(t *testing.T, batch bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "sampling.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	node, token, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "synthetic sampling", CollectionSeconds: 1, ReportSeconds: 2})
	if err != nil {
		t.Fatal(err)
	}
	gateway := New(db, NewHub()) // The extension does not depend on HTTP checks.
	server := httptest.NewServer(http.HandlerFunc(gateway.WebSocketV2))
	defer server.Close()
	connection, _, err := websocket.Dial(ctx, strings.Replace(server.URL, "http", "ws", 1), &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + token}}})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	hello, _ := v2.NewEnvelope(v1.TypeHello, 1, v1.Hello{AgentVersion: "fixture", OS: "linux", Architecture: "amd64", CollectionSeconds: 5, ReportSeconds: 5, Capabilities: []string{v2.ConfigurationCapability}})
	if err := wsjson.Write(ctx, connection, hello); err != nil {
		t.Fatal(err)
	}
	var frame v1.Envelope
	if err := wsjson.Read(ctx, connection, &frame); err != nil {
		t.Fatal(err)
	}
	welcome, err := v1.DecodePayload[v2.Welcome](frame)
	if err != nil || !v2.AllowsConfiguration(welcome.Capabilities) || welcome.RuntimeConfig == nil || welcome.RuntimeConfig.Validate() != nil {
		t.Fatal("mutually negotiated welcome omitted managed configuration")
	}
	first := *welcome.RuntimeConfig
	if first.CollectionSeconds != 1 || first.ReportSeconds != 2 {
		t.Fatal("welcome did not contain desired settings")
	}
	ack := func(value v2.RuntimeConfiguration, expected string) {
		t.Helper()
		envelope, _ := v2.NewEnvelope(v2.TypeConfigurationApplied, 2, v2.ConfigurationApplied(value))
		if err := wsjson.Write(ctx, connection, envelope); err != nil {
			t.Fatal(err)
		}
		if err := wsjson.Read(ctx, connection, &frame); err != nil {
			t.Fatal(err)
		}
		if frame.Type != expected {
			t.Fatalf("ack reply type=%s", frame.Type)
		}
	}
	badNonce := first
	badNonce.Nonce = strings.Repeat("f", 32)
	ack(badNonce, v1.TypeError)
	badPeriods := first
	badPeriods.ReportSeconds++
	ack(badPeriods, v1.TypeError)
	evidence, err := db.SamplingConfigurationEvidence(ctx, node.ID)
	if err != nil || evidence.Status != "awaiting_agent_ack" || evidence.AppliedRevision != nil {
		t.Fatal("invalid application acknowledgement created proof")
	}
	ack(first, v1.TypeAcknowledged)
	if batch {
		user, err := db.CreateUser(ctx, "synthetic batch administrator", "fixture-hash")
		if err != nil {
			t.Fatal(err)
		}
		collection, report := 2, 3
		preview, err := db.PreviewNodeBatch(ctx, user.ID, store.NodeBatchRequest{NodeIDs: []string{node.ID}, CollectionSeconds: &collection, ReportSeconds: &report}, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ApplyNodeBatch(ctx, user.ID, preview.ID, "synthetic-batch-sampling", time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	} else {
		if _, err := db.UpdateNode(ctx, node.ID, store.UpdateNodeParams{Name: node.Name, CollectionSeconds: 2, ReportSeconds: 3, LatencyMode: v1.TaskKindPing}); err != nil {
			t.Fatal(err)
		}
	}
	ack(first, v1.TypeError)
	if err := wsjson.Read(ctx, connection, &frame); err != nil {
		t.Fatal(err)
	}
	if frame.Type != v1.TypeConfiguration || frame.Version != 2 {
		t.Fatal("live refresh did not send a negotiated configuration")
	}
	second, err := v1.DecodePayload[v2.RuntimeConfiguration](frame)
	if err != nil || second.Revision != 2 || second.Nonce == first.Nonce || second.CollectionSeconds != 2 || second.ReportSeconds != 3 {
		t.Fatal("live refresh reused stale configuration identity")
	}
	ack(second, v1.TypeAcknowledged)
	ack(first, v1.TypeError)
	evidence, err = db.SamplingConfigurationEvidence(ctx, node.ID)
	if err != nil || evidence.Status != "agent_confirmed" || evidence.AppliedRevision == nil || *evidence.AppliedRevision != 2 {
		t.Fatal("old acknowledgement replaced current proof")
	}
}
