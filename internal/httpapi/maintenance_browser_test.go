package httpapi

import (
	"context"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/alerts"
	"github.com/zhengyifei200112-collab/myprobe/internal/auth"
	"github.com/zhengyifei200112-collab/myprobe/internal/config"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestLiveMaintenanceBrowserLifecycle(t *testing.T) {
	if os.Getenv("MYPROBE_TEST_MAINTENANCE_BROWSER") != "1" {
		t.Skip("requires Playwright and Edge; opt in with MYPROBE_TEST_MAINTENANCE_BROWSER=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "browser.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	authService := auth.New(db, time.Hour)
	password := rand.Text()
	if _, err := authService.Bootstrap(ctx, "browser-admin", password); err != nil {
		t.Fatal(err)
	}
	node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "Maintenance node", Tags: []string{"prod"}})
	if err != nil {
		t.Fatal(err)
	}
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("maintenance browser fixture unexpectedly sent notification")
		w.WriteHeader(204)
	}))
	defer receiver.Close()
	key := rand.Text() + rand.Text()
	svc := alerts.New(db, key, alerts.NewHTTPSender(receiver.Client()), nil)
	channel, err := svc.CreateChannel(ctx, "Browser local receiver", "webhook", alerts.ChannelConfig{URL: receiver.URL})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := svc.CreateRule(ctx, node.ID, channel.ID, "cpu", alerts.RuleConfig{ThresholdPercent: 90}, 900)
	if err != nil {
		t.Fatal(err)
	}
	incident, err := db.ObserveAlert(ctx, rule, node, store.AlertObservation{At: time.Now().UTC(), Known: true, Active: true, Message: "synthetic browser fault"})
	if err != nil {
		t.Fatal(err)
	}
	hub := agentgateway.NewHub()
	api := NewWithAlertService(config.Config{EncryptionKey: key}, db, authService, agentgateway.New(db, hub), hub, svc)
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		for ctx.Err() == nil {
			windows, _, err := db.ListMaintenanceWindows(ctx, "", 25)
			if err != nil {
				if ctx.Err() == nil {
					t.Error(err)
				}
				return
			}
			if len(windows) > 0 {
				if _, err := svc.DeliverOne(ctx, time.Now().UTC()); err != nil {
					t.Error(err)
				}
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
	command := exec.CommandContext(ctx, "node", "../../scripts/maintenance-live-ui.cjs")
	command.Env = append(os.Environ(), "MAINTENANCE_BROWSER_URL="+server.URL, "MAINTENANCE_BROWSER_PASSWORD="+password)
	output, err := command.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatal(err)
	}
	windows, _, err := db.ListMaintenanceWindows(ctx, "", 25)
	if err != nil || len(windows) != 2 {
		t.Fatalf("window count: %d %v", len(windows), err)
	}
	for _, w := range windows {
		if w.CanceledAt == nil || w.Revision != 2 || w.Selection == nil || w.CreatedBy == "" || w.CanceledBy != w.CreatedBy {
			t.Fatalf("window state: %+v", w)
		}
	}
	jobs, err := db.ListIncidentDeliveries(ctx, incident.ID, 0, 10)
	if err != nil || len(jobs) != 1 || jobs[0].Status != "canceled" || jobs[0].ErrorClass != "maintenance" {
		t.Fatalf("browser suppressed delivery: %+v %v", jobs, err)
	}
}
