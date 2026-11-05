package agentclient

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const jointAPITraceName = "joint-api-requests.jsonl"
const jointAPITraceRecordBytes = 256

// Recorded once per measured request. Node is a one-based fixture ordinal;
// the URL, database node ID, credentials, body, and raw error stay out of the trace.
type jointAPIRequest struct {
	Endpoint   string `json:"endpoint"`
	Node       int    `json:"node,omitempty"`
	StartedNS  int64  `json:"started_ns"`
	HeadersNS  int64  `json:"headers_ns,omitempty"`
	ElapsedNS  int64  `json:"elapsed_ns"`
	Status     int    `json:"status,omitempty"`
	Failure    string `json:"failure,omitempty"`
	ErrorClass string `json:"error_class,omitempty"`
}

type jointAPITrace struct {
	file    *os.File
	writer  *bufio.Writer
	digest  hash.Hash
	summary jointTraceSummary
	closed  bool
}

func newJointAPITrace(dir string, duration time.Duration) (*jointAPITrace, error) {
	if !filepath.IsAbs(dir) || duration <= 0 || duration > 48*time.Hour {
		return nil, errors.New("API trace requires an absolute directory and bounded duration")
	}
	file, err := os.OpenFile(filepath.Join(dir, jointAPITraceName), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	digest := sha256.New()
	// At most four serial requests per one-second tick. Allow the final tick
	// and cap bytes independently of how many responses were successful.
	limit := int64(duration/time.Second+2) * 4 * jointAPITraceRecordBytes
	return &jointAPITrace{
		file: file, writer: bufio.NewWriterSize(io.MultiWriter(file, digest), 32<<10), digest: digest,
		summary: jointTraceSummary{Format: "myprobe.joint-api-requests.v1", File: jointAPITraceName, LimitBytes: limit},
	}, nil
}

func (trace *jointAPITrace) record(request jointAPIRequest) error {
	if trace.closed {
		return errors.New("API trace already closed")
	}
	line, err := json.Marshal(request)
	if err == nil && (len(line)+1 > jointAPITraceRecordBytes || trace.summary.Bytes+int64(len(line)+1) > trace.summary.LimitBytes) {
		err = errors.New("API trace exceeded its size bound")
	}
	if err == nil {
		_, err = trace.writer.Write(append(line, '\n'))
	}
	if err != nil {
		trace.summary.WriteErrors++
		return err
	}
	trace.summary.Events++
	trace.summary.Bytes += int64(len(line) + 1)
	return nil
}

func (trace *jointAPITrace) finish() jointTraceSummary {
	if trace.closed {
		return trace.summary
	}
	trace.closed = true
	if err := errors.Join(trace.writer.Flush(), trace.file.Sync(), trace.file.Close()); err != nil {
		trace.summary.WriteErrors++
	} else {
		trace.summary.SyncedAndClosed = true
		trace.summary.SHA256 = hex.EncodeToString(trace.digest.Sum(nil))
	}
	return trace.summary
}

func TestJointAPITracePreservesRotatedNodeAndFailureTiming(t *testing.T) {
	dir := t.TempDir()
	trace, err := newJointAPITrace(dir, 90*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { trace.finish() })
	samples := []jointAPIRequest{
		{Endpoint: "history_dense_30d", Node: 3, StartedNS: 1e9, HeadersNS: 2e8, ElapsedNS: 4e8, Status: 200},
		{Endpoint: "history_dense_1y", Node: 3, StartedNS: 2e9, HeadersNS: 3e8, ElapsedNS: 5e8, Status: 200},
		{Endpoint: "history_dense_30d", Node: 20, StartedNS: 4e9, ElapsedNS: 4e9, Failure: "transport", ErrorClass: "network_timeout"},
	}
	for _, sample := range samples {
		if err := trace.record(sample); err != nil {
			t.Fatal(err)
		}
	}
	summary := trace.finish()
	raw, err := os.ReadFile(filepath.Join(dir, summary.File))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	if summary.Events != 3 || summary.Bytes != int64(len(raw)) || summary.WriteErrors != 0 || !summary.SyncedAndClosed || summary.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatal("trace count, length or digest does not bind the written bytes", summary)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	for _, want := range samples {
		var got jointAPIRequest
		if err := decoder.Decode(&got); err != nil || got != want {
			t.Fatal("per-request attribution changed", got, err)
		}
	}
	if _, err := newJointAPITrace(dir, 90*time.Second); err == nil {
		t.Fatal("retry could overwrite the previous request evidence")
	}
}

func TestJointAPITraceFailsWhenBoundCannotKeepAllRequests(t *testing.T) {
	trace, err := newJointAPITrace(t.TempDir(), 90*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	trace.summary.LimitBytes = 1
	if err := trace.record(jointAPIRequest{Endpoint: "overview", ElapsedNS: 1, Status: 200}); err == nil {
		t.Fatal("a dropped request could silently pass")
	}
	summary := trace.finish()
	if summary.Events != 0 || summary.Bytes != 0 || summary.WriteErrors != 1 || !summary.SyncedAndClosed {
		t.Fatal("incomplete trace failed to retain its closeout state", summary)
	}
}
