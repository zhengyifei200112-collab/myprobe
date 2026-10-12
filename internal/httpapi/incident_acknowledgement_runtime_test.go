package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
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

func TestAcknowledgementDoesNotSilenceRealNotifications(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "runtime.db")
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	key, password := rand.Text()+rand.Text(), rand.Text()
	authService := auth.New(db, time.Hour)
	if _, err := authService.Bootstrap(ctx, "runtime-admin", password); err != nil {
		t.Fatal(err)
	}
	type receipt struct {
		notification alerts.Notification
		key          string
	}
	receipts := make(chan receipt, 4)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		for _, forbidden := range []string{"private-runtime-note", "actor_id", "actor_name", "acknowledgement"} {
			if strings.Contains(string(raw), forbidden) {
				t.Errorf("notification leaked %s", forbidden)
			}
		}
		var notification alerts.Notification
		if err := json.Unmarshal(raw, &notification); err != nil {
			t.Error(err)
		}
		receipts <- receipt{notification, r.Header.Get("Idempotency-Key")}
		w.WriteHeader(204)
	}))
	defer receiver.Close()
	svc := alerts.New(db, key, alerts.NewHTTPSender(receiver.Client()), nil)
	hub := agentgateway.NewHub()
	api := NewWithAlertService(config.Config{EncryptionKey: key}, db, authService, agentgateway.New(db, hub), hub, svc)
	loginBody, _ := json.Marshal(map[string]string{"username": "runtime-admin", "password": password})
	login := httptest.NewRecorder()
	api.Handler().ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(string(loginBody))))
	if login.Code != 200 {
		t.Fatalf("login: %d", login.Code)
	}
	var session struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	cookie := login.Result().Cookies()[0]
	node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "runtime fixture"})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Millisecond)
	setExpiry := func(expiry time.Time) {
		t.Helper()
		if _, err := db.UpdateNode(ctx, node.ID, store.UpdateNodeParams{Name: node.Name, ExpiresAt: &expiry, CollectionSeconds: 5, ReportSeconds: 5, LatencyMode: "ping"}); err != nil {
			t.Fatal(err)
		}
	}
	setExpiry(at.Add(time.Hour))
	channel, err := svc.CreateChannel(ctx, "local receiver", "webhook", alerts.ChannelConfig{URL: receiver.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateRule(ctx, node.ID, channel.ID, "expiry", alerts.RuleConfig{DaysBefore: 1}, 300); err != nil {
		t.Fatal(err)
	}
	if err := svc.Tick(ctx, at); err != nil {
		t.Fatal(err)
	}
	incidents, err := db.ListIncidents(ctx, store.IncidentFilter{Limit: 10})
	if err != nil || len(incidents) != 1 {
		t.Fatalf("incidents: %+v %v", incidents, err)
	}
	id := incidents[0].ID
	ackPath := "/api/v1/admin/incidents/" + id + "/acknowledgement"
	body := `{"request_id":"runtime-ack","note":"private-runtime-note"}`
	created := authenticatedRequest(t, api.Handler(), cookie, session.CSRFToken, http.MethodPost, ackPath, body)
	if created.Code != 201 {
		t.Fatalf("ack: %d %s", created.Code, created.Body.String())
	}
	var original store.AcknowledgeIncidentResult
	if err := json.Unmarshal(created.Body.Bytes(), &original); err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	deliver := func(now time.Time, state string) {
		t.Helper()
		if worked, err := svc.DeliverOne(ctx, now); err != nil || !worked {
			t.Fatalf("delivery %s: %v %v", state, worked, err)
		}
		select {
		case got := <-receipts:
			if got.notification.State != state || got.notification.IncidentID != id || got.key == "" || keys[got.key] {
				t.Fatalf("receipt: %+v", got)
			}
			keys[got.key] = true
		case <-ctx.Done():
			t.Fatal("receiver timeout")
		}
	}
	deliver(at.Add(time.Second), "firing")
	if err := svc.Tick(ctx, at.Add(302*time.Second)); err != nil {
		t.Fatal(err)
	}
	deliver(at.Add(302*time.Second), "firing")
	setExpiry(at.Add(72 * time.Hour))
	if err := svc.Tick(ctx, at.Add(303*time.Second)); err != nil {
		t.Fatal(err)
	}
	deliver(at.Add(303*time.Second), "resolved")
	jobs, err := db.ListIncidentDeliveries(ctx, id, 0, 10)
	if err != nil || len(jobs) != 3 {
		t.Fatalf("jobs: %+v %v", jobs, err)
	}
	for _, job := range jobs {
		if job.Status != "delivered" || job.AttemptCount != 1 {
			t.Fatalf("ack changed delivery: %+v", job)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	svc = alerts.New(reopened, key, alerts.NewHTTPSender(receiver.Client()), nil)
	if err := svc.Tick(ctx, at.Add(304*time.Second)); err != nil {
		t.Fatal(err)
	}
	if worked, err := svc.DeliverOne(ctx, at.Add(304*time.Second)); err != nil || worked {
		t.Fatalf("restart duplicate: %v %v", worked, err)
	}
	view, err := reopened.IncidentAcknowledgement(ctx, id)
	if err != nil || view.IncidentState != "resolved" || view.Acknowledgement == nil || !view.Acknowledgement.AcknowledgedAt.Equal(original.Acknowledgement.AcknowledgedAt) || view.Acknowledgement.Note != "private-runtime-note" {
		t.Fatalf("resolved ack history: %+v %v", view, err)
	}
	if len(receipts) != 0 || len(keys) != 3 {
		t.Fatal("unexpected receipts after restart")
	}
}
