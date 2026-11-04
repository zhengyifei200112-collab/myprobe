package agentclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	v2 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v2"
)

func TestClientObservationCanceledReportPreservesSequenceAndPrivacy(t *testing.T) {
	var requests atomic.Int64
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer receiver.Close()
	client := observedTestClient(t, receiver.URL)
	var events []clientEnvelopeEvent
	client.observeEnvelope = func(event clientEnvelopeEvent) { events = append(events, event) }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report := protocol.Report{CapturedAt: time.Date(2026, 10, 23, 0, 0, 0, 1, time.UTC), PublicIP: "198.51.100.99", CPU: protocol.CPUMetric{UsagePercent: 17.125}}
	if err := client.sendReport(ctx, report); !errors.Is(err, context.Canceled) || requests.Load() != 0 || client.sequence.Load() != 1 {
		t.Fatal("observation changed cancellation or sequence accounting", err)
	}
	if len(events) != 3 || events[0].Phase != "generated" || events[1].Phase != "report" || events[2].Phase != "write_returned" || events[2].ErrorClass != "canceled" || events[2].Transport != "http" {
		t.Fatal("canceled report missing lifecycle evidence", events)
	}
	payload, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if events[1].CapturedAt != report.CapturedAt || events[1].PayloadSHA256 != sha256.Sum256(payload) {
		t.Fatal("report identity changed")
	}
	for _, event := range events {
		if event.Sequence != 1 {
			t.Fatal("lost allocated sequence")
		}
	}
	raw, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"owned-token-not-for-trace", "198.51.100.99", "usage_percent", "Authorization", receiver.URL} {
		if bytes.Contains(raw, []byte(private)) {
			t.Fatal("event exposes private data")
		}
	}
}

func TestClientObservationFallbackRetainsEnvelopeAndAppliesConfiguration(t *testing.T) {
	received := make(chan protocol.Envelope, 1)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var envelope protocol.Envelope
		if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
			t.Error(err)
			return
		}
		received <- envelope
		io.WriteString(w, `{"sequence":1,"config":{"collection_seconds":3,"report_seconds":4}}`)
	}))
	defer receiver.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// A real closed socket forces the existing WebSocket-to-HTTP path.
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err == nil {
			conn.CloseNow()
		}
	}))
	defer wsServer.Close()
	conn, _, err := websocket.Dial(ctx, "ws"+wsServer.URL[len("http"):], nil)
	if err != nil {
		t.Fatal(err)
	}
	conn.CloseNow()
	client := observedTestClient(t, receiver.URL)
	client.conn, client.connVersion = conn, 2
	var events []clientEnvelopeEvent
	client.observeEnvelope = func(event clientEnvelopeEvent) { events = append(events, event) }
	report := protocol.Report{CapturedAt: time.Now().UTC(), CPU: protocol.CPUMetric{UsagePercent: 17}}
	if err = client.sendReport(ctx, report); err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 || events[2].Transport != "websocket" || events[2].ErrorClass == "" || events[3].Transport != "http" || events[3].HTTPStatus != 200 {
		t.Fatal("fallback evidence incomplete", events)
	}
	for _, event := range events {
		if event.Sequence != 1 {
			t.Fatal("fallback allocated a new sequence")
		}
	}
	var envelope protocol.Envelope
	select {
	case envelope = <-received:
	case <-ctx.Done():
		t.Fatal("report did not reach HTTP receiver")
	}
	if envelope.Version != 1 || envelope.Sequence != 1 || envelope.Type != protocol.TypeReport || sha256.Sum256(envelope.Payload) != events[1].PayloadSHA256 {
		t.Fatal("fallback changed wire identity")
	}
	if client.reportNS.Load() != int64(4*time.Second) || client.collectionNS.Load() != int64(3*time.Second) {
		t.Fatal("fallback configuration was lost")
	}
}

