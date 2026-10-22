package agentclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/collector"
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

func TestClientObservationCanceledReportRetainsRawGap(t *testing.T) {
	var requests atomic.Int64
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(200) }))
	defer receiver.Close()
	client := observedTestClient(t, receiver.URL)
	trace, err := newJointEnvelopeTrace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := &jointReportObserver{trace: trace}
	p.observeClient(client, "private-node-name")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report := protocol.Report{CapturedAt: time.Date(2026, 10, 23, 0, 0, 0, 1, time.UTC), PublicIP: "198.51.100.99", CPU: protocol.CPUMetric{UsagePercent: 17.125}}
	err = client.sendReport(ctx, report)
	if !errors.Is(err, context.Canceled) || requests.Load() != 0 || client.sequence.Load() != 1 {
		t.Fatal("observation changed cancellation behavior")
	}
	var account jointReportAccounting
	p.sequenceAccounting(&account, map[string]uint64{"private-node-name": client.sequence.Load()})
	if account.UnobservedGeneratedEnvelopes != 1 || account.GeneratedEnvelopes != 1 {
		t.Fatal("local failure silently exempted from original accounting")
	}
	summary := p.finishTrace()
	if summary.WriteErrors != 0 || !summary.SyncedAndClosed || summary.Format != "myprobe.joint-envelope-trace.v2" {
		t.Fatal("trace incomplete")
	}
	raw, err := os.ReadFile(trace.file.Name())
	if err != nil {
		t.Fatal(err)
	}
	events := map[string]map[string]any{}
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte{'\n'}) {
		var event map[string]any
		if json.Unmarshal(line, &event) != nil {
			t.Fatal("invalid trace")
		}
		events[event["event"].(string)] = event
		if event["sequence"] != "1" {
			t.Fatal("wrong sequence attribution")
		}
	}
	if events["client_generated"]["type"] != protocol.TypeReport || events["client_write_returned"]["error_class"] != "canceled" || events["client_write_returned"]["transport"] != "http" || events["sequence_gap"] == nil {
		t.Fatal("client-local failure not correlated with observer gap")
	}
	payload, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(payload)
	if events["client_report"]["payload_sha256"] != hex.EncodeToString(hash[:]) || events["client_report"]["captured_at"] != report.CapturedAt.Format(time.RFC3339Nano) {
		t.Fatal("report metadata not exact")
	}
	for _, secret := range []string{"owned-token-not-for-trace", "private-node-name", "198.51.100.99", "usage_percent", "Authorization", receiver.URL} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatal("trace exposed private data")
		}
	}
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
	received := make(chan protocol.Envelope, 2)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
		if err != nil {
			return
		}
		defer connection.CloseNow()
		for i := 0; i < 2; i++ {
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
	for i, kind := range []string{protocol.TypeReport, v2.TypeConfigurationApplied} {
		select {
		case envelope := <-received:
			if envelope.Version != 2 || envelope.Sequence != uint64(i+1) || envelope.Type != kind {
				t.Fatal("observed wrapper changed original wire envelope")
			}
		case <-ctx.Done():
			t.Fatal("write did not reach receiver")
		}
	}
	if len(events) != 5 || events[2].Phase != "write_returned" || events[2].Type != protocol.TypeReport || events[4].Type != v2.TypeConfigurationApplied || events[4].Phase != "write_returned" || events[4].Sequence != 2 || events[4].Transport != "websocket" {
		t.Fatal("v1/v2 write result observation incomplete")
	}
}

func TestClientObservationServerRejectionDoesNotExposeMessage(t *testing.T) {
	trace, err := newJointEnvelopeTrace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := &jointReportObserver{trace: trace}
	for _, code := range []string{"storage_error", "private-credential-in-unknown-code"} {
		envelope, err := protocol.NewEnvelope(protocol.TypeError, 0, protocol.ProtocolError{Code: code, Message: "private rejection message must never be logged"})
		if err != nil {
			t.Fatal(err)
		}
		p.observeServerEnvelope("owned", envelope)
	}
	summary := p.finishTrace()
	if summary.WriteErrors != 0 || summary.Events != 2 {
		t.Fatal("server rejection trace missing")
	}
	raw, err := os.ReadFile(trace.file.Name())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("private")) {
		t.Fatal("server-controlled error text leaked")
	}
	classes := []string{}
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte{'\n'}) {
		var event map[string]any
		if json.Unmarshal(line, &event) != nil {
			t.Fatal("invalid trace")
		}
		if event["sequence"] != "0" || event["event"] != "server_protocol_error" {
			t.Fatal("server error falsely attributed to client request")
		}
		classes = append(classes, event["error_class"].(string))
	}
	if len(classes) != 2 || classes[0] != "storage_error" || classes[1] != "unclassified" {
		t.Fatal("server rejection classification changed")
	}
}
