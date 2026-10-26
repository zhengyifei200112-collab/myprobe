package alerts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

// The receiver deliberately withholds its response while a real evaluator
// cancels the in-flight job under maintenance. Releasing it returns an ACK, a
// permanent rejection or a lost response, without reviving retry authority.
func TestMaintenanceRaceRetainsCompletedAttempt(t *testing.T) {
	for _, tc := range []struct {
		name          string
		status        int
		drop, disable bool
		want, class   string
		noEvaluation  bool
	}{
		{"ack", 204, false, false, "delivered", "", false},
		{"permanent-rejection", 401, false, false, "failed", "http_client_error", false},
		{"lost-response", 0, true, false, "unknown", "transport_error", false},
		{"policy-disabled", 204, false, true, "delivered", "", false},
		{"ack-before-maintenance-evaluation", 204, false, false, "delivered", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			db, err := store.Open(ctx, filepath.Join(t.TempDir(), "race.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			accepted := make(chan struct{})
			release := make(chan struct{})
			responseReleased := false
			var calls atomic.Int32
			keys := make(chan string, 4)
			receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var notification Notification
				if err := json.NewDecoder(r.Body).Decode(&notification); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				if notification.State != "firing" {
					t.Errorf("unexpected notification state: %s", notification.State)
				}
				keys <- r.Header.Get("Idempotency-Key")
				if calls.Add(1) == 1 {
					close(accepted)
					<-release
					if tc.drop {
						conn, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						conn.Close()
						return
					}
					w.WriteHeader(tc.status)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer receiver.Close()
			defer func() {
				if !responseReleased {
					close(release)
				}
			}()
			s := New(db, strings.Repeat("r", 32), NewHTTPSender(receiver.Client()), nil)
			node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "race fixture"})
			if err != nil {
				t.Fatal(err)
			}
			expires := time.Now().Add(time.Hour)
			node = updateNodeExpiry(t, db, node, &expires)
			channel, err := s.CreateChannel(ctx, "local receiver", "webhook", ChannelConfig{URL: receiver.URL})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.CreateRule(ctx, node.ID, channel.ID, "expiry", RuleConfig{DaysBefore: 1}, 900); err != nil {
				t.Fatal(err)
			}
			if err := s.Tick(ctx, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			incidents, err := db.ListIncidents(ctx, store.IncidentFilter{Limit: 10})
			if err != nil || len(incidents) != 1 {
				t.Fatalf("incidents: %+v %v", incidents, err)
			}
			jobs, err := db.ListIncidentDeliveries(ctx, incidents[0].ID, 0, 10)
			if err != nil || len(jobs) != 1 {
				t.Fatalf("jobs: %+v %v", jobs, err)
			}
			done := make(chan error, 1)
			go func() { _, err := s.DeliverOne(ctx, time.Now().UTC()); done <- err }()
			select {
			case <-accepted:
			case <-ctx.Done():
				t.Fatal("receiver never accepted request")
			}
			now := time.Now().UTC()
			window, err := db.CreateMaintenanceWindow(ctx, store.MaintenanceWindow{Reason: "work while sending", Timezone: "UTC", CreatedBy: "fixture", NodeIDs: []string{node.ID}, StartsAt: now.Add(-time.Second), EndsAt: now.Add(time.Hour)})
			if err != nil {
				t.Fatal(err)
			}
			if !tc.noEvaluation {
				if err := s.Tick(ctx, time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
				canceled, err := db.Delivery(ctx, jobs[0].ID)
				if err != nil || canceled.Status != "canceled" || canceled.ErrorClass != "maintenance" {
					t.Fatalf("cancellation: %+v %v", canceled, err)
				}
			}
			if tc.disable {
				policies, err := db.ListAlertPolicies(ctx)
				if err != nil || len(policies) != 1 {
					t.Fatalf("policies: %+v %v", policies, err)
				}
				p := policies[0]
				p.Enabled = false
				if _, err := s.SavePolicy(ctx, p); err != nil {
					t.Fatal(err)
				}
				if err := s.Tick(ctx, time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
			}
			close(release)
			responseReleased = true
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("completion lost after maintenance cancellation: %v", err)
				}
			case <-ctx.Done():
				t.Fatal("worker completion timed out")
			}
			attempts, err := db.ListDeliveryAttempts(ctx, jobs[0].ID)
			if err != nil || len(attempts) != 1 || attempts[0].Outcome != tc.want || attempts[0].ErrorClass != tc.class {
				t.Fatalf("acknowledged attempt: %+v %v", attempts, err)
			}
			final, err := db.Delivery(ctx, jobs[0].ID)
			wantStatus, wantError := "canceled", "maintenance"
			if tc.noEvaluation {
				wantStatus, wantError = "delivered", ""
				// Window existence alone cannot invent an earlier suppression decision.
				links, err := db.ListDeliveryMaintenance(ctx, jobs[0].ID, "", 10)
				if err != nil || len(links.Windows) != 0 {
					t.Fatalf("invented suppression: %+v %v", links, err)
				}
			}
			if err != nil || final.Status != wantStatus || final.ErrorClass != wantError {
				t.Fatalf("ack resurrected task: %+v %v", final, err)
			}
			current, err := db.Incident(ctx, incidents[0].ID)
			if err != nil || (tc.noEvaluation && current.LastEnqueuedAt == nil) || (!tc.noEvaluation && current.LastEnqueuedAt != nil) {
				t.Fatalf("ack changed repeat clock: %+v %v", current, err)
			}
			if tc.disable && (current.State != "resolved" || current.ResolutionReason != "rule_disabled") {
				t.Fatalf("late ack revived disabled incident: %+v", current)
			}
			if calls.Load() != 1 {
				t.Fatalf("in-flight request duplicate: %d", calls.Load())
			}
			if err := db.CancelMaintenanceWindow(ctx, window.ID, 1, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			path := db.DatabasePath()
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := store.Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			restarted := New(reopened, strings.Repeat("r", 32), NewHTTPSender(receiver.Client()), nil)
			if err := restarted.Tick(ctx, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			worked, err := restarted.DeliverOne(ctx, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			wantCalls := int32(2)
			if tc.disable {
				wantCalls = 1
				if worked {
					t.Fatal("disabled policy sent after restart")
				}
			} else if !worked {
				t.Fatal("continuing fault had no fresh summary")
			}
			if calls.Load() != wantCalls {
				t.Fatalf("post-maintenance sends: %d want %d", calls.Load(), wantCalls)
			}
			if err := restarted.Tick(ctx, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			if worked, err := restarted.DeliverOne(ctx, time.Now().UTC()); err != nil || worked {
				t.Fatalf("restart repeat: %v %v", worked, err)
			}
			firstKey := <-keys
			if firstKey == "" {
				t.Fatal("missing receiver idempotency key")
			}
			if !tc.disable {
				nextKey := <-keys
				if nextKey == "" || nextKey == firstKey {
					t.Fatal("current summary reused canceled intent identity")
				}
			}
		})
	}
}
