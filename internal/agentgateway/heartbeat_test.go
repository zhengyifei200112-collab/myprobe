package agentgateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	v1 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestHeartbeatACKRequiresCurrentDurableContact(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			path := filepath.Join(t.TempDir(), "heartbeat.db")
			db, err := store.Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			node, token, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "synthetic contact"})
			if err != nil {
				t.Fatal(err)
			}
			captured := time.Now().UTC().Add(-time.Hour)
			if err := db.SaveReportWithIngress(ctx, node.ID, v1.Report{CapturedAt: captured, PublicIP: "198.51.100.123"}, "websocket", version); err != nil {
				t.Fatal(err)
			}
			before, err := db.LastReportIngress(ctx, node.ID)
			if err != nil {
				t.Fatal(err)
			}
			hub := NewHub()
			g := New(db, hub)
			handler := g.WebSocket
			if version == 2 {
				handler = g.WebSocketV2
			}
			server := httptest.NewServer(http.HandlerFunc(handler))
			defer server.Close()
			conn, _, err := websocket.Dial(ctx, strings.Replace(server.URL, "http", "ws", 1), &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + token}}})
			if err != nil {
				t.Fatal(err)
			}
			defer conn.CloseNow()
			hello, _ := v1.NewEnvelope(v1.TypeHello, 1, v1.Hello{AgentVersion: "private-version", Hostname: "private-host", OS: "linux"})
			hello.Version = version
			if err := wsjson.Write(ctx, conn, hello); err != nil {
				t.Fatal(err)
			}
			var reply v1.Envelope
			if err := wsjson.Read(ctx, conn, &reply); err != nil || reply.Type != v1.TypeWelcome {
				t.Fatal("welcome", err)
			}
			control, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer control.Close()
			if _, err := control.Exec(`UPDATE nodes SET last_seen_at=? WHERE id=?`, captured.Format(time.RFC3339Nano), node.ID); err != nil {
				t.Fatal(err)
			}
			events, stop := hub.Subscribe()
			defer stop()
			send := func(sequence uint64, expected string) {
				t.Helper()
				frame, _ := v1.NewEnvelope(v1.TypeHeartbeat, sequence, struct{}{})
				frame.Version = version
				if err := wsjson.Write(ctx, conn, frame); err != nil {
					t.Fatal(err)
				}
				if err := wsjson.Read(ctx, conn, &reply); err != nil || reply.Type != expected || reply.Version != version {
					t.Fatalf("heartbeat reply: %+v %v", reply, err)
				}
			}
			send(2, v1.TypeAcknowledged)
			item, err := db.PublicNode(ctx, node.ID, time.Now().UTC())
			if err != nil || !item.Online || !item.Stale || !item.Report.CapturedAt.Equal(captured) {
				t.Fatal("contact fabricated fresh metrics", err)
			}
			select {
			case event := <-events:
				if event.Node.Report.PublicIP != "198.51.••.••" || event.Node.Node.Agent.Hostname != "" || event.Node.Node.Agent.AgentVersion != "" || !event.Node.Stale || !event.Node.Online {
					t.Fatal("contact publication lost freshness/privacy")
				}
			case <-ctx.Done():
				t.Fatal("contact state was not published")
			}
			seen := *item.Node.LastSeenAt
			if _, err := control.Exec(`CREATE TRIGGER reject_heartbeat BEFORE UPDATE OF last_seen_at ON nodes BEGIN SELECT RAISE(ABORT,'synthetic contact failure'); END`); err != nil {
				t.Fatal(err)
			}
			send(3, v1.TypeError)
			after, err := db.LastReportIngress(ctx, node.ID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("heartbeat changed ingress", err)
			}
			item, err = db.PublicNode(ctx, node.ID, time.Now().UTC())
			if err != nil || !item.Node.LastSeenAt.Equal(seen) {
				t.Fatal("failed heartbeat was partly persisted", err)
			}
			select {
			case <-events:
				t.Fatal("failed heartbeat published success")
			default:
			}
			if _, err := control.Exec(`DROP TRIGGER reject_heartbeat`); err != nil {
				t.Fatal(err)
			}
			g.sessionsMu.RLock()
			old := g.sessions[node.ID]
			g.sessionsMu.RUnlock()
			g.unregister(node.ID, old)
			if err := g.recordHeartbeat(ctx, node.ID, old); !errors.Is(err, store.ErrConfigurationSuperseded) {
				t.Fatal("unregistered socket refreshed contact", err)
			}
			send(4, v1.TypeError)
			if err := wsjson.Read(ctx, conn, &reply); err == nil {
				t.Fatal("unregistered socket was left active after contact rejection")
			}
			var samples int
			if err := control.QueryRow(`SELECT count(*) FROM metric_samples`).Scan(&samples); err != nil || samples != 1 {
				t.Fatal("heartbeat inserted samples", err)
			}
		})
	}
}

func TestHTTPHelloRequiresAuthenticationAndDoesNotCreateMetricIngress(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	node, token, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "synthetic HTTP contact"})
	if err != nil {
		t.Fatal(err)
	}
	g := New(db, NewHub())
	frame, _ := v1.NewEnvelope(v1.TypeHello, 1, v1.Hello{AgentVersion: "fixture", OS: "linux"})
	body, _ := json.Marshal(frame)
	for _, tc := range []struct {
		token  string
		status int
	}{{"invalid", 401}, {token, 200}} {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/agent/hello", strings.NewReader(string(body)))
		r.Header.Set("Authorization", "Bearer "+tc.token)
		w := httptest.NewRecorder()
		g.HTTPHello(w, r)
		if w.Code != tc.status {
			t.Fatalf("HTTP hello: %d", w.Code)
		}
		item, err := db.PublicNode(ctx, node.ID, time.Now().UTC())
		if err != nil || item.Online != (tc.status == 200) || item.Report != nil {
			t.Fatal("HTTP hello contact/auth boundary", err)
		}
	}
	ingress, err := db.LastReportIngress(ctx, node.ID)
	if err != nil || ingress.Status != "unavailable" {
		t.Fatal("hello manufactured ingress", err)
	}
}
