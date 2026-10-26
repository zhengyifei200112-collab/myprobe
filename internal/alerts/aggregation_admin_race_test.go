package alerts

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/alertpolicy"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestAggregateAdminMutationBeforeEvaluationKeepsReceiptOnly(t *testing.T) {
	for _, action := range []string{"disable-policy", "delete-policy", "delete-channel", "change-condition", "narrow-scope", "delete-node", "disable-channel", "change-provider"} {
		for _, result := range []string{"success", "rejection", "unknown", "rate-limited"} {
			t.Run(action+"/"+result, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				entered, release := make(chan struct{}, 1), make(chan struct{})
				var calls atomic.Int32
				receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					_, _ = io.Copy(io.Discard, r.Body)
					entered <- struct{}{}
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
					switch result {
					case "success":
						w.WriteHeader(204)
					case "rejection":
						w.WriteHeader(503)
					case "rate-limited":
						w.Header().Set("Retry-After", "120")
						w.WriteHeader(429)
					default:
						conn, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						_ = conn.Close()
					}
				}))
				defer receiver.Close()
				defer close(release)
				db, svc, nodes := aggregateRuntime(t, receiver)
				policies, err := db.ListAlertPolicies(ctx)
				if err != nil || len(policies) != 1 {
					t.Fatal(err)
				}
				policy := policies[0]
				incidents, err := db.ListIncidents(ctx, store.IncidentFilter{Limit: 10})
				if err != nil || len(incidents) != 2 {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { _, err := svc.DeliverOne(ctx, time.Now().UTC()); done <- err }()
				select {
				case <-entered:
				case <-ctx.Done():
					t.Fatal("no request")
				}
				switch action {
				case "disable-policy":
					policy.Enabled = false
					_, err = svc.SavePolicy(ctx, policy)
				case "delete-policy":
					err = db.DeleteAlertPolicy(ctx, policy.ID, policy.Revision)
				case "delete-channel":
					err = db.DeleteAlertPolicy(ctx, policy.ID, policy.Revision)
					if err == nil {
						err = db.DeleteNotificationChannel(ctx, policy.ChannelID)
					}
				case "change-condition":
					policy.Config = []byte(`{"days_before":0,"recovery_seconds":0,"aggregation_seconds":5}`)
					_, err = svc.SavePolicy(ctx, policy)
				case "narrow-scope":
					policy.Scope = alertpolicy.Scope{Kind: "nodes", NodeIDs: []string{nodes[1].ID}}
					_, err = svc.SavePolicy(ctx, policy)
				case "delete-node":
					err = db.DeleteNode(ctx, nodes[0].ID)
				case "disable-channel":
					_, err = svc.UpdateChannel(ctx, policy.ChannelID, "disabled", "webhook", nil, false)
				case "change-provider":
					config := ChannelConfig{URL: receiver.URL}
					_, err = svc.UpdateChannel(ctx, policy.ChannelID, "replacement", "discord", &config, true)
				}
				if err != nil {
					t.Fatal(err)
				}
				// No Tick/Reconcile: current persisted management state must be enough.
				clocks := map[string]*time.Time{}
				for _, i := range incidents {
					current, err := db.Incident(ctx, i.ID)
					if err != nil {
						t.Fatal(err)
					}
					clocks[i.ID] = current.LastEnqueuedAt
				}
				release <- struct{}{}
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-ctx.Done():
					t.Fatal("completion did not finish")
				}
				wantReceipt := map[string]string{"success": "delivered", "rejection": "failed", "unknown": "unknown", "rate-limited": "failed"}[result]
				for _, i := range incidents {
					jobs, err := db.ListIncidentDeliveries(ctx, i.ID, 0, 10)
					if err != nil || len(jobs) != 1 {
						t.Fatal(err)
					}
					invalid := action != "narrow-scope" && action != "delete-node" || i.NodeID == nodes[0].ID
					if invalid && jobs[0].Status != "canceled" {
						t.Fatalf("obsolete member retained job authority: %s", jobs[0].Status)
					}
					if !invalid && result == "success" && jobs[0].Status != "delivered" {
						t.Fatal("valid member lost delivery")
					}
					attempts, err := db.ListDeliveryAttempts(ctx, jobs[0].ID)
					if err != nil || len(attempts) != 1 || attempts[0].Outcome != wantReceipt {
						t.Fatalf("receipt: %v %v", attempts, err)
					}
					current, err := db.Incident(ctx, i.ID)
					if err != nil {
						t.Fatal(err)
					}
					if invalid && !reflectTime(current.LastEnqueuedAt, clocks[i.ID]) {
						t.Fatal("late receipt changed obsolete member clock")
					}
				}
				// With a wholly revoked group, even a future claim must do no more I/O.
				if action != "narrow-scope" && action != "delete-node" {
					channel, err := db.NotificationChannel(ctx, policy.ChannelID)
					if action != "delete-channel" && (err != nil || channel.DeliveryPausedUntil != nil) {
						t.Fatal("revoked group regained channel wait authority")
					}
					if worked, err := svc.DeliverOne(ctx, time.Now().UTC().Add(time.Hour)); err != nil || worked {
						t.Fatalf("revoked group continued: %v %v", worked, err)
					}
				}
				if calls.Load() != 1 {
					t.Fatal("obsolete aggregate resent")
				}
			})
		}
	}
}

