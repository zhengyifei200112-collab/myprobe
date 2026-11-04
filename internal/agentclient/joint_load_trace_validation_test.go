package agentclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

func observerTestEnvelope(t *testing.T, sequence uint64, cpu float64) protocol.Envelope {
	t.Helper()
	envelope, err := protocol.NewEnvelope(protocol.TypeReport, sequence, protocol.Report{CapturedAt: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC), CPU: protocol.CPUMetric{UsagePercent: cpu}, PublicIP: "198.51.100.99"})
	if err != nil {
		t.Fatal(err)
	}
	return envelope
}

func TestReportObserverRepeatedEnvelopePreservesFirstACK(t *testing.T) {
	p := &jointReportObserver{attempts: map[jointAttemptKey]*jointReportAttempt{}}
	envelope := observerTestEnvelope(t, 7, 1)
	p.record("node", "websocket", envelope)
	p.forwarded("node", 7)
	p.ack("node", 7)
	first := *p.attempts[jointAttemptKey{"node", 7}]
	p.record("node", "http", envelope)
	p.interrupt("node", []uint64{7})
	p.ack("node", 7)
	actual := p.attempts[jointAttemptKey{"node", 7}]
	if !actual.acknowledged || !actual.forwarded || actual.sentAt != first.sentAt || actual.ackAt != first.ackAt || !actual.interruptedAt.IsZero() {
		t.Fatal("fallback replaced the first attempt or receipt")
	}
	result := p.reconcile(map[string]map[string][32]byte{"node": {first.captured: first.hash}}, 0)
	if result.ReportAttempts != 1 || result.Acknowledged != 1 || result.Stored != 1 || result.StoredWithoutACK != 0 || result.ConflictingReplays != 0 {
		t.Fatalf("replayed receipt miscounted: %+v", result)
	}
}

func TestReportObserverConflictingSequenceFailsWithoutReplacingReceipt(t *testing.T) {
	p := &jointReportObserver{attempts: map[jointAttemptKey]*jointReportAttempt{}}
	p.record("node", "websocket", observerTestEnvelope(t, 7, 1))
	p.ack("node", 7)
	first := *p.attempts[jointAttemptKey{"node", 7}]
	p.record("node", "http", observerTestEnvelope(t, 7, 2))
	if actual := p.attempts[jointAttemptKey{"node", 7}]; actual.hash != first.hash || actual.ackAt != first.ackAt {
		t.Fatal("conflict replaced original evidence")
	}
	result := p.reconcile(map[string]map[string][32]byte{"node": {first.captured: first.hash}}, 0)
	if result.ConflictingReplays != 1 {
		t.Fatal("conflicting replay not reported")
	}
}

func TestReportObserverHTTPRepeatAndPrivateTrace(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var envelope protocol.Envelope
		if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
			t.Error(err)
			return
		}
		json.NewEncoder(w).Encode(protocol.Acknowledgement{Sequence: envelope.Sequence})
	}))
	defer upstream.Close()
	p := newJointReportObserver(upstream.URL)
	trace, err := newJointEnvelopeTrace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p.trace = trace
	defer func() { p.close(); p.finishTrace() }()
	const token = "fixture-token-must-never-appear"
	const node = "fixture-node-identity-must-be-hashed"
	p.register(token, node)
	envelope := observerTestEnvelope(t, 7, 17.125)
	body, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		request, err := http.NewRequest("POST", p.server.URL+"/api/v1/agent/report", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+token)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatalf("status=%d", response.StatusCode)
		}
	}
	p.close()
	p.mu.Lock()
	attempt := *p.attempts[jointAttemptKey{node, 7}]
	p.mu.Unlock()
	result := p.reconcile(map[string]map[string][32]byte{node: {attempt.captured: attempt.hash}}, 0)
	p.sequenceAccounting(&result, map[string]uint64{node: 8})
	summary := p.finishTrace()
	if !summary.SyncedAndClosed || summary.WriteErrors != 0 || summary.Events == 0 || result.Acknowledged != 1 || result.ReportAttempts != 1 {
		t.Fatalf("incomplete evidence: %+v %+v", summary, result)
	}
	raw, err := os.ReadFile(trace.file.Name())
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(raw)
	if summary.SHA256 != hex.EncodeToString(hash[:]) || summary.Bytes != int64(len(raw)) {
		t.Fatal("trace digest/size mismatch")
	}
	for _, secret := range []string{token, node, "198.51.100.99", "17.125", "report_json", "Authorization"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("trace leaked %q", secret)
		}
	}
	events := map[string]int{}
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte{'\n'}) {
		var value map[string]any
		if err = json.Unmarshal(line, &value); err != nil {
			t.Fatal(err)
		}
		events[value["event"].(string)]++
		if _, ok := value["sequence"].(string); !ok {
			t.Fatal("sequence must preserve integer precision")
		}
	}
	if events["observed"] != 2 || events["report"] != 1 || events["ack"] != 2 || events["http_status"] != 2 || events["sequence_gap"] != 7 {
		t.Fatalf("trace omitted state transitions: %v", events)
	}
	if got := p.finishTrace(); got != summary {
		t.Fatal("trace finalization not idempotent")
	}
}

