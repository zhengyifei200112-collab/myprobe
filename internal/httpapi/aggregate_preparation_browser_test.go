package httpapi

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/alertpolicy"
	"github.com/zhengyifei200112-collab/myprobe/internal/alerts"
	"github.com/zhengyifei200112-collab/myprobe/internal/auth"
	"github.com/zhengyifei200112-collab/myprobe/internal/config"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestLiveAggregatePreparationFailureBrowser(t *testing.T) {
	if os.Getenv("MYPROBE_TEST_AGGREGATION_BROWSER") != "1" {
		t.Skip("requires Playwright/Edge; opt in with MYPROBE_TEST_AGGREGATION_BROWSER=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "failure.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	key, password := rand.Text()+rand.Text(), rand.Text()
	authService := auth.New(db, time.Hour)
	if _, err := authService.Bootstrap(ctx, "browser-admin", password); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(204) }))
	defer receiver.Close()
	svc := alerts.New(db, key, alerts.NewHTTPSender(receiver.Client()), nil)
	channel, err := svc.CreateChannel(ctx, "Local failed preparation", "webhook", alerts.ChannelConfig{URL: receiver.URL})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	for _, name := range []string{"Unsent node A", "Unsent node B"} {
		node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: name})
		if err != nil {
			t.Fatal(err)
		}
		expiry := at.Add(time.Hour)
		if _, err := db.UpdateNode(ctx, node.ID, store.UpdateNodeParams{Name: name, ExpiresAt: &expiry, LatencyMode: "ping", CollectionSeconds: 5, ReportSeconds: 5}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.SavePolicy(ctx, store.AlertPolicy{Policy: alertpolicy.Policy{Key: "preparation", Enabled: true, Scope: alertpolicy.Scope{Kind: "all"}}, Name: "preparation", ChannelID: channel.ID, Kind: "expiry", Config: json.RawMessage(`{"days_before":1,"aggregation_seconds":5}`), CooldownSeconds: 300}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Tick(ctx, at.Add(-6*time.Second)); err != nil {
		t.Fatal(err)
	}
	fixture, err := sql.Open("sqlite", db.DatabasePath())
	if err != nil {
		t.Fatal(err)
	}
	defer fixture.Close()
	if _, err := fixture.Exec(`UPDATE notification_deliveries SET payload_json='{"timestamp":"private-render-marker"}' WHERE aggregate_id IS NOT NULL`); err != nil {
		t.Fatal(err)
	}
	if worked, err := svc.DeliverOne(ctx, at); err != nil || !worked {
		t.Fatalf("preparation: %v %v", worked, err)
	}
	groups, err := db.ListNotificationAggregates(ctx, 0, 25)
	if err != nil || len(groups.Aggregates) != 1 || groups.Aggregates[0].Totals.Failed != 2 {
		t.Fatal("fixture did not record a local failure")
	}
	hub := agentgateway.NewHub()
	api := NewWithAlertService(config.Config{EncryptionKey: key}, db, authService, agentgateway.New(db, hub), hub, svc)
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	command := exec.CommandContext(ctx, "node", "../../scripts/aggregate-preparation-failure-ui.cjs")
	command.Env = append(os.Environ(), "AGG_BROWSER_URL="+server.URL, "AGG_BROWSER_PASSWORD="+password, "AGG_BROWSER_GROUP="+groups.Aggregates[0].ID)
	output, err := command.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("local preparation error made an external request")
	}
}
