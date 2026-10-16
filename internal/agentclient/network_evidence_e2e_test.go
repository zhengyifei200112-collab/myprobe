package agentclient

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/collector"
	check "github.com/zhengyifei200112-collab/myprobe/internal/protocol/networkcheck"
	v1 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestNetworkProbeThroughActualAgentAndGateway(t *testing.T) {
	type testCase struct {
		name, network, host, family string
		busy                        bool
	}
	cases := []testCase{{"tcp4", "tcp4", "127.0.0.1", check.FamilyIPv4, false}, {"tcp6", "tcp6", "::1", check.FamilyIPv6, false}, {"bounded_busy", "tcp4", "127.0.0.1", check.FamilyUnknown, true}}
	if os.Getenv("MYPROBE_TEST_NETWORK_EVIDENCE_NATIVE") == "1" {
		cases = append(cases, testCase{"icmp4_three_packets", "", "127.0.0.1", check.FamilyIPv4, false}, testCase{"icmp6_three_packets", "", "::1", check.FamilyIPv6, false})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			kind, packets := v1.TaskKindPing, 3
			var port *int
			if tc.network != "" {
				kind, packets = v1.TaskKindTCPing, 0
				listener, err := net.Listen(tc.network, net.JoinHostPort(tc.host, "0"))
				if err != nil {
					if tc.network == "tcp6" && os.Getenv("MYPROBE_TEST_NETWORK_EVIDENCE_NATIVE") != "1" {
						t.Skip("IPv6 loopback listener unavailable:", err)
					}
					t.Fatal(err)
				}
				defer listener.Close()
				go func() {
					for {
						conn, err := listener.Accept()
						if err != nil {
							return
						}
						conn.Close()
					}
				}()
				p := listener.Addr().(*net.TCPAddr).Port
				port = &p
			}
			path := filepath.Join(t.TempDir(), "actual-network.db")
			db, err := store.Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			node, token, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "Synthetic actual network Agent"})
			if err != nil {
				t.Fatal(err)
			}
			requested := tc.family
			if tc.busy {
				requested = check.FamilyAuto
			}
			target, err := db.CreateTarget(ctx, store.CreateTargetParams{Name: "Synthetic loopback target", Kind: kind, Host: tc.host, Port: port, TimeoutMS: 4000, IntervalSeconds: 5, AddressFamily: requested, PacketCount: packets})
			if err != nil {
				t.Fatal(err)
			}
			if err = db.AssignTarget(ctx, node.ID, target.ID); err != nil {
				t.Fatal(err)
			}
			gateway := agentgateway.New(db, agentgateway.NewHub())
			gateway.EnableNetworkProbes(true)
			mux := http.NewServeMux()
			mux.HandleFunc("/api/v2/agent/ws", gateway.WebSocketV2)
			server := httptest.NewServer(mux)
			defer server.Close()
			client, err := New(Config{ServerURL: server.URL, Token: token, AgentVersion: "network-e2e", CollectionPeriod: time.Second, ReportPeriod: time.Second}, collector.New(collector.Config{}), slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err != nil {
				t.Fatal(err)
			}
			if tc.busy {
				for range cap(client.taskSlots) {
					client.taskSlots <- struct{}{}
				}
			}
			agentCtx, stop := context.WithCancel(ctx)
			done := make(chan struct{})
			go func() { client.Run(agentCtx); close(done) }()
			defer func() { stop(); <-done }()
			for !gateway.NodeConnected(node.ID) && ctx.Err() == nil {
				time.Sleep(10 * time.Millisecond)
			}
			if ctx.Err() != nil {
				t.Fatal("Agent did not connect")
			}
			var assignments []store.NetworkAssignment
			for ctx.Err() == nil {
				assignments, err = db.ListNetworkAssignments(ctx, time.Now().UTC())
				if err != nil || len(assignments) == 1 {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err != nil || len(assignments) != 1 {
				t.Fatal("scheduled assignment unavailable", err)
			}
			assignment := assignments[0]
			if err = gateway.DispatchNetwork(ctx, assignment.EpochID, assignment.ScheduledAt); err != nil {
				t.Fatal(err)
			}
			raw, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			var stored string
			for ctx.Err() == nil {
				err = raw.QueryRow(`SELECT COALESCE(result_json,'') FROM network_probe_tasks WHERE epoch_id=? AND scheduled_at=? AND state='completed' AND result_format='network.v1'`, assignment.EpochID, assignment.ScheduledAt.Unix()).Scan(&stored)
				if err == nil {
					break
				}
				if err != sql.ErrNoRows {
					t.Fatal(err)
				}
				time.Sleep(10 * time.Millisecond)
			}
			if stored == "" {
				t.Fatal("actual executor result did not commit", ctx.Err())
			}
			var result check.Result
			if err = json.Unmarshal([]byte(stored), &result); err != nil {
				t.Fatal(err)
			}
			if result.AddressFamily != tc.family || kind == v1.TaskKindTCPing && result.Packets != nil {
				t.Fatal("wrong observed family or TCP packet counts", result)
			}
			if kind == v1.TaskKindPing && (result.Packets == nil || result.Packets.Sent != 3 || result.Packets.Received != 3) {
				t.Fatal("scheduled multi-packet ICMP proof unavailable", result)
			}
			if tc.busy {
				if result.Outcome != check.Unobserved || result.ErrorClass != "busy" || result.RTTMS != nil {
					t.Fatal("busy executor became measured failure", result)
				}
			} else if result.Outcome != check.Success || result.RTTMS == nil {
				t.Fatal("native result unavailable", result)
			}
			// Range counts deliberately mature only after timeout + delivery grace.
			stats, err := db.PublicNetworkQuality(ctx, store.NetworkQualityQuery{NodeID: node.ID, TargetID: target.ID, Kind: kind, Start: assignment.ScheduledAt, End: assignment.ScheduledAt.Add(5 * time.Second), AsOf: assignment.ScheduledAt.Add(30 * time.Second)})
			if err != nil {
				t.Fatal(err)
			}
			if tc.busy {
				if stats.NotExecuted != 1 || stats.Success+stats.Failure != 0 {
					t.Fatal("busy result became measured failure", stats)
				}
			} else if stats.Success != 1 || stats.Totals.Evidence.IPv4+stats.Totals.Evidence.IPv6 != 1 {
				t.Fatal("actual evidence not readable", stats)
			}
			for gateway.Diagnostics().PendingResults != 0 && ctx.Err() == nil {
				time.Sleep(10 * time.Millisecond)
			}
			if gateway.Diagnostics().PendingResults != 0 {
				t.Fatal("committed task remained pending")
			}
		})
	}
}
