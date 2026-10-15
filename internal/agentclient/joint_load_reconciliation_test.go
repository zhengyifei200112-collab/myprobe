package agentclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

// The observer terminates/re-encodes test connections, but forwards original
// envelopes without changing version, sequence, payload or negotiated config.
// It runs outside the Server cgroup; results must disclose this additional hop.
type jointReportObserver struct {
	server      *httptest.Server
	upstream    string
	client      *http.Client
	mu          sync.Mutex
	nodes       map[string]string // generated fixture tokens stay in memory only
	attempts    map[jointAttemptKey]*jointReportAttempt
	sequences   map[string]map[uint64]bool
	connections map[*websocket.Conn]bool
	workers     sync.WaitGroup
	closing     bool
	closeOnce   sync.Once
}
type jointAttemptKey struct {
	node     string
	sequence uint64
}
type jointReportAttempt struct {
	node, captured, transport string
	sequence                  uint64
	hash                      [32]byte
	sentAt                    time.Time
	ackAt                     time.Time
	forwarded, acknowledged   bool
	interruptedAt             time.Time
	HTTPStatus                int
}
type jointReportAccounting struct {
	Mode                                                                                                       string
	ReportAttempts, Forwarded, Acknowledged, Stored, StoredWithoutACK                                          int
	InterruptedNotStored, UnexplainedNotStored, ACKWithoutStored, UnknownStored, HashMismatch, DuplicateStored int
	MaxACKDelayMilliseconds                                                                                    float64
	GeneratedEnvelopes, ObservedEnvelopes, UnobservedGeneratedEnvelopes                                        int
}

func newJointReportObserver(upstream string) *jointReportObserver {
	p := &jointReportObserver{upstream: upstream, client: &http.Client{Timeout: 15 * time.Second}, nodes: make(map[string]string), attempts: make(map[jointAttemptKey]*jointReportAttempt), sequences: make(map[string]map[uint64]bool), connections: make(map[*websocket.Conn]bool)}
	p.server = httptest.NewServer(http.HandlerFunc(p.serveHTTP))
	return p
}
func (p *jointReportObserver) register(token, node string) {
	p.mu.Lock()
	p.nodes[token] = node
	p.mu.Unlock()
}
func (p *jointReportObserver) close() {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closing = true
		connections := make([]*websocket.Conn, 0, len(p.connections))
		for conn := range p.connections {
			connections = append(connections, conn)
		}
		p.mu.Unlock()
		for _, conn := range connections {
			conn.CloseNow()
		}
		p.server.Close()
		p.workers.Wait()
	})
}
func (p *jointReportObserver) record(node, transport string, envelope protocol.Envelope) {
	p.mu.Lock()
	if p.sequences == nil {
		p.sequences = make(map[string]map[uint64]bool)
	}
	if p.sequences[node] == nil {
		p.sequences[node] = make(map[uint64]bool)
	}
	p.sequences[node][envelope.Sequence] = true
	p.mu.Unlock()
	if envelope.Type != protocol.TypeReport {
		return
	}
	report, err := protocol.DecodePayload[protocol.Report](envelope)
	if err != nil {
		return
	}
	report.PublicIP = ""
	raw, err := json.Marshal(report)
	if err != nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.attempts[jointAttemptKey{node, envelope.Sequence}] = &jointReportAttempt{node: node, sequence: envelope.Sequence, captured: report.CapturedAt.UTC().Format(time.RFC3339Nano), transport: transport, hash: sha256.Sum256(raw), sentAt: time.Now()}
}

