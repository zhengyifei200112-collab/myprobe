package agentclient

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/collector"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

type samplingFixtureSource struct {
	calls   atomic.Int64
	updates atomic.Int64
	block   chan struct{}
	started chan struct{}
	once    sync.Once
}

func (f *samplingFixtureSource) Hello(context.Context, string, int, int) (protocol.Hello, error) {
	return protocol.Hello{}, nil
}
func (f *samplingFixtureSource) UpdateConfig(collector.Config) { f.updates.Add(1) }
func (f *samplingFixtureSource) Collect(ctx context.Context) (protocol.Report, error) {
	number := f.calls.Add(1)
	if number == 1 && f.block != nil {
		f.once.Do(func() { close(f.started) })
		select {
		case <-ctx.Done():
			return protocol.Report{}, ctx.Err()
		case <-f.block:
		}
	}
	return protocol.Report{CapturedAt: time.Now().UTC(), Uptime: uint64(number)}, nil
}
func newSamplingFixtureClient(t *testing.T, base string, collection, report time.Duration) *Client {
	t.Helper()
	client, err := New(Config{ServerURL: base, Token: "synthetic-token", CollectionPeriod: collection, ReportPeriod: report}, collector.New(collector.Config{}), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return client
}
func TestSamplingCollectionRunsIndependentlyOfReportPeriod(t *testing.T) {
	source := &samplingFixtureSource{}
	client := newSamplingFixtureClient(t, "http://127.0.0.1", time.Second, 10*time.Second)
	client.collector = source
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { client.collectionLoop(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	// Collect increments calls before returning. Wait for publication instead:
	// entering the third call does not mean collectionLoop has cached its result.
	for deadline := time.Now().Add(4 * time.Second); time.Now().Before(deadline); {
		if sample, ok := client.currentSample(); ok && sample.report.Uptime >= 3 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("three collections were not published before the ten-second report period (calls=%d)", source.calls.Load())
}
func TestSamplingChangeDiscardsCollectionStartedUnderOldGeneration(t *testing.T) {
	source := &samplingFixtureSource{block: make(chan struct{}), started: make(chan struct{})}
	client := newSamplingFixtureClient(t, "http://127.0.0.1", time.Second, time.Second)
	client.collector = source
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { client.collectionLoop(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	<-source.started
	if err := client.applySamplingPeriods(2, 3); err != nil {
		t.Fatal(err)
	}
	if _, ok := client.currentSample(); ok {
		t.Fatal("old generation remained reportable")
	}
	close(source.block)
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		if sample, ok := client.currentSample(); ok {
			if sample.report.Uptime == 1 {
				t.Fatal("old in-flight collection became the new generation")
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("managed change did not wake collection")
}
func TestReportPeriodDoesNotRepeatTheSameCachedCollection(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(200) }))
	defer server.Close()
	client := newSamplingFixtureClient(t, server.URL, time.Second, time.Second)
	client.sample = &collectedSample{id: 1, report: protocol.Report{CapturedAt: time.Now().UTC()}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { client.reportLoop(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline) && requests.Load() < 1; {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(1100 * time.Millisecond)
	if requests.Load() != 1 {
		t.Fatalf("unchanged collection repeated: %d", requests.Load())
	}
}
func TestLegacySamplingOmissionRetainsLocalSelection(t *testing.T) {
	client := newSamplingFixtureClient(t, "http://127.0.0.1", time.Second, 2*time.Second)
	source := &samplingFixtureSource{}
	client.collector = source
	client.applyConfig(protocol.Config{})
	if client.collectionNS.Load() != int64(time.Second) || client.reportNS.Load() != int64(2*time.Second) || source.updates.Load() != 0 {
		t.Fatal("omitted legacy fields changed local settings")
	}
	client.applyConfig(protocol.Config{CollectionSeconds: 3, ReportSeconds: 4})
	if client.collectionNS.Load() != int64(3*time.Second) || client.reportNS.Load() != int64(4*time.Second) || source.updates.Load() != 0 {
		t.Fatal("period-only config cleared local selection")
	}
	client.applyConfig(protocol.Config{CollectionSeconds: 0, ReportSeconds: 4000})
	if client.reportNS.Load() != int64(4*time.Second) {
		t.Fatal("invalid legacy period partially applied")
	}
}

func TestLegacySamplingPartialOmissionPreservesFractionalDuration(t *testing.T) {
	client := newSamplingFixtureClient(t, "http://127.0.0.1", 1500*time.Millisecond, 2500*time.Millisecond)
	if err := client.applyLegacySampling(protocol.Config{ReportSeconds: 3}); err != nil {
		t.Fatal(err)
	}
	if client.collectionNS.Load() != int64(1500*time.Millisecond) || client.reportNS.Load() != int64(3*time.Second) {
		t.Fatal("partial legacy message rounded an omitted local duration")
	}
}
