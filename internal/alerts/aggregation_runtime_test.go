package alerts

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/alertpolicy"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func aggregateRuntime(t *testing.T, receiver *httptest.Server) (*store.Store, *Service, []store.Node) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "aggregate-runtime.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	svc := New(db, "synthetic-aggregate-key-0123456789", NewHTTPSender(receiver.Client()), nil)
	channel, err := svc.CreateChannel(ctx, "loopback", "webhook", ChannelConfig{URL: receiver.URL})
	if err != nil {
		t.Fatal(err)
	}
	nodes := []store.Node{}
	expires := time.Now().UTC().Add(time.Hour)
	for _, name := range []string{"first fixture", "second fixture"} {
		node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: name})
		if err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, updateNodeExpiry(t, db, node, &expires))
	}
	if _, err := svc.SavePolicy(ctx, store.AlertPolicy{Policy: alertpolicy.Policy{Key: "aggregate-expiry", Scope: alertpolicy.Scope{Kind: "all"}, Enabled: true}, Name: "aggregate expiry", Kind: "expiry", ChannelID: channel.ID, Config: json.RawMessage(`{"days_before":1,"recovery_seconds":0,"aggregation_seconds":5}`), CooldownSeconds: 300}); err != nil {
		t.Fatal(err)
	}
	// Timestamped observation ends its fixed window before actual network I/O.
	if err := svc.Tick(ctx, time.Now().UTC().Add(-6*time.Second)); err != nil {
		t.Fatal(err)
	}
	return db, svc, nodes
}

func TestAggregateActualSendKeepsMemberLateResults(t *testing.T) {
	for _, recoverInside := range []bool{false, true} {
		t.Run(map[bool]string{false: "partial-silence", true: "recovery-after-final-check"}[recoverInside], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			accepted, release := make(chan Notification, 1), make(chan struct{})
			released := false
			receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				if strings.Contains(string(raw), "private-aggregate-note") || strings.Contains(string(raw), "actor_name") {
					t.Error("private action in notification")
				}
				var n Notification
				if err := json.Unmarshal(raw, &n); err != nil {
					t.Error(err)
				}
				if n.State != "firing" || n.AggregateID == "" || len(n.Members) != 2 || r.Header.Get("Idempotency-Key") == "" {
					t.Error("invalid grouped receipt")
				}
				accepted <- n
				<-release
				w.WriteHeader(204)
			}))
			defer receiver.Close()
			defer func() {
				if !released {
					close(release)
				}
			}()
			db, svc, nodes := aggregateRuntime(t, receiver)
			incidents, err := db.ListIncidents(ctx, store.IncidentFilter{Limit: 10})
			if err != nil || len(incidents) != 2 {
				t.Fatal(err)
			}
			var selected store.Incident
			for _, i := range incidents {
				if i.NodeID == nodes[0].ID {
					selected = i
				}
			}
			actor, err := db.CreateUser(ctx, "fixture operator", "synthetic")
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { _, err := svc.DeliverOne(ctx, time.Now().UTC()); done <- err }()
			select {
			case <-accepted:
			case <-ctx.Done():
				t.Fatal("no real receiver request")
			}
			if recoverInside {
				expires := time.Now().UTC().Add(72 * time.Hour)
				updateNodeExpiry(t, db, nodes[0], &expires)
				if err := svc.Tick(ctx, time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := db.CreateIncidentSilence(ctx, selected.ID, "runtime-mute", actor.ID, store.CreateIncidentSilenceParams{DurationSeconds: 60, Reason: "private-aggregate-note"}, time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
			}
			before, err := db.Incident(ctx, selected.ID)
			if err != nil {
				t.Fatal(err)
			}
			close(release)
			released = true
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("completion discarded")
			}
			after, err := db.Incident(ctx, selected.ID)
			if err != nil || after.State != before.State || !reflectTime(after.LastEnqueuedAt, before.LastEnqueuedAt) {
				t.Fatal("late result changed canceled member clock")
			}
			for _, i := range incidents {
				jobs, err := db.ListIncidentDeliveries(ctx, i.ID, 0, 10)
				if err != nil {
					t.Fatal(err)
				}
				var old store.NotificationDelivery
				for _, job := range jobs {
					if job.NotificationType == "firing" {
						old = job
					}
				}
				want := "delivered"
				if i.ID == selected.ID {
					want = "canceled"
				}
				if old.Status != want {
					t.Fatalf("member status: %+v", old)
				}
				attempts, err := db.ListDeliveryAttempts(ctx, old.ID)
				if err != nil || len(attempts) != 1 || attempts[0].Outcome != "delivered" {
					t.Fatalf("actual member receipt: %+v %v", attempts, err)
				}
			}
			if worked, err := svc.DeliverOne(ctx, time.Now().UTC()); err != nil || worked {
				t.Fatal("old group retried or early recovery escaped")
			}
		})
	}
}
func reflectTime(a, b *time.Time) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && a.Equal(*b))
}

func TestAggregateRealReceiverRetryUsesSameKeyAndBody(t *testing.T) {
	ctx := context.Background()
	receipts := make(chan struct{ body, key string }, 2)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		receipts <- struct{ body, key string }{string(raw), r.Header.Get("Idempotency-Key")}
		if len(receipts) == 1 {
			w.WriteHeader(503)
		} else {
			w.WriteHeader(204)
		}
	}))
	defer receiver.Close()
	db, svc, _ := aggregateRuntime(t, receiver)
	now := time.Now().UTC()
	if worked, err := svc.DeliverOne(ctx, now); err != nil || !worked {
		t.Fatal(err)
	}
	incidents, err := db.ListIncidents(ctx, store.IncidentFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var due time.Time
	for _, i := range incidents {
		jobs, err := db.ListIncidentDeliveries(ctx, i.ID, 0, 10)
		if err != nil || len(jobs) != 1 || jobs[0].Status != "pending" || jobs[0].AttemptCount != 1 || jobs[0].AvailableAt.Before(now.Add(30*time.Second)) {
			t.Fatalf("retry state: %+v %v", jobs, err)
		}
		if jobs[0].AvailableAt.After(due) {
			due = jobs[0].AvailableAt
		}
	}
	if worked, err := svc.DeliverOne(ctx, due.Add(-time.Millisecond)); err != nil || worked {
		t.Fatal("provider retry deadline ignored")
	}
	if worked, err := svc.DeliverOne(ctx, due); err != nil || !worked {
		t.Fatal(err)
	}
	one, two := <-receipts, <-receipts
	if one.key == "" || one.key != two.key || one.body != two.body {
		t.Fatal("actual retry changed identity/content")
	}
}
