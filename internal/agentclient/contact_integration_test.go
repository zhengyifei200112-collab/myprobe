package agentclient

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/collector"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestActualAgentContactWithLongCollectionAndHTTPFallback(t *testing.T) {
	for _, transport := range []string{"websocket-v1", "websocket-v2", "http-blocked-dial"} {
		t.Run(transport, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			db, err := store.Open(ctx, filepath.Join(httpFixtureDir(t), "contact.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			node, token, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "synthetic slow collector", CollectionSeconds: 3600, ReportSeconds: 1})
			if err != nil {
				t.Fatal(err)
			}
			gateway := agentgateway.New(db, agentgateway.NewHub())
			mux := http.NewServeMux()
			var hellos atomic.Int64
			mux.HandleFunc("/api/v1/agent/hello", func(w http.ResponseWriter, r *http.Request) { hellos.Add(1); gateway.HTTPHello(w, r) })
			mux.HandleFunc("/api/v1/agent/report", gateway.HTTPReport)
			switch transport {
			case "websocket-v1":
				mux.HandleFunc("/api/v1/agent/ws", gateway.WebSocket)
			case "websocket-v2":
				mux.HandleFunc("/api/v2/agent/ws", gateway.WebSocketV2)
			case "http-blocked-dial":
				// Keep the connection attempt pending longer than this test. The
				// retry loop cannot supply HTTP hellos: only the independent contact
				// worker can keep an HTTP-only Agent alive during the stalled dial.
				mux.HandleFunc("/api/v2/agent/ws", func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
			}
			server := httptest.NewServer(mux)
			defer server.Close()
			client, err := New(Config{ServerURL: server.URL, Token: token, AgentVersion: "contact-fixture", CollectionPeriod: time.Hour, ReportPeriod: time.Second}, collector.New(collector.Config{}), slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err != nil {
				t.Fatal(err)
			}
			source := &samplingFixtureSource{}
			client.collector = source
			agentCtx, stopAgent := context.WithCancel(ctx)
			done := make(chan struct{})
			go func() { client.Run(agentCtx); close(done) }()
			defer func() { stopAgent(); <-done }()
			var ingress store.ReportIngress
			for ctx.Err() == nil {
				ingress, err = db.LastReportIngress(ctx, node.ID)
				if err != nil {
					t.Fatal(err)
				}
				if ingress.Status == "observed" {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if ctx.Err() != nil {
				t.Fatal("initial sample was not admitted")
			}
			deadline := time.Now().Add(8 * time.Second)
			var refreshed bool
			for time.Now().Before(deadline) {
				item, err := db.PublicNode(ctx, node.ID, time.Now().UTC())
				if err != nil {
					t.Fatal(err)
				}
				if item.Node.LastSeenAt != nil && item.Node.LastSeenAt.After(ingress.ObservedAt.Add(time.Second)) {
					if !item.Online || item.Stale || !item.Report.CapturedAt.Equal(*ingress.CapturedAt) {
						t.Fatal("slow collector contact/freshness mismatch")
					}
					refreshed = true
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !refreshed {
				t.Fatal("contact did not advance independently of the hour-long collection")
			}
			after, err := db.LastReportIngress(ctx, node.ID)
			if err != nil || !reflect.DeepEqual(ingress, after) || source.calls.Load() != 1 {
				t.Fatal("contact fabricated/repeated metric ingress or collection", err)
			}
			if transport == "http-blocked-dial" {
				if hellos.Load() < 1 || ingress.Transport != "http" {
					t.Fatal("blocked dial prevented independent HTTP contact")
				}
			} else {
				_, version := client.currentConnectionVersion()
				want := 1
				if transport == "websocket-v2" {
					want = 2
				}
				if version != want || ingress.Transport != "websocket" || hellos.Load() != 0 {
					t.Fatal("socket contact used an unexpected fallback")
				}
			}
		})
	}
}
