package agentclient

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Test-only evidence; no token, address, response body or full payload is logged.
// Calls are serialized by the observer mutex, including finalization.
type jointEnvelopeEvent struct {
	Event, Node, Type, Transport, CapturedAt, ErrorClass string
	Sequence                                             uint64
	ReportSHA256                                         [32]byte
	PayloadSHA256                                        [32]byte
	HTTPStatus                                           int
}
type jointEnvelopeTrace struct {
	file              *os.File
	writer            *bufio.Writer
	started, lastSync time.Time
	limit, bytes      int64
	events            uint64
	err               error
	closed            bool
	summary           jointTraceSummary
}
type jointTraceSummary struct {
	Format          string `json:"format"`
	File            string `json:"file"`
	Events          uint64 `json:"events"`
	Bytes           int64  `json:"bytes"`
	LimitBytes      int64  `json:"limit_bytes"`
	SHA256          string `json:"sha256"`
	WriteErrors     int    `json:"write_errors"`
	SyncedAndClosed bool   `json:"synced_and_closed"`
}

func newJointEnvelopeTrace(directory string) (*jointEnvelopeTrace, error) {
	if !filepath.IsAbs(directory) {
		return nil, errors.New("absolute trace directory required")
	}
	f, err := os.OpenFile(filepath.Join(directory, "joint-envelope-events.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	return &jointEnvelopeTrace{file: f, writer: bufio.NewWriterSize(f, 64*1024), started: now, lastSync: now, limit: 1024 * 1024 * 1024}, nil
}
func (p *jointReportObserver) traceLocked(event jointEnvelopeEvent) {
	if p.trace != nil {
		p.trace.write(event)
	}
}

func (p *jointReportObserver) observeClient(client *Client, node string) {
	// Called before Client.Run. The Client's callback is never replaced while
	// send goroutines are active; the observer mutex serializes their events.
	client.observeEnvelope = func(event clientEnvelopeEvent) {
		p.mu.Lock()
		defer p.mu.Unlock()
		captured := ""
		if !event.CapturedAt.IsZero() {
			captured = event.CapturedAt.UTC().Format(time.RFC3339Nano)
		}
		p.traceLocked(jointEnvelopeEvent{Event: "client_" + event.Phase, Node: node, Sequence: event.Sequence, Type: event.Type, Transport: event.Transport, ErrorClass: event.ErrorClass, HTTPStatus: event.HTTPStatus, CapturedAt: captured, PayloadSHA256: event.PayloadSHA256})
	}
}

func (p *jointReportObserver) observeServerEnvelope(node string, envelope protocol.Envelope) {
	if envelope.Type != protocol.TypeError {
		return
	}
	class := "decode_error"
	if rejection, err := protocol.DecodePayload[protocol.ProtocolError](envelope); err == nil {
		switch rejection.Code {
		case "storage_error", "invalid_report", "invalid_envelope", "invalid_result", "configuration_ack_rejected":
			class = rejection.Code
		default:
			class = "unclassified"
		}
	}
	// Current Server protocol errors have sequence0. Preserve that original
	// value and timestamp; never pretend it identifies a specific request.
	p.mu.Lock()
	defer p.mu.Unlock()
	p.traceLocked(jointEnvelopeEvent{Event: "server_protocol_error", Node: node, Sequence: envelope.Sequence, Type: envelope.Type, ErrorClass: class})
}
func (trace *jointEnvelopeTrace) write(event jointEnvelopeEvent) {
	if trace.err != nil {
		return
	}
	if trace.closed {
		trace.err = errors.New("trace write after finalization")
		trace.summary.WriteErrors = 1
		trace.summary.SyncedAndClosed = false
		return
	}
	node := sha256.Sum256([]byte(event.Node))
	value := struct {
		At            string `json:"at"`
		ElapsedNS     string `json:"elapsed_ns"`
		Event         string `json:"event"`
		NodeSHA256    string `json:"node_sha256"`
		Sequence      string `json:"sequence"`
		Type          string `json:"type,omitempty"`
		Transport     string `json:"transport,omitempty"`
		CapturedAt    string `json:"captured_at,omitempty"`
		ReportSHA256  string `json:"report_sha256,omitempty"`
		HTTPStatus    int    `json:"http_status,omitempty"`
		ErrorClass    string `json:"error_class,omitempty"`
		PayloadSHA256 string `json:"payload_sha256,omitempty"`
	}{At: time.Now().UTC().Format(time.RFC3339Nano), ElapsedNS: strconv.FormatInt(time.Since(trace.started).Nanoseconds(), 10), Event: event.Event, NodeSHA256: hex.EncodeToString(node[:]), Sequence: strconv.FormatUint(event.Sequence, 10), Type: event.Type, Transport: event.Transport, CapturedAt: event.CapturedAt, HTTPStatus: event.HTTPStatus, ErrorClass: event.ErrorClass}
	if event.ReportSHA256 != ([32]byte{}) {
		value.ReportSHA256 = hex.EncodeToString(event.ReportSHA256[:])
	}
	if event.PayloadSHA256 != ([32]byte{}) {
		value.PayloadSHA256 = hex.EncodeToString(event.PayloadSHA256[:])
	}
	raw, err := json.Marshal(value)
	if err != nil {
		trace.err = err
		return
	}
	raw = append(raw, '\n')
	if int64(len(raw)) > trace.limit-trace.bytes {
		trace.err = errors.New("envelope trace capacity exceeded")
		return
	}
	n, err := trace.writer.Write(raw)
	trace.bytes += int64(n)
	if err != nil {
		trace.err = err
		return
	}
	trace.events++
	// Bound active-traffic buffering by both batch size and time. Explicit close
	// fsyncs the tail; crashes may lose the unflushed interval and cannot pass.
	if trace.events%256 == 0 || time.Since(trace.lastSync) >= 5*time.Second {
		if err = trace.writer.Flush(); err == nil {
			err = trace.file.Sync()
		}
		if err != nil {
			trace.err = err
		}
		trace.lastSync = time.Now()
	}
}
func (p *jointReportObserver) finishTrace() jointTraceSummary {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.trace == nil {
		return jointTraceSummary{}
	}
	return p.trace.finish()
}
func (trace *jointEnvelopeTrace) finish() jointTraceSummary {
	if trace.closed {
		return trace.summary
	}
	if err := trace.writer.Flush(); err != nil && trace.err == nil {
		trace.err = err
	}
	syncOK := trace.file.Sync() == nil
	if !syncOK && trace.err == nil {
		trace.err = errors.New("trace sync failed")
	}
	if err := trace.file.Close(); err != nil && trace.err == nil {
		trace.err = err
	}
	trace.closed = true
	trace.summary = jointTraceSummary{Format: "myprobe.joint-envelope-trace.v2", File: filepath.Base(trace.file.Name()), Events: trace.events, Bytes: trace.bytes, LimitBytes: trace.limit, SyncedAndClosed: syncOK && trace.err == nil}
	f, err := os.Open(trace.file.Name())
	if err == nil {
		h := sha256.New()
		_, err = io.Copy(h, f)
		f.Close()
		if err == nil {
			trace.summary.SHA256 = fmt.Sprintf("%x", h.Sum(nil))
		}
	}
	if err != nil && trace.err == nil {
		trace.err = err
	}
	if trace.err != nil {
		trace.summary.WriteErrors = 1
		trace.summary.SyncedAndClosed = false
	}
	return trace.summary
}