func TestAggregateConfigurationReplacedDuringActualRequest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	type receipt struct{ body, key string }
	received := make(chan receipt, 2)
	release := make(chan struct{})
	oldReceiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		received <- receipt{string(raw), r.Header.Get("Idempotency-Key")}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(429)
	}))
	defer oldReceiver.Close()
	defer close(release)
	newReceiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		received <- receipt{string(raw), r.Header.Get("Idempotency-Key")}
		w.WriteHeader(204)
	}))
	defer newReceiver.Close()
	db, svc, _ := aggregateRuntime(t, oldReceiver)
	policies, err := db.ListAlertPolicies(ctx)
	if err != nil || len(policies) != 1 {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := svc.DeliverOne(ctx, time.Now().UTC()); done <- err }()
	var first receipt
	select {
	case first = <-received:
	case <-ctx.Done():
		t.Fatal("first request missing")
	}
	config := ChannelConfig{URL: newReceiver.URL}
	if _, err := svc.UpdateChannel(ctx, policies[0].ChannelID, "replacement", "webhook", &config, true); err != nil {
		t.Fatal(err)
	}
	release <- struct{}{}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("old response missing")
	}
	channel, err := db.NotificationChannel(ctx, policies[0].ChannelID)
	if err != nil || channel.DeliveryPausedUntil != nil {
		t.Fatal("old receiver paused replacement")
	}
	page, err := db.ListNotificationAggregates(ctx, 0, 25)
	if err != nil || len(page.Aggregates) != 1 {
		t.Fatal(err)
	}
	if worked, err := svc.DeliverOne(ctx, page.Aggregates[0].AvailableAt.Add(-time.Millisecond)); err != nil || worked {
		t.Fatal("replacement bypassed intrinsic retry deadline")
	}
	if worked, err := svc.DeliverOne(ctx, page.Aggregates[0].AvailableAt); err != nil || !worked {
		t.Fatal("replacement not sent")
	}
	var second receipt
	select {
	case second = <-received:
	case <-ctx.Done():
		t.Fatal("replacement request missing")
	}
	if first.body != second.body || first.key == "" || first.key == second.key {
		t.Fatal("replacement reused old transport identity or changed logical content")
	}
	detail, err := db.AggregateContext(ctx, page.Aggregates[0].ID, 0, 25)
	if err != nil || detail.Totals.Delivered != 2 || len(detail.Attempts) != 2 || detail.Attempts[0].Outcome != "delivered" || detail.Attempts[1].Outcome != "failed" {
		t.Fatal("replacement lost retry history")
	}
}
