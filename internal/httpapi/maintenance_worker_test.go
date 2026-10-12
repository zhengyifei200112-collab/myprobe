package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/alerts"
	"github.com/zhengyifei200112-collab/myprobe/internal/auth"
	"github.com/zhengyifei200112-collab/myprobe/internal/config"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

// Exercises management mutation, real evaluator/worker and an actual local HTTP
// receiver together. No production endpoints or credentials are used.
func TestMaintenanceAPIRuntimeNotifications(t *testing.T) {
	for _, recoverInside := range []bool{true, false} {
		t.Run(map[bool]string{true: "fault-resolves-inside", false: "continuing-fault"}[recoverInside], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			db, err := store.Open(ctx, filepath.Join(t.TempDir(), "runtime.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			messages := make(chan alerts.Notification, 8)
			receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var notification alerts.Notification
				if err := json.NewDecoder(r.Body).Decode(&notification); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				if r.Header.Get("Idempotency-Key") == "" {
					t.Error("missing receiver idempotency key")
				}
				select {
				case messages <- notification:
				default:
					t.Error("notification storm")
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			defer receiver.Close()
			key := strings.Repeat("m", 32)
			svc := alerts.New(db, key, alerts.NewHTTPSender(receiver.Client()), nil)
			channel, err := svc.CreateChannel(ctx, "local receiver", "webhook", alerts.ChannelConfig{URL: receiver.URL})
			if err != nil {
				t.Fatal(err)
			}
			node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "expiry fixture", Tags: []string{"prod"}})
			if err != nil {
				t.Fatal(err)
			}
			updateExpiry := func(at time.Time) {
				t.Helper()
				if _, err := db.UpdateNode(ctx, node.ID, store.UpdateNodeParams{Name: node.Name, Tags: node.Tags, ExpiresAt: &at, CollectionSeconds: 5, ReportSeconds: 5, LatencyMode: "ping"}); err != nil {
					t.Fatal(err)
				}
			}
			updateExpiry(time.Now().Add(time.Hour))
			rule, err := svc.CreateRule(ctx, node.ID, channel.ID, "expiry", alerts.RuleConfig{DaysBefore: 1}, 900)
			if err != nil {
				t.Fatal(err)
			}
			tick := func() {
				t.Helper()
				if err := svc.Tick(ctx, time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
			}
			// Continuing-fault case also verifies an older queued firing is canceled.
			if !recoverInside {
				tick()
			}
			authService := auth.New(db, time.Hour)
			if _, err := authService.Bootstrap(ctx, "admin", "fixture-password"); err != nil {
				t.Fatal(err)
			}
			hub := agentgateway.NewHub()
			api := NewWithAlertService(config.Config{EncryptionKey: key}, db, authService, agentgateway.New(db, hub), hub, svc)
			login, credentials := loginSecurityRequest(t, api.Handler(), "admin", "fixture-password", "", "")
			if login.Code != 200 {
				t.Fatal(login.Code)
			}
			cookie := login.Result().Cookies()[0]
			now := time.Now().UTC()
			draft := store.MaintenanceDraft{Selection: store.MaintenanceSelection{Kind: "tags", Tags: []string{"prod"}, TagMode: "all"}, Reason: "runtime work", Timezone: "UTC", StartsAt: now.Add(-time.Minute), EndsAt: now.Add(time.Hour)}
			raw, err := json.Marshal(draft)
			if err != nil {
				t.Fatal(err)
			}
			previewResponse := authenticatedRequest(t, api.Handler(), cookie, credentials.CSRFToken, http.MethodPost, "/api/v1/admin/maintenance-windows/preview", string(raw))
			var preview store.MaintenancePreview
			if err := json.Unmarshal(previewResponse.Body.Bytes(), &preview); err != nil || previewResponse.Code != 200 {
				t.Fatalf("preview: %s %v", previewResponse.Body.String(), err)
			}
			raw, err = json.Marshal(map[string]any{"draft": preview.Draft, "preview_digest": preview.Digest, "request_id": "runtime-create"})
			if err != nil {
				t.Fatal(err)
			}
			created := authenticatedRequest(t, api.Handler(), cookie, credentials.CSRFToken, http.MethodPost, "/api/v1/admin/maintenance-windows", string(raw))
			var result store.MaintenanceCreateResult
			if err := json.Unmarshal(created.Body.Bytes(), &result); err != nil || created.Code != 201 {
				t.Fatalf("create: %s %v", created.Body.String(), err)
			}
			done := make(chan struct{})
			go func() { defer close(done); svc.Run(ctx) }()
			defer func() { cancel(); <-done }()
			tick()
			incidents, err := db.ListIncidents(ctx, store.IncidentFilter{Limit: 10})
			if err != nil || len(incidents) != 1 || incidents[0].State != "firing" || incidents[0].RuleID != rule.ID {
				t.Fatalf("active event: %+v %v", incidents, err)
			}
			incidentID := incidents[0].ID
			if recoverInside {
				updateExpiry(time.Now().Add(72 * time.Hour))
				tick()
			}
			canceled := authenticatedRequest(t, api.Handler(), cookie, credentials.CSRFToken, http.MethodPost, "/api/v1/admin/maintenance-windows/"+result.WindowID+"/cancel", `{"revision":1}`)
			if canceled.Code != 200 {
				t.Fatalf("cancel: %s", canceled.Body.String())
			}
			tick()
			if !recoverInside {
				select {
				case msg := <-messages:
					if msg.State != "firing" || msg.NodeID != node.ID {
						t.Fatalf("message: %+v", msg)
					}
				case <-ctx.Done():
					t.Fatal("missing current firing notification")
				}
			}
			tick()
			// Let the running workers finish the send/commit, then stop cleanly.
			deadline := time.Now().Add(3 * time.Second)
			var jobs []store.NotificationDelivery
			for {
				jobs, err = db.ListIncidentDeliveries(ctx, incidentID, 0, 10)
				if err != nil {
					t.Fatal(err)
				}
				if recoverInside || (len(jobs) == 2 && jobs[1].Status == "delivered") || (len(jobs) == 2 && jobs[0].Status == "delivered") {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("delivery did not settle: %+v", jobs)
				}
				select {
				case <-time.After(10 * time.Millisecond):
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			cancel()
			<-done
			select {
			case msg := <-messages:
				t.Fatalf("unexpected notification: %+v", msg)
			default:
			}
			freshCtx := context.Background()
			// Restart evaluator and queue sender after cancellation: no duplicate summary.
			restarted := alerts.New(db, key, alerts.NewHTTPSender(receiver.Client()), nil)
			if err := restarted.Tick(freshCtx, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			if worked, err := restarted.DeliverOne(freshCtx, time.Now().UTC()); err != nil || worked {
				t.Fatalf("restart duplicate: %v %v", worked, err)
			}
			final, err := db.ListIncidents(freshCtx, store.IncidentFilter{Limit: 10})
			if err != nil || len(final) != 1 || final[0].ID != incidentID {
				t.Fatalf("event continuity: %+v %v", final, err)
			}
			if recoverInside && final[0].State != "resolved" || !recoverInside && final[0].State != "firing" {
				t.Fatalf("final state: %+v", final[0])
			}
			if recoverInside {
				if len(jobs) != 0 {
					t.Fatalf("in-window fault created deliveries: %+v", jobs)
				}
			} else {
				if len(jobs) != 2 {
					t.Fatalf("delivery count: %+v", jobs)
				}
				canceledCount, deliveredCount := 0, 0
				for _, job := range jobs {
					if job.Status == "canceled" && job.ErrorClass == "maintenance" {
						canceledCount++
					}
					if job.Status == "delivered" {
						deliveredCount++
					}
				}
				if canceledCount != 1 || deliveredCount != 1 {
					t.Fatalf("delivery outcomes: %+v", jobs)
				}
			}
		})
	}
}
