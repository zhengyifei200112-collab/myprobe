package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/alerts"
	"github.com/zhengyifei200112-collab/myprobe/internal/auth"
	"github.com/zhengyifei200112-collab/myprobe/internal/config"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestLiveIncidentAcknowledgementBrowser(t *testing.T) {
	if os.Getenv("MYPROBE_TEST_ACK_BROWSER") != "1" {
		t.Skip("requires Playwright/Edge; opt in with MYPROBE_TEST_ACK_BROWSER=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "browser.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	key, password := rand.Text()+rand.Text(), rand.Text()
	authService := auth.New(db, time.Hour)
	if _, err := authService.Bootstrap(ctx, "browser-admin", password); err != nil {
		t.Fatal(err)
	}
	node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "Ack node"})
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().UTC().Add(time.Hour)
	if _, err := db.UpdateNode(ctx, node.ID, store.UpdateNodeParams{Name: node.Name, ExpiresAt: &expires, CollectionSeconds: 5, ReportSeconds: 5, LatencyMode: "ping"}); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		if strings.Contains(string(raw), "private-browser-note") || strings.Contains(string(raw), "actor_name") {
			t.Error("private acknowledgement data entered a notification")
		}
		var notification alerts.Notification
		if err := json.Unmarshal(raw, &notification); err != nil {
			t.Error(err)
		}
		count := calls.Add(1)
		want := "firing"
		if count == 2 {
			want = "resolved"
		}
		if count > 2 || notification.NodeID != node.ID || notification.State != want || r.Header.Get("Idempotency-Key") == "" {
			t.Errorf("unexpected browser receipt %d: %+v", count, notification)
		}
		w.WriteHeader(204)
	}))
	defer receiver.Close()
	svc := alerts.New(db, key, alerts.NewHTTPSender(receiver.Client()), nil)
	channel, err := svc.CreateChannel(ctx, "Browser local receiver", "webhook", alerts.ChannelConfig{URL: receiver.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateRule(ctx, node.ID, channel.ID, "expiry", alerts.RuleConfig{DaysBefore: 1}, 900); err != nil {
		t.Fatal(err)
	}
	if err := svc.Tick(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	incidents, err := db.ListIncidents(ctx, store.IncidentFilter{Limit: 10})
	if err != nil || len(incidents) != 1 {
		t.Fatalf("primary incident: %+v %v", incidents, err)
	}
	primary := incidents[0].ID
	other, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "Other ack node"})
	if err != nil {
		t.Fatal(err)
	}
	otherRule, err := db.CreateAlertRule(ctx, other.ID, channel.ID, "cpu", json.RawMessage(`{"threshold_percent":90}`), 900)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ObserveAlert(ctx, otherRule, other, store.AlertObservation{At: time.Now().UTC(), Known: true, Active: true}); err != nil {
		t.Fatal(err)
	}
	hub := agentgateway.NewHub()
	api := NewWithAlertService(config.Config{EncryptionKey: key}, db, authService, agentgateway.New(db, hub), hub, svc)
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		sent := false
		for ctx.Err() == nil {
			var err error
			if !sent {
				view, readErr := db.IncidentAcknowledgement(ctx, primary)
				err = readErr
				if err == nil && view.Acknowledgement != nil {
					_, err = svc.DeliverOne(ctx, time.Now().UTC())
					sent = true
				}
			} else {
				nodes, readErr := db.ListNodes(ctx)
				err = readErr
				if err == nil {
					for _, current := range nodes {
						if current.ID == node.ID && current.ExpiresAt != nil && current.ExpiresAt.After(time.Now().UTC().Add(48*time.Hour)) {
							if err = svc.Tick(ctx, time.Now().UTC()); err == nil {
								_, err = svc.DeliverOne(ctx, time.Now().UTC())
							}
							if err != nil && ctx.Err() == nil {
								t.Error(err)
							}
							return
						}
					}
				}
			}
			if err != nil && ctx.Err() == nil {
				t.Error(err)
				return
			}
			select {
			case <-time.After(10 * time.Millisecond):
			case <-ctx.Done():
				return
			}
		}
	}()
	defer func() { cancel(); <-workerDone }()
	command := exec.CommandContext(ctx, "node", "../../scripts/incident-acknowledgement-live-ui.cjs")
	command.Env = append(os.Environ(), "ACK_BROWSER_URL="+server.URL, "ACK_BROWSER_PASSWORD="+password)
	output, err := command.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("receipt count: %d", calls.Load())
	}
	view, err := db.IncidentAcknowledgement(ctx, primary)
	if err != nil || view.IncidentState != "resolved" || view.Acknowledgement == nil || !strings.Contains(view.Acknowledgement.Note, "private-browser-note") {
		t.Fatalf("final history: %+v %v", view, err)
	}
}
