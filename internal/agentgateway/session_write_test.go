package agentgateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

func sessionWritePair(t *testing.T, version int) (*agentSession, *websocket.Conn) {
	t.Helper()
	accepted := make(chan *websocket.Conn, 1)
	stop := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		accepted <- conn
		<-stop
		conn.CloseNow()
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(stop) })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, _, err := websocket.Dial(ctx, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.CloseNow() })
	conn := <-accepted
	t.Cleanup(func() { conn.CloseNow() })
	return &agentSession{connection: conn, version: version}, client
}

func TestAgentSessionWriteCancellationKeepsUsableSocket(t *testing.T) {
	for _, version := range []int{1, 2} {
		for _, reason := range []string{"already_canceled", "canceled_while_waiting", "deadline_while_waiting"} {
			t.Run(fmt.Sprintf("v%d/%s", version, reason), func(t *testing.T) {
				session, peer := sessionWritePair(t, version)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if reason == "deadline_while_waiting" {
					ctx, cancel = context.WithTimeout(context.Background(), 30*time.Millisecond)
					defer cancel()
				}
				if reason == "already_canceled" {
					cancel()
				}
				envelope, err := protocol.NewEnvelope(protocol.TypeAcknowledged, 1, protocol.Acknowledgement{Sequence: 1})
				if err != nil {
					t.Fatal(err)
				}
				session.writeMu.Lock()
				done := make(chan error, 1)
				go func() { done <- session.write(ctx, envelope) }()
				if reason == "canceled_while_waiting" {
					cancel()
				}
				<-ctx.Done()
				var returned error
				completed := false
				select {
				case returned = <-done:
					completed = true
				case <-time.After(time.Second):
					t.Error("write did not return on cancellation while the writer remained locked")
				}
				session.writeMu.Unlock()
				if !completed {
					select {
					case returned = <-done:
					case <-time.After(time.Second):
						t.Fatal("write did not finish after releasing its owned test lock")
					}
				}
				if !errors.Is(returned, ctx.Err()) {
					t.Errorf("unexpected cancellation result: %v", returned)
				}
				fresh, stop := context.WithTimeout(context.Background(), time.Second)
				defer stop()
				freshEnvelope, err := protocol.NewEnvelope(protocol.TypeAcknowledged, 2, protocol.Acknowledgement{Sequence: 2})
				if err != nil {
					t.Fatal(err)
				}
				if err := session.write(fresh, freshEnvelope); err != nil {
					t.Fatalf("a canceled queued write closed the otherwise usable socket: %v", err)
				}
				var received protocol.Envelope
				if err := wsjson.Read(fresh, peer, &received); err != nil || received.Sequence != 2 || received.Version != version {
					t.Fatalf("fresh write was not the first received frame with the negotiated version: %+v %v", received, err)
				}
			})
		}
	}
}

func TestAgentSessionWriteWaitingSucceedsAfterRelease(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) {
			session, peer := sessionWritePair(t, version)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			envelope, err := protocol.NewEnvelope(protocol.TypeAcknowledged, 3, protocol.Acknowledgement{Sequence: 3})
			if err != nil {
				t.Fatal(err)
			}
			session.writeMu.Lock()
			done := make(chan error, 1)
			go func() { done <- session.write(ctx, envelope) }()
			select {
			case err := <-done:
				session.writeMu.Unlock()
				t.Fatalf("write bypassed its locked writer: %v", err)
			default:
			}
			session.writeMu.Unlock()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			var received protocol.Envelope
			if err := wsjson.Read(ctx, peer, &received); err != nil || received.Sequence != 3 || received.Version != version {
				t.Fatalf("uncanceled write changed after writer release: %+v %v", received, err)
			}
		})
	}
}
