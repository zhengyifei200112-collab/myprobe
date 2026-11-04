package agentclient

import (
	"context"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/zhengyifei200112-collab/myprobe/internal/collector"
	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	v2 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v2"
)

func observedTestClient(t *testing.T, base string) *Client {
	t.Helper()
	client, err := New(Config{ServerURL: base, Token: "owned-token-not-for-trace", CollectionPeriod: time.Second, ReportPeriod: time.Second}, collector.New(collector.Config{}), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestClientObservationHTTPResponseIsNotAcceptance(t *testing.T) {
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(502)
		io.WriteString(w, "private response body")
	}))
	defer receiver.Close()
	client := observedTestClient(t, receiver.URL)
	events := []clientEnvelopeEvent{}
	client.observeEnvelope = func(event clientEnvelopeEvent) { events = append(events, event) }
	err := client.sendReport(context.Background(), protocol.Report{CapturedAt: time.Now().UTC()})
	if err == nil || len(events) != 3 {
		t.Fatal("HTTP failure/result observation changed")
	}
	last := events[2]
	if last.Phase != "write_returned" || last.Transport != "http" || last.HTTPStatus != 502 || last.ErrorClass != "" {
		t.Fatal("transport return and HTTP status conflated")
	}
}

func TestClientObservationEncodingErrorKeepsAllocatedSequence(t *testing.T) {
	events := []clientEnvelopeEvent{}
	client := &Client{observeEnvelope: func(event clientEnvelopeEvent) { events = append(events, event) }}
	err := client.sendReport(context.Background(), protocol.Report{CapturedAt: time.Now().UTC(), CPU: protocol.CPUMetric{UsagePercent: math.NaN()}})
	if err == nil || len(events) != 2 || events[0].Sequence != 1 || events[1].Sequence != 1 || events[1].Phase != "local_rejected" || events[1].ErrorClass != "encoding" {
		t.Fatal("encoding failure lost its allocated sequence")
	}
}

func TestClientObservationDisconnectedResultKeepsAllocatedSequence(t *testing.T) {
	events := []clientEnvelopeEvent{}
	client := &Client{observeEnvelope: func(event clientEnvelopeEvent) { events = append(events, event) }}
	err := client.sendLatencyResult(context.Background(), protocol.TaskKindTCPing, protocol.LatencyResult{TaskID: "owned", TargetID: "owned", Success: true, LatencyMS: 1, CompletedAt: time.Now().UTC()})
	if err == nil || len(events) != 2 || events[0].Sequence != 1 || events[1].Sequence != 1 || events[1].Phase != "local_rejected" || events[1].ErrorClass != "disconnected" {
		t.Fatal("disconnected result lost its allocated sequence")
	}
}

func TestClientObservationIsDisabledByDefault(t *testing.T) {
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer receiver.Close()
	client := observedTestClient(t, receiver.URL)
	if client.observeEnvelope != nil {
		t.Fatal("new production client has test observation enabled")
	}
	if err := client.sendReport(context.Background(), protocol.Report{CapturedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
}

func TestClientObservationV1AndV2WritesPreserveWireEnvelope(t *testing.T) {
	received := make(chan protocol.Envelope, 4)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
		if err != nil {
			return
		}
		defer connection.CloseNow()
		for i := 0; i < 4; i++ {
			var envelope protocol.Envelope
			if wsjson.Read(r.Context(), connection, &envelope) != nil {
				return
			}
			received <- envelope
		}
	}))
	defer receiver.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(receiver.URL, "http"), &websocket.DialOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	client := observedTestClient(t, receiver.URL)
	client.conn = connection
	client.connVersion = 2
	events := []clientEnvelopeEvent{}
	client.observeEnvelope = func(event clientEnvelopeEvent) { events = append(events, event) }
	report := protocol.Report{CapturedAt: time.Now().UTC()}
	if err = client.sendReport(ctx, report); err != nil {
		t.Fatal(err)
	}
	configuration := v2.RuntimeConfiguration{Scope: v2.SamplingConfigurationScope, Revision: 1, Nonce: strings.Repeat("a", 32), CollectionSeconds: 15, ReportSeconds: 15}
	if err = configuration.Validate(); err != nil {
		t.Fatal(err)
	}
	if err = client.sendConfigurationApplied(ctx, connection, 2, configuration); err != nil {
		t.Fatal(err)
	}
	client.sendHTTPTaskResult(ctx, connection, httpcheck.Result{TaskID: "owned-http-task", ServiceID: "owned-http-service"})
	if err = client.sendLatencyResult(ctx, protocol.TaskKindTCPing, protocol.LatencyResult{TaskID: "owned-tcp-task", TargetID: "owned-tcp-target"}); err != nil {
		t.Fatal(err)
	}
	for i, kind := range []string{protocol.TypeReport, v2.TypeConfigurationApplied, v2.TypeHTTPResult, protocol.TypeTCPingResult} {
		select {
		case envelope := <-received:
			if envelope.Version != 2 || envelope.Sequence != uint64(i+1) || envelope.Type != kind {
				t.Fatal("observed wrapper changed original wire envelope")
			}
		case <-ctx.Done():
			t.Fatal("write did not reach receiver")
		}
	}
	if len(events) != 9 {
		t.Fatal("incomplete v1/v2 lifecycle", events)
	}
	writes := []clientEnvelopeEvent{events[2], events[4], events[6], events[8]}
	for i, kind := range []string{protocol.TypeReport, v2.TypeConfigurationApplied, v2.TypeHTTPResult, protocol.TypeTCPingResult} {
		got := writes[i]
		if got.Phase != "write_returned" || got.Type != kind || got.Sequence != uint64(i+1) || got.Transport != "websocket" || got.ErrorClass != "" {
			t.Fatal("missing v1/v2 write result", got)
		}
	}
}
