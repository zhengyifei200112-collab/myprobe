package agentgateway

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	check "github.com/zhengyifei200112-collab/myprobe/internal/protocol/networkcheck"
	v1 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	v2 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v2"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestNetworkProbeSocketNegotiationAndDurableACK(t *testing.T) {
	for _, tc := range []struct {
		name                string
		version             int
		enabled, advertised bool
	}{{"mutual", 2, true, true}, {"server_disabled", 2, false, true}, {"agent_legacy", 2, true, false}, {"v1_isolated", 1, true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			path := filepath.Join(t.TempDir(), "evidence.db")
			db, err := store.Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			node, token, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "Synthetic evidence socket"})
			if err != nil {
				t.Fatal(err)
			}
			target, err := db.CreateTarget(ctx, store.CreateTargetParams{Name: "Synthetic evidence target", Kind: v1.TaskKindPing, Host: "example.test", TimeoutMS: 1000})
			if err != nil {
				t.Fatal(err)
			}
			if err = db.AssignTarget(ctx, node.ID, target.ID); err != nil {
				t.Fatal(err)
			}
			g := New(db, NewHub())
			g.EnableNetworkProbes(tc.enabled)
			handler := g.WebSocketV2
			if tc.version == 1 {
				handler = g.WebSocket
			}
			server := httptest.NewServer(http.HandlerFunc(handler))
			defer server.Close()
			conn, _, err := websocket.Dial(ctx, strings.Replace(server.URL, "http", "ws", 1), &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + token}}})
			if err != nil {
				t.Fatal(err)
			}
			defer conn.CloseNow()
			caps := []string{}
			if tc.advertised {
				caps = append(caps, check.Capability)
			}
			hello, _ := v1.NewEnvelope(v1.TypeHello, 1, v1.Hello{AgentVersion: "synthetic", OS: "linux", Architecture: "amd64", Capabilities: caps})
			hello.Version = tc.version
			if err = wsjson.Write(ctx, conn, hello); err != nil {
				t.Fatal(err)
			}
			var reply v1.Envelope
			if err = wsjson.Read(ctx, conn, &reply); err != nil {
				t.Fatal(err)
			}
			mutual := tc.version == 2 && tc.enabled && tc.advertised
			if tc.version == 2 {
				welcome, err := v1.DecodePayload[v2.Welcome](reply)
				if err != nil || v2.AllowsNetwork(welcome.Capabilities) != mutual {
					t.Fatal("capability not mutual", err)
				}
			}
			task := v1.Task{ID: "synthetic-wire-probe", TargetID: target.ID, Kind: target.Kind, Host: target.Host, TimeoutMS: 1000, ExpiresAt: time.Now().UTC().Add(time.Minute)}
			if err = g.SendTask(ctx, node.ID, task); err != nil {
				t.Fatal(err)
			}
			if err = wsjson.Read(ctx, conn, &reply); err != nil {
				t.Fatal(err)
			}
			wantType := v1.TypeTask
			if mutual {
				wantType = v2.TypeNetworkTask
			}
			if reply.Type != wantType || reply.Version != tc.version {
				t.Fatal("wrong dispatch format", reply.Type, reply.Version)
			}
			rtt := 10.0
			r := check.Result{TaskID: task.ID, TargetID: target.ID, Kind: task.Kind, Outcome: check.Success, AddressFamily: check.FamilyIPv6, RTTMS: &rtt, Packets: &check.PacketCounts{Sent: 1, Received: 1}, CompletedAt: time.Now().UTC()}
			write := func(seq uint64, value check.Result) v1.Envelope {
				t.Helper()
				frame, _ := v2.NewEnvelope(v2.TypeNetworkResult, seq, value)
				frame.Version = tc.version
				if err := wsjson.Write(ctx, conn, frame); err != nil {
					t.Fatal(err)
				}
				var answer v1.Envelope
				if err := wsjson.Read(ctx, conn, &answer); err != nil {
					t.Fatal(err)
				}
				return answer
			}
			if !mutual {
				if answer := write(2, r); answer.Type != v1.TypeError {
					t.Fatal("unnegotiated evidence accepted")
				}
				return
			}
			raw, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			if _, err = raw.Exec(`CREATE TRIGGER synthetic_wire_failure BEFORE UPDATE ON network_probe_tasks WHEN NEW.state='completed' BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`); err != nil {
				t.Fatal(err)
			}
			if answer := write(2, r); answer.Type != v1.TypeError {
				t.Fatal("storage failure was ACKed")
			}
			var count int
			if err = raw.QueryRow(`SELECT COUNT(*) FROM latency_samples`).Scan(&count); err != nil || count != 0 {
				t.Fatal("partial commit", count, err)
			}
			if _, err = raw.Exec(`DROP TRIGGER synthetic_wire_failure`); err != nil {
				t.Fatal(err)
			}
			for _, seq := range []uint64{3, 4} {
				answer := write(seq, r)
				ack, err := v1.DecodePayload[v1.Acknowledgement](answer)
				if err != nil || answer.Type != v1.TypeAcknowledged || ack.Sequence != seq {
					t.Fatal("missing durable ACK", err)
				}
			}
			if err = raw.QueryRow(`SELECT COUNT(*) FROM latency_samples`).Scan(&count); err != nil || count != 1 {
				t.Fatal("duplicate sample", count, err)
			}
			r.Packets = &check.PacketCounts{Sent: 1, Received: 0}
			r.Outcome = check.Failure
			r.ErrorClass = "timeout"
			r.RTTMS = nil
			if answer := write(5, r); answer.Type != v1.TypeError {
				t.Fatal("conflicting result ACKed")
			}
		})
	}
}
