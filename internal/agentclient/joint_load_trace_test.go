package agentclient

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Test-only evidence; no token, address, response body or full payload is logged.
// Calls are serialized by the observer mutex, including finalization.
type jointEnvelopeEvent struct {
	Event, Node, Type, Transport, CapturedAt string
	Sequence                                 uint64
	ReportSHA256                             [32]byte
	HTTPStatus                               int
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
	return &jointEnvelopeTrace{file: f, writer: bufio.NewWriterSize(f, 64*1024), started: now, lastSync: now, limit: 512 * 1024 * 1024}, nil
}
func (p *jointReportObserver) traceLocked(event jointEnvelopeEvent) {
	if p.trace != nil {
		p.trace.write(event)
	}
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
		At           string `json:"at"`
		ElapsedNS    string `json:"elapsed_ns"`
		Event        string `json:"event"`
		NodeSHA256   string `json:"node_sha256"`
		Sequence     string `json:"sequence"`
		Type         string `json:"type,omitempty"`
		Transport    string `json:"transport,omitempty"`
		CapturedAt   string `json:"captured_at,omitempty"`
		ReportSHA256 string `json:"report_sha256,omitempty"`
		HTTPStatus   int    `json:"http_status,omitempty"`
	}{At: time.Now().UTC().Format(time.RFC3339Nano), ElapsedNS: strconv.FormatInt(time.Since(trace.started).Nanoseconds(), 10), Event: event.Event, NodeSHA256: hex.EncodeToString(node[:]), Sequence: strconv.FormatUint(event.Sequence, 10), Type: event.Type, Transport: event.Transport, CapturedAt: event.CapturedAt, HTTPStatus: event.HTTPStatus}
	if event.ReportSHA256 != ([32]byte{}) {
		value.ReportSHA256 = hex.EncodeToString(event.ReportSHA256[:])
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
	trace.summary = jointTraceSummary{Format: "myprobe.joint-envelope-trace.v1", File: filepath.Base(trace.file.Name()), Events: trace.events, Bytes: trace.bytes, LimitBytes: trace.limit, SyncedAndClosed: syncOK && trace.err == nil}
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
