package agentclient

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/zhengyifei200112-collab/myprobe/internal/collector"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

type fakeMetrics struct{ calls atomic.Int64 }

func (f *fakeMetrics) Collect(context.Context) (protocol.Report, error) {
	f.calls.Add(1)
	return protocol.Report{CapturedAt: time.Now().UTC()}, nil
}
func (f *fakeMetrics) Hello(_ context.Context, version string, collection, report int) (protocol.Hello, error) {
	return protocol.Hello{AgentVersion: version, CollectionSeconds: collection, ReportSeconds: report}, nil
}
func (*fakeMetrics) UpdateConfig(collector.Config) {}
func configClient(t *testing.T, url string, source metricSource) *Client {
	t.Helper()
	c, err := New(Config{ServerURL: url, Token: "test-token", CollectionPeriod: time.Hour, ReportPeriod: time.Hour}, source, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func TestCollectionIntervalChangeInterruptsOldDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		source := &fakeMetrics{}
		c := configClient(t, "http://localhost", source)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go c.collectionLoop(ctx)
		synctest.Wait()
		if source.calls.Load() != 1 {
			t.Fatal("initial collection missing")
		}
		c.applyConfig(protocol.Config{CollectionSeconds: 2, ReportSeconds: 5})
		synctest.Wait()
		time.Sleep(2100 * time.Millisecond)
		synctest.Wait()
		if source.calls.Load() != 2 {
			t.Fatalf("collection count = %d", source.calls.Load())
		}
		c.applyConfig(protocol.Config{CollectionSeconds: 0, ReportSeconds: 3601})
		if c.collectionNS.Load() != int64(2*time.Second) || c.reportNS.Load() != int64(5*time.Second) {
			t.Fatal("invalid intervals changed active configuration")
		}
	})
}
func TestReportingUsesCachedSampleWithoutCollecting(t *testing.T) {
	received := make(chan protocol.Report, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var envelope protocol.Envelope
		if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
			t.Error(err)
			return
		}
		report, err := protocol.DecodePayload[protocol.Report](envelope)
		if err != nil {
			t.Error(err)
			return
		}
		select {
		case received <- report:
		default:
		}
		_ = json.NewEncoder(w).Encode(protocol.Acknowledgement{Sequence: envelope.Sequence})
	}))
	defer server.Close()
	source := &fakeMetrics{}
	c := configClient(t, server.URL, source)
	captured := time.Now().UTC().Add(-time.Minute)
	c.sample = &protocol.Report{CapturedAt: captured}
	c.reportNS.Store(int64(5 * time.Millisecond))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); c.reportLoop(ctx) }()
	defer func() { cancel(); <-done }()
	for range 3 {
		select {
		case report := <-received:
			if !report.CapturedAt.Equal(captured) {
				t.Fatal("cached report timestamp changed")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("report did not arrive")
		}
	}
	if source.calls.Load() != 0 {
		t.Fatal("reporting invoked collection")
	}
}
func TestWelcomeAppliesConfiguration(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(map[bool]string{false: "valid", true: "invalid"}[invalid], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer conn.CloseNow()
				var hello protocol.Envelope
				if err := wsjson.Read(r.Context(), conn, &hello); err != nil {
					t.Error(err)
					return
				}
				welcome, _ := protocol.NewEnvelope(protocol.TypeWelcome, 0, protocol.Welcome{Config: protocol.Config{CollectionSeconds: 2, ReportSeconds: 7}})
				if invalid {
					welcome.Payload = json.RawMessage(`"wrong type"`)
				}
				if err := wsjson.Write(r.Context(), conn, welcome); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			c := configClient(t, server.URL, &fakeMetrics{})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			connected, _ := c.connectAndRead(ctx)
			if connected == invalid {
				t.Fatalf("connected=%v invalid=%v", connected, invalid)
			}
			want := int64(time.Hour)
			if !invalid {
				want = int64(7 * time.Second)
			}
			if c.reportNS.Load() != want {
				t.Fatalf("report interval=%v", c.reportNS.Load())
			}
		})
	}
}
func TestHTTPFallbackOptionalConfiguration(t *testing.T) {
	for _, body := range []string{`{"sequence":1}`, ``, `{"sequence":1,"config":{"collection_seconds":3,"report_seconds":4}}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, body) }))
			defer server.Close()
			c := configClient(t, server.URL, &fakeMetrics{})
			if err := c.sendHelloHTTP(context.Background()); err != nil {
				t.Fatal(err)
			}
			want := int64(time.Hour)
			if len(body) > 20 {
				want = int64(4 * time.Second)
			}
			if c.reportNS.Load() != want {
				t.Fatal("HTTP config not applied or old response changed interval")
			}
		})
	}
}