func (p *jointReportObserver) sequenceAccounting(result *jointReportAccounting, generated map[string]uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for node, maximum := range generated {
		result.GeneratedEnvelopes += int(maximum)
		for sequence := uint64(1); sequence <= maximum; sequence++ {
			if p.sequences[node][sequence] {
				result.ObservedEnvelopes++
			} else {
				result.UnobservedGeneratedEnvelopes++
			}
		}
	}
}
func (p *jointReportObserver) forwarded(node string, sequence uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if attempt := p.attempts[jointAttemptKey{node, sequence}]; attempt != nil {
		attempt.forwarded = true
	}
}
func (p *jointReportObserver) ack(node string, sequence uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if attempt := p.attempts[jointAttemptKey{node, sequence}]; attempt != nil {
		attempt.acknowledged = true
		if attempt.ackAt.IsZero() {
			attempt.ackAt = time.Now()
		}
	}
}
func (p *jointReportObserver) interrupt(node string, sequences []uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for _, sequence := range sequences {
		if attempt := p.attempts[jointAttemptKey{node, sequence}]; attempt != nil && !attempt.acknowledged && attempt.interruptedAt.IsZero() {
			attempt.interruptedAt = now
		}
	}
}
func (p *jointReportObserver) serveHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	node := p.nodes[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
	p.mu.Unlock()
	if node == "" {
		w.WriteHeader(401)
		return
	}
	if r.URL.Path == "/api/v1/agent/ws" || r.URL.Path == "/api/v2/agent/ws" {
		p.serveSocket(w, r, node)
		return
	}
	if r.Method != "POST" || (r.URL.Path != "/api/v1/agent/report" && r.URL.Path != "/api/v1/agent/hello") {
		w.WriteHeader(404)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, protocol.MaxMessageBytes+1))
	if err != nil || len(raw) > protocol.MaxMessageBytes {
		w.WriteHeader(400)
		return
	}
	var envelope protocol.Envelope
	if json.Unmarshal(raw, &envelope) != nil {
		w.WriteHeader(400)
		return
	}
	p.record(node, "http", envelope)
	req, err := http.NewRequestWithContext(r.Context(), "POST", p.upstream+r.URL.Path, bytes.NewReader(raw))
	if err != nil {
		w.WriteHeader(502)
		return
	}
	req.Header.Set("Authorization", r.Header.Get("Authorization"))
	req.Header.Set("Content-Type", "application/json")
	response, err := p.client.Do(req)
	if err != nil {
		p.interrupt(node, []uint64{envelope.Sequence})
		w.WriteHeader(502)
		return
	}
	defer response.Body.Close()
	p.forwarded(node, envelope.Sequence)
	body, err := io.ReadAll(io.LimitReader(response.Body, protocol.MaxMessageBytes+1))
	if err != nil {
		p.interrupt(node, []uint64{envelope.Sequence})
		w.WriteHeader(502)
		return
	}
	p.mu.Lock()
	if attempt := p.attempts[jointAttemptKey{node, envelope.Sequence}]; attempt != nil {
		attempt.HTTPStatus = response.StatusCode
	}
	p.mu.Unlock()
	if response.StatusCode == 200 && envelope.Type == protocol.TypeReport {
		var ack protocol.Acknowledgement
		if json.Unmarshal(body, &ack) == nil && ack.Sequence == envelope.Sequence {
			p.ack(node, ack.Sequence)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(body)
}
func (p *jointReportObserver) serveSocket(w http.ResponseWriter, r *http.Request, node string) {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	upstream, response, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(p.upstream, "http")+r.URL.Path, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {r.Header.Get("Authorization")}}, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		if response != nil {
			w.WriteHeader(response.StatusCode)
		} else {
			w.WriteHeader(502)
		}
		return
	}
	downstream, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		upstream.CloseNow()
		return
	}
	upstream.SetReadLimit(protocol.MaxMessageBytes)
	downstream.SetReadLimit(protocol.MaxMessageBytes)
	p.mu.Lock()
	if p.closing {
		p.mu.Unlock()
		upstream.CloseNow()
		downstream.CloseNow()
		return
	}
	p.connections[upstream], p.connections[downstream] = true, true
	p.workers.Add(1)
	p.mu.Unlock()
	defer p.workers.Done()
	defer func() {
		upstream.CloseNow()
		downstream.CloseNow()
		p.mu.Lock()
		delete(p.connections, upstream)
		delete(p.connections, downstream)
		p.mu.Unlock()
	}()
	done := make(chan struct{}, 1)
	var sequences []uint64
	var sequenceMu sync.Mutex
	go func() {
		defer cancel()
		defer func() { done <- struct{}{} }()
		for ctx.Err() == nil {
			var envelope protocol.Envelope
			if wsjson.Read(ctx, downstream, &envelope) != nil {
				return
			}
			p.record(node, "websocket", envelope)
			if envelope.Type == protocol.TypeReport {
				sequenceMu.Lock()
				sequences = append(sequences, envelope.Sequence)
				sequenceMu.Unlock()
			}
			if wsjson.Write(ctx, upstream, envelope) != nil {
				return
			}
			p.forwarded(node, envelope.Sequence)
		}
	}()
	for ctx.Err() == nil {
		var envelope protocol.Envelope
		if wsjson.Read(ctx, upstream, &envelope) != nil {
			break
		}
		if envelope.Type == protocol.TypeAcknowledged {
			ack, err := protocol.DecodePayload[protocol.Acknowledgement](envelope)
			if err == nil && ack.Sequence == envelope.Sequence {
				p.ack(node, ack.Sequence)
			}
		}
		if wsjson.Write(ctx, downstream, envelope) != nil {
			break
		}
	}
	cancel()
	upstream.CloseNow()
	downstream.CloseNow()
	<-done
	sequenceMu.Lock()
	p.interrupt(node, sequences)
	sequenceMu.Unlock()
}

// Stored data are checked after the real Server stops. A connection closing
// much later cannot excuse a silently discarded report earlier in its lifetime.
func (p *jointReportObserver) reconcile(stored map[string]map[string][32]byte, duplicates int) jointReportAccounting {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := jointReportAccounting{Mode: "test-observer-http-ws-ack-store", DuplicateStored: duplicates}
	known := make(map[string]map[string]bool)
	for _, attempt := range p.attempts {
		result.ReportAttempts++
		if attempt.forwarded {
			result.Forwarded++
		}
		if attempt.acknowledged {
			result.Acknowledged++
			delay := float64(attempt.ackAt.Sub(attempt.sentAt)) / float64(time.Millisecond)
			if delay > result.MaxACKDelayMilliseconds {
				result.MaxACKDelayMilliseconds = delay
			}
		}
		hash, present := stored[attempt.node][attempt.captured]
		if known[attempt.node] == nil {
			known[attempt.node] = make(map[string]bool)
		}
		known[attempt.node][attempt.captured] = true
		if present {
			if hash != attempt.hash {
				result.HashMismatch++
			}
			if !attempt.acknowledged {
				result.StoredWithoutACK++
			}
		} else if attempt.acknowledged {
			result.ACKWithoutStored++
		} else if !attempt.interruptedAt.IsZero() && attempt.interruptedAt.Sub(attempt.sentAt) <= 10*time.Second {
			result.InterruptedNotStored++
		} else {
			result.UnexplainedNotStored++
		}
	}
	for node, reports := range stored {
		for captured := range reports {
			result.Stored++
			if !known[node][captured] {
				result.UnknownStored++
			}
		}
	}
	return result
}
