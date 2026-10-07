package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/alerts"
	"github.com/zhengyifei200112-collab/myprobe/internal/auth"
	"github.com/zhengyifei200112-collab/myprobe/internal/config"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestIncidentPipelineRetryRecoveryAndAdminReads(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var notifications []alerts.Notification
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var notification alerts.Notification
		if err := json.NewDecoder(r.Body).Decode(&notification); err != nil {
			t.Error(err)
		}
		notifications = append(notifications, notification)
		if len(notifications) == 1 {
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()
	authService := auth.New(db, time.Hour)
	if _, err := authService.Bootstrap(ctx, "admin", "pipeline-test-password"); err != nil {
		t.Fatal(err)
	}
	hub := agentgateway.NewHub()
	server := New(config.Config{EncryptionKey: strings.Repeat("e", 32)}, db, authService, agentgateway.New(db, hub), hub)
	server.alerts = alerts.New(db, strings.Repeat("e", 32), alerts.NewHTTPSender(receiver.Client()), nil)
	login := httptest.NewRecorder()
	server.Handler().ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"username":"admin","password":"pipeline-test-password"}`)))
	if login.Code != http.StatusOK {
		t.Fatalf("login: %d", login.Code)
	}
	var session struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	cookie := login.Result().Cookies()[0]
	node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "pipeline"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	setExpiry := func(expiry time.Time) {
		t.Helper()
		_, err := db.UpdateNode(ctx, node.ID, store.UpdateNodeParams{Name: node.Name, ExpiresAt: &expiry, CollectionSeconds: 5, ReportSeconds: 5, LatencyMode: "ping"})
		if err != nil {
			t.Fatal(err)
		}
	}
	setExpiry(now.Add(time.Hour))
	channel, err := server.alerts.CreateChannel(ctx, "receiver", "webhook", alerts.ChannelConfig{URL: receiver.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.alerts.CreateRule(ctx, node.ID, channel.ID, "expiry", alerts.RuleConfig{DaysBefore: 1}, 300); err != nil {
		t.Fatal(err)
	}
	get := func(path string, destination any) {
		t.Helper()
		response := authenticatedRequest(t, server.Handler(), cookie, session.CSRFToken, http.MethodGet, path, "")
		if response.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, response.Code, response.Body.String())
		}
		if err := json.Unmarshal(response.Body.Bytes(), destination); err != nil {
			t.Fatal(err)
		}
	}
	if err := server.alerts.Tick(ctx, now); err != nil {
		t.Fatal(err)
	}
	var incidents struct {
		Items []store.Incident `json:"incidents"`
	}
	get("/api/v1/admin/incidents?state=firing", &incidents)
	if len(incidents.Items) != 1 {
		t.Fatalf("incidents: %+v", incidents)
	}
	id := incidents.Items[0].ID
	if _, err := server.alerts.DeliverOne(ctx, now); err != nil {
		t.Fatal(err)
	}
	var deliveries struct {
		Items []store.NotificationDelivery `json:"deliveries"`
	}
	path := "/api/v1/admin/incidents/" + id + "/deliveries"
	get(path, &deliveries)
	if len(deliveries.Items) != 1 || deliveries.Items[0].Status != "pending" {
		t.Fatalf("retry API: %+v", deliveries)
	}
	retryAt := deliveries.Items[0].AvailableAt.Add(time.Millisecond)
	if _, err := server.alerts.DeliverOne(ctx, retryAt); err != nil {
		t.Fatal(err)
	}
	get(path, &deliveries)
	if deliveries.Items[0].Status != "delivered" || deliveries.Items[0].AttemptCount != 2 {
		t.Fatalf("delivery API: %+v", deliveries)
	}
	setExpiry(now.Add(10 * 24 * time.Hour))
	if err := server.alerts.Tick(ctx, retryAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := server.alerts.DeliverOne(ctx, retryAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	get("/api/v1/admin/incidents?state=resolved", &incidents)
	if len(incidents.Items) != 1 || incidents.Items[0].ID != id || incidents.Items[0].ResolutionReason != "recovered" {
		t.Fatalf("recovery API: %+v", incidents)
	}
	get(path, &deliveries)
	if len(deliveries.Items) != 2 || deliveries.Items[0].NotificationType != "resolved" || deliveries.Items[0].Status != "delivered" {
		t.Fatalf("recovery delivery API: %+v", deliveries)
	}
	if len(notifications) != 3 || notifications[2].State != "resolved" || notifications[2].IncidentID != id {
		t.Fatalf("receiver: %+v", notifications)
	}
}
