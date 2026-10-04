package agentclient

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/zhengyifei200112-collab/myprobe/internal/collector"
	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
	v1 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	v2 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v2"
)

func TestNegotiatedHTTPTaskReturnsPolicyResult(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	results := make(chan httpcheck.Result, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()
		var hello v2.Envelope
		if err := wsjson.Read(ctx, connection, &hello); err != nil {
			t.Error(err)
			return
		}
		metadata, err := v2.DecodePayload[v1.Hello](hello)
		if err != nil || !v2.AllowsHTTP(metadata.Capabilities) {
			t.Error("Agent did not advertise executor")
			return
		}
		welcome, _ := v2.NewEnvelope(v1.TypeWelcome, 0, v2.Welcome{Capabilities: []string{httpcheck.Capability}})
		if err := wsjson.Write(ctx, connection, welcome); err != nil {
			t.Error(err)
			return
		}
		now := time.Now().UTC()
		task := httpcheck.Task{ID: "fixture", ServiceID: "service", Revision: 1, ScheduledAt: now, ExpiresAt: now.Add(time.Minute), Spec: httpcheck.Spec{URL: "http://127.0.0.1/", Method: "GET", StatusCodes: []int{200}, TimeoutMS: 1000, MaxBodyBytes: 1024}}
		message, _ := v2.NewEnvelope(v2.TypeHTTPTask, 1, task)
		if err := wsjson.Write(ctx, connection, message); err != nil {
			t.Error(err)
			return
		}
		var reply v2.Envelope
		if err := wsjson.Read(ctx, connection, &reply); err != nil {
			t.Error(err)
			return
		}
		result, err := v2.DecodePayload[httpcheck.Result](reply)
		if err != nil || reply.Type != v2.TypeHTTPResult || reply.Version != 2 {
			t.Error("invalid result envelope")
			return
		}
		if err := result.ValidateFor(task, time.Now()); err != nil {
			t.Error(err)
			return
		}
		results <- result
	}))
	defer server.Close()
	client, err := New(Config{ServerURL: server.URL, Token: "fixture-token", AgentVersion: "test", CollectionPeriod: time.Second, ReportPeriod: time.Second}, collector.New(collector.Config{}), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { client.connectAndRead(ctx); close(done) }()
	defer func() { cancel(); client.clearConnection(nil); <-done }()
	select {
	case result := <-results:
		if result.Outcome != "unobserved" || result.ErrorClass != "policy_denied" {
			t.Fatalf("local policy bypassed: %+v", result)
		}
	case <-ctx.Done():
		t.Fatal("no HTTP result")
	}
}