func TestClientObservationConfigurationFenceDoesNotWrite(t *testing.T) {
	var events []clientEnvelopeEvent
	client := &Client{observeEnvelope: func(event clientEnvelopeEvent) { events = append(events, event) }}
	// A stale identity is rejected before any socket method is called.
	if err := client.sendConfigurationApplied(context.Background(), new(websocket.Conn), 2, v2.RuntimeConfiguration{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Phase != "generated" || events[1].Phase != "local_rejected" || events[1].ErrorClass != "canceled" || events[1].Sequence != events[0].Sequence {
		t.Fatal("stale connection rejection missing", events)
	}
}

func TestClientObservationConcurrentGenerationAndErrorPrivacy(t *testing.T) {
	var mu sync.Mutex
	var events []clientEnvelopeEvent
	client := &Client{observeEnvelope: func(event clientEnvelopeEvent) { mu.Lock(); defer mu.Unlock(); events = append(events, event) }}
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			_ = client.sendLatencyResult(context.Background(), protocol.TaskKindTCPing, protocol.LatencyResult{})
		})
	}
	wg.Wait()
	counts := map[uint64]int{}
	for _, event := range events {
		counts[event.Sequence]++
	}
	if len(counts) != 32 || len(events) != 64 {
		t.Fatal("concurrent sequences lost")
	}
	for sequence, count := range counts {
		if sequence == 0 || count != 2 {
			t.Fatal("sequence identity reused")
		}
	}
	for _, test := range []struct {
		err   error
		class string
	}{
		{context.Canceled, "canceled"}, {context.DeadlineExceeded, "deadline"},
		{&net.DNSError{Err: "private-host-error", Name: "private-host", IsTimeout: true}, "network_timeout"},
		{errors.New("private-credential-in-error"), "transport"},
	} {
		client.observeEnvelopeReturn(protocol.TypeReport, 33, "http", test.err, 0)
		last := events[len(events)-1]
		if last.ErrorClass != test.class {
			t.Fatal("wrong transport class", last)
		}
		encoded, err := json.Marshal(last)
		if err != nil || bytes.Contains(encoded, []byte("private")) {
			t.Fatal("raw transport error leaked")
		}
	}
}

func TestClientObservationHelloAndHeartbeatReachLegacyServer(t *testing.T) {
	received := make(chan protocol.Envelope, 2)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/agent/ws" {
			http.NotFound(w, r)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		var envelope protocol.Envelope
		if err = wsjson.Read(r.Context(), conn, &envelope); err != nil {
			t.Error(err)
			return
		}
		received <- envelope
		welcome, err := protocol.NewEnvelope(protocol.TypeWelcome, 0, protocol.Welcome{Config: protocol.Config{CollectionSeconds: 15, ReportSeconds: 15}})
		if err != nil {
			t.Error(err)
			return
		}
		if err = wsjson.Write(r.Context(), conn, welcome); err != nil {
			t.Error(err)
			return
		}
		if err = wsjson.Read(r.Context(), conn, &envelope); err != nil {
			t.Error(err)
			return
		}
		received <- envelope
	}))
	defer receiver.Close()
	client := observedTestClient(t, receiver.URL)
	events := make(chan clientEnvelopeEvent, 4)
	client.observeEnvelope = func(event clientEnvelopeEvent) {
		select {
		case events <- event:
		default:
			t.Error("unexpected excess lifecycle events")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	connected, _ := client.connectAndRead(ctx)
	if !connected {
		t.Fatal("legacy handshake failed")
	}
	for i, kind := range []string{protocol.TypeHello, protocol.TypeHeartbeat} {
		select {
		case envelope := <-received:
			if envelope.Type != kind || envelope.Version != 1 || envelope.Sequence != uint64(i+1) {
				t.Fatal("legacy wire changed")
			}
		case <-ctx.Done():
			t.Fatal("missing wire envelope")
		}
	}
	// The heartbeat callback may return after the server has read the frame.
	for i, kind := range []string{protocol.TypeHello, protocol.TypeHeartbeat} {
		for _, phase := range []string{"generated", "write_returned"} {
			select {
			case event := <-events:
				if event.Type != kind || event.Phase != phase || event.Sequence != uint64(i+1) || event.ErrorClass != "" {
					t.Fatal("wrong hello/heartbeat correlation", event)
				}
			case <-ctx.Done():
				t.Fatal("missing hello/heartbeat observation")
			}
		}
	}
}

func TestClientObservationHTTPHelloRetainsContactAndStatus(t *testing.T) {
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var envelope protocol.Envelope
		if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil || r.URL.Path != "/api/v1/agent/hello" || envelope.Type != protocol.TypeHello || envelope.Version != 1 || envelope.Sequence != 1 {
			t.Error("HTTP contact envelope changed", err)
			w.WriteHeader(400)
			return
		}
		w.WriteHeader(204)
	}))
	defer receiver.Close()
	client := observedTestClient(t, receiver.URL)
	var events []clientEnvelopeEvent
	client.observeEnvelope = func(event clientEnvelopeEvent) { events = append(events, event) }
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.sendHelloHTTP(ctx); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Type != protocol.TypeHello || events[0].Phase != "generated" || events[1].Type != protocol.TypeHello || events[1].Phase != "write_returned" || events[1].Sequence != events[0].Sequence || events[1].Transport != "http" || events[1].HTTPStatus != 204 {
		t.Fatal("HTTP hello observation missing", events)
	}
}
