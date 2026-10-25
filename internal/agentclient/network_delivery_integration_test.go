package agentclient

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/collector"
	check "github.com/zhengyifei200112-collab/myprobe/internal/protocol/networkcheck"
	v1 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	v2 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v2"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

type networkDeliveryFaultProxy struct {
	mu           sync.Mutex
	mode         string
	first        *v1.Envelope
	attempts     int
	dropped      int
	acknowledged int
	changed      bool
	connections  map[*websocket.Conn]bool
	workers      sync.WaitGroup
	server       *httptest.Server
}

func newNetworkDeliveryFaultProxy(t *testing.T, upstream, mode string) *networkDeliveryFaultProxy {
	t.Helper()
	p := &networkDeliveryFaultProxy{mode: mode, connections: make(map[*websocket.Conn]bool)}
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		legacy := p.mode == "legacy_reconnect" && p.dropped > 0 && r.URL.Path == "/api/v2/agent/ws"
		p.mu.Unlock()
		if legacy {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/agent/ws") {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		remote, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(upstream, "http")+r.URL.Path, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {r.Header.Get("Authorization")}}})
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		defer remote.CloseNow()
		local, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer local.CloseNow()
		p.mu.Lock()
		p.connections[remote], p.connections[local] = true, true
		p.workers.Add(1)
		p.mu.Unlock()
		defer p.workers.Done()
		defer func() {
			p.mu.Lock()
			delete(p.connections, remote)
			delete(p.connections, local)
			p.mu.Unlock()
		}()
		downstreamDone := make(chan struct{})
		go func() {
			defer close(downstreamDone)
			defer cancel()
			for {
				var envelope v1.Envelope
				if wsjson.Read(ctx, local, &envelope) != nil {
					return
				}
				if envelope.Type == v2.TypeNetworkResult {
					p.mu.Lock()
					p.attempts++
					if p.first == nil {
						copy := envelope
						copy.Payload = append([]byte(nil), envelope.Payload...)
						p.first = &copy
					} else if envelope.Sequence != p.first.Sequence || !bytes.Equal(envelope.Payload, p.first.Payload) {
						p.changed = true
					}
					drop := (p.mode == "before_store" || p.mode == "capability_removed" || p.mode == "legacy_reconnect") && p.dropped == 0
					if drop {
						p.dropped++
					}
					p.mu.Unlock()
					if drop {
						return
					}
				}
				if wsjson.Write(ctx, remote, envelope) != nil {
					return
				}
			}
		}()
		defer func() { cancel(); local.CloseNow(); remote.CloseNow(); <-downstreamDone }()
		for {
			var envelope v1.Envelope
			if wsjson.Read(ctx, remote, &envelope) != nil {
				return
			}
			p.mu.Lock()
			removeCapability := p.mode == "capability_removed" && p.dropped > 0 && envelope.Type == v1.TypeWelcome
			p.mu.Unlock()
			if removeCapability {
				welcome, err := v1.DecodePayload[v2.Welcome](envelope)
				if err != nil {
					return
				}
				allowed := welcome.Capabilities[:0]
				for _, capability := range welcome.Capabilities {
					if capability != check.Capability {
						allowed = append(allowed, capability)
					}
				}
				welcome.Capabilities = allowed
				envelope.Payload, err = json.Marshal(welcome)
				if err != nil {
					return
				}
			}
			p.mu.Lock()
			networkAck := envelope.Type == v1.TypeAcknowledged && p.first != nil && envelope.Sequence == p.first.Sequence
			drop := networkAck && (p.mode == "after_store_before_ack" || p.mode == "lost_ack_same_connection") && p.dropped == 0
			if drop {
				p.dropped++
			} else if networkAck {
				p.acknowledged++
			}
			p.mu.Unlock()
			if drop {
				if p.mode == "lost_ack_same_connection" {
					continue
				}
				return
			}
			if wsjson.Write(ctx, local, envelope) != nil {
				return
			}
		}
	}))
	t.Cleanup(func() {
		p.mu.Lock()
		connections := make([]*websocket.Conn, 0, len(p.connections))
		for connection := range p.connections {
			connections = append(connections, connection)
		}
		p.mu.Unlock()
		for _, connection := range connections {
			connection.CloseNow()
		}
		p.server.Close()
		p.workers.Wait()
	})
	return p
}