func TestReportObserverWebSocketThenHTTPPreservesReceipt(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/ws") {
			connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
			if err != nil {
				return
			}
			defer connection.CloseNow()
			for {
				var envelope protocol.Envelope
				if err = wsjson.Read(r.Context(), connection, &envelope); err != nil {
					return
				}
				ack, err := protocol.NewEnvelope(protocol.TypeAcknowledged, envelope.Sequence, protocol.Acknowledgement{Sequence: envelope.Sequence})
				if err != nil {
					return
				}
				if wsjson.Write(r.Context(), connection, ack) != nil {
					return
				}
			}
		}
		var envelope protocol.Envelope
		if json.NewDecoder(r.Body).Decode(&envelope) != nil {
			w.WriteHeader(400)
			return
		}
		json.NewEncoder(w).Encode(protocol.Acknowledgement{Sequence: envelope.Sequence})
	}))
	defer upstream.Close()
	p := newJointReportObserver(upstream.URL)
	defer p.close()
	p.register("owned-token", "node")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(p.server.URL, "http")+"/api/v1/agent/ws", &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer owned-token"}}, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	envelope := observerTestEnvelope(t, 9, 1)
	if err = wsjson.Write(ctx, connection, envelope); err != nil {
		t.Fatal(err)
	}
	var ack protocol.Envelope
	if err = wsjson.Read(ctx, connection, &ack); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	first := *p.attempts[jointAttemptKey{"node", 9}]
	p.mu.Unlock()
	if !first.acknowledged {
		t.Fatal("original WS receipt missing")
	}
	connection.CloseNow()
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(ctx, "POST", p.server.URL+"/api/v1/agent/report", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer owned-token")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("fallback status=%d", response.StatusCode)
	}
	p.close()
	p.mu.Lock()
	after := *p.attempts[jointAttemptKey{"node", 9}]
	p.mu.Unlock()
	if !after.acknowledged || after.ackAt != first.ackAt || after.sentAt != first.sentAt {
		t.Fatal("actual transport fallback changed first receipt")
	}
}

func TestEnvelopeTraceLimitAndIOFailureCannotPass(t *testing.T) {
	for _, kind := range []string{"capacity", "closed-file"} {
		t.Run(kind, func(t *testing.T) {
			trace, err := newJointEnvelopeTrace(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if kind == "capacity" {
				trace.limit = 1
			} else {
				trace.file.Close()
			}
			trace.write(jointEnvelopeEvent{Event: "observed", Node: "node", Sequence: 1})
			summary := trace.finish()
			if summary.WriteErrors != 1 || summary.SyncedAndClosed {
				t.Fatal("trace failure accepted")
			}
		})
	}
}

func TestEnvelopeTraceConcurrentObserverEventsAreComplete(t *testing.T) {
	trace, err := newJointEnvelopeTrace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p := &jointReportObserver{attempts: map[jointAttemptKey]*jointReportAttempt{}, trace: trace}
	var workers sync.WaitGroup
	for i := 0; i < 32; i++ {
		envelope := observerTestEnvelope(t, uint64(i+1), float64(i))
		workers.Add(1)
		go func() {
			defer workers.Done()
			p.record("node", "websocket", envelope)
			p.forwarded("node", envelope.Sequence)
			p.ack("node", envelope.Sequence)
		}()
	}
	workers.Wait()
	summary := p.finishTrace()
	if summary.Events != 128 || summary.WriteErrors != 0 {
		t.Fatalf("lost concurrent events: %+v", summary)
	}
}

func TestEnvelopeTraceRefusesExistingFile(t *testing.T) {
	directory := t.TempDir()
	file := filepath.Join(directory, "joint-envelope-events.jsonl")
	if err := os.WriteFile(file, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := newJointEnvelopeTrace(directory); err == nil {
		t.Fatal("trace overwrote existing evidence")
	}
	raw, err := os.ReadFile(file)
	if err != nil || string(raw) != "keep" {
		t.Fatal("existing evidence changed")
	}
}