func TestNetworkResultDeliverySurvivesActualRunReconnect(t *testing.T) {
	for _, mode := range []string{"before_store", "after_store_before_ack", "lost_ack_same_connection", "capability_removed", "legacy_reconnect"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			var executions atomic.Int32
			go func() {
				for {
					connection, err := listener.Accept()
					if err != nil {
						return
					}
					executions.Add(1)
					connection.Close()
				}
			}()
			file := filepath.Join(t.TempDir(), "network-delivery.db")
			database, err := store.Open(ctx, file)
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			node, token, err := database.CreateNode(ctx, store.CreateNodeParams{Name: "Synthetic network delivery"})
			if err != nil {
				t.Fatal(err)
			}
			port := listener.Addr().(*net.TCPAddr).Port
			target, err := database.CreateTarget(ctx, store.CreateTargetParams{Name: "Synthetic delivery target", Kind: v1.TaskKindTCPing, Host: "127.0.0.1", Port: &port, TimeoutMS: 4000, IntervalSeconds: 60, AddressFamily: check.FamilyIPv4})
			if err != nil {
				t.Fatal(err)
			}
			if err := database.AssignTarget(ctx, node.ID, target.ID); err != nil {
				t.Fatal(err)
			}
			gateway := agentgateway.New(database, agentgateway.NewHub())
			gateway.EnableNetworkProbes(true)
			mux := http.NewServeMux()
			mux.HandleFunc("/api/v2/agent/ws", gateway.WebSocketV2)
			mux.HandleFunc("/api/v1/agent/ws", gateway.WebSocket)
			server := httptest.NewServer(mux)
			defer server.Close()
			proxy := newNetworkDeliveryFaultProxy(t, server.URL, mode)
			client, err := New(Config{ServerURL: proxy.server.URL, Token: token, AgentVersion: "network-delivery-test", CollectionPeriod: time.Hour, ReportPeriod: time.Hour}, collector.New(collector.Config{}), slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err != nil {
				t.Fatal(err)
			}
			rejected := make(chan clientEnvelopeEvent, 1)
			client.observeEnvelope = func(event clientEnvelopeEvent) {
				if event.Type == v2.TypeNetworkResult && event.Phase == "local_rejected" {
					rejected <- event
				}
			}
			agentCtx, stop := context.WithCancel(ctx)
			done := make(chan struct{})
			go func() { defer close(done); client.Run(agentCtx) }()
			defer func() { stop(); <-done }()
			for !gateway.NodeConnected(node.ID) && ctx.Err() == nil {
				time.Sleep(5 * time.Millisecond)
			}
			taskID := "synthetic-delivery-" + mode
			task := v1.Task{ID: taskID, TargetID: target.ID, Kind: v1.TaskKindTCPing, Host: "127.0.0.1", Port: port, TimeoutMS: 4000, ExpiresAt: time.Now().UTC().Add(10 * time.Second)}
			if err := gateway.SendTask(ctx, node.ID, task); err != nil {
				t.Fatal(err)
			}
			raw, err := sql.Open("sqlite", file)
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			deadline := time.Now().Add(8 * time.Second)
			wantExpiry := mode == "capability_removed" || mode == "legacy_reconnect"
			if wantExpiry {
				deadline = time.Now().Add(11 * time.Second)
			}
			expired := false
			var attempts, dropped, acknowledged int
			var changed bool
			for time.Now().Before(deadline) {
				proxy.mu.Lock()
				attempts, dropped, acknowledged, changed = proxy.attempts, proxy.dropped, proxy.acknowledged, proxy.changed
				proxy.mu.Unlock()
				if acknowledged > 0 && attempts >= 2 {
					break
				}
				if wantExpiry {
					select {
					case event := <-rejected:
						expired = event.ErrorClass == "expired"
					default:
					}
					if expired {
						break
					}
				}
				time.Sleep(10 * time.Millisecond)
			}
			if wantExpiry {
				connection, version := client.currentConnectionVersion()
				if !expired || attempts != 1 || dropped != 1 || acknowledged != 0 || changed || executions.Load() != 1 || networkPendingCount(client) != 0 || connection == nil || client.networkConnection() != nil || (mode == "legacy_reconnect" && version != 1) {
					t.Fatalf("result crossed negotiation/expiry bounds: expired=%v attempts=%d dropped=%d ack=%d version=%d", expired, attempts, dropped, acknowledged, version)
				}
				var count int
				if err := raw.QueryRowContext(ctx, `SELECT COUNT(*) FROM latency_samples WHERE node_id=? AND target_id=?`, node.ID, target.ID).Scan(&count); err != nil || count != 0 {
					t.Fatal("unnegotiated replay must not create a latency row", count, err)
				}
				return
			}
			if attempts < 2 || dropped != 1 || acknowledged != 1 || changed {
				t.Fatalf("network delivery did not survive actual reconnect unchanged: attempts=%d dropped=%d ack=%d changed=%v", attempts, dropped, acknowledged, changed)
			}
			if executions.Load() != 1 {
				t.Fatalf("delivery retry executed the TCP probe again: executions=%d", executions.Load())
			}
			var count int
			if err := raw.QueryRowContext(ctx, `SELECT COUNT(*) FROM latency_samples WHERE node_id=? AND target_id=?`, node.ID, target.ID).Scan(&count); err != nil || count != 1 {
				t.Fatalf("network replay must commit exactly one latency row, count=%d error=%v", count, err)
			}
			var completed int
			if err := raw.QueryRowContext(ctx, `SELECT COUNT(*) FROM network_probe_tasks WHERE id=? AND state='completed' AND result_format='network.v1'`, taskID).Scan(&completed); err != nil || completed != 1 {
				t.Fatalf("network replay must preserve one completed durable task, count=%d error=%v", completed, err)
			}
			t.Log(fmt.Sprintf("actual Run reconnect: %s attempts=%d acknowledged=%d latency_rows=%d completed_tasks=%d", mode, attempts, acknowledged, count, completed))
		})
	}
}
