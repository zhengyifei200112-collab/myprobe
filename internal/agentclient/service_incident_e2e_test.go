package agentclient

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
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
	"github.com/zhengyifei200112-collab/myprobe/internal/collector"
	"github.com/zhengyifei200112-collab/myprobe/internal/config"
	"github.com/zhengyifei200112-collab/myprobe/internal/httpapi"
	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
	"github.com/zhengyifei200112-collab/myprobe/internal/scheduler"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestServiceIncidentThroughRealAgent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var targetStatus atomic.Int32
	targetStatus.Store(503)
	agentConfig, targetURL := startPrivateTarget(t, false, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(int(targetStatus.Load())) }))
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "lifecycle.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	key, password := rand.Text()+rand.Text(), rand.Text()
	authentication := auth.New(db, time.Hour)
	if _, err := authentication.Bootstrap(ctx, "service-admin", password); err != nil {
		t.Fatal(err)
	}
	node, token, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "Service observer"})
	if err != nil {
		t.Fatal(err)
	}
	notices := make(chan alerts.Notification, 10)
	var noticeCount atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		if strings.Contains(string(raw), targetURL) {
			t.Error("private target in notice")
		}
		var n alerts.Notification
		if err := json.Unmarshal(raw, &n); err != nil {
			t.Error(err)
			return
		}
		noticeCount.Add(1)
		notices <- n
		w.WriteHeader(204)
	}))
	defer receiver.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewUnstartedServer(nil)
	origin := "http://" + server.Listener.Addr().String()
	hub := agentgateway.NewHub()
	gateway := agentgateway.New(db, hub)
	gateway.EnableHTTPProbes(true)
	svc := alerts.New(db, key, alerts.NewHTTPSender(receiver.Client()), logger, origin)
	server.Config.Handler = httpapi.NewWithAlertService(config.Config{EncryptionKey: key, SessionTTL: time.Hour, HTTPProbesEnabled: true}, db, authentication, gateway, hub, svc).Handler()
	server.Start()
	defer server.Close()
	channel, err := svc.CreateChannel(ctx, "Service fixture channel", "webhook", alerts.ChannelConfig{URL: receiver.URL})
	if err != nil {
		t.Fatal(err)
	}
	service, err := db.SaveHTTPService(ctx, store.HTTPService{Name: "Lifecycle website", Enabled: true, IntervalSeconds: 30, NodeIDs: []string{node.ID}, Spec: httpcheck.Spec{URL: targetURL, Method: "GET", StatusCodes: []int{200}, TimeoutMS: 1000, MaxBodyBytes: 1024}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveServiceAlertSettings(ctx, store.ServiceAlertSettings{ServiceID: service.ID, ServiceRevision: service.Revision, Enabled: true, ChannelID: channel.ID, CooldownSeconds: 300}); err != nil {
		t.Fatal(err)
	}
	agentConfig.ServerURL, agentConfig.Token = server.URL, token
	agentConfig.CollectionPeriod, agentConfig.ReportPeriod = time.Second, time.Second
	client, err := New(agentConfig, collector.New(collector.Config{}), logger)
	if err != nil {
		t.Fatal(err)
	}
	agentDone, schedulerDone, alertsDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() { defer close(agentDone); _ = client.Run(ctx) }()
	go func() { defer close(schedulerDone); scheduler.NewHTTP(db, gateway, logger).Run(ctx) }()
	go func() { defer close(alertsDone); svc.Run(ctx) }()
	defer func() {
		cancel()
		client.clearConnection(nil)
		for _, done := range []chan struct{}{agentDone, schedulerDone, alertsDone} {
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Error("service runtime did not stop")
			}
		}
	}()
	var fault alerts.Notification
	select {
	case fault = <-notices:
	case <-ctx.Done():
		t.Fatal("real service fault notification missing")
	}
	if fault.State != "firing" || fault.Kind != "service_http" || fault.ServiceID != service.ID || fault.IncidentID == "" || fault.IncidentPath != origin+"/admin?incident="+fault.IncidentID {
		t.Fatal("fault notice identity")
	}
	nodes, err := db.ListNodes(ctx)
	if err != nil || len(nodes) != 1 || nodes[0].LastSeenAt == nil || time.Since(*nodes[0].LastSeenAt) > 20*time.Second || !gateway.SupportsHTTP(node.ID) {
		t.Fatal("host was not online during service fault")
	}
	targetStatus.Store(200)
	var recovery alerts.Notification
	select {
	case recovery = <-notices:
	case <-ctx.Done():
		t.Fatal("real service recovery notification missing")
	}
	if recovery.State != "resolved" || recovery.IncidentID != fault.IncidentID || recovery.ServiceID != service.ID {
		t.Fatal("recovery did not use original event")
	}
	event, err := db.Incident(ctx, fault.IncidentID)
	if err != nil || event.State != "resolved" || event.ResolutionReason != "recovered" {
		t.Fatal("service event did not recover")
	}
	for {
		jobs, err := db.ListIncidentDeliveries(ctx, event.ID, 0, 25)
		if err != nil {
			t.Fatal(err)
		}
		if len(jobs) == 2 && jobs[0].Status == "delivered" && jobs[1].Status == "delivered" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("existing outbox lifecycle incomplete")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if os.Getenv("MYPROBE_TEST_HTTP_BROWSER") == "1" {
		cmd := exec.CommandContext(ctx, "node", "../../scripts/service-incident-live-ui.cjs")
		cmd.Env = append(os.Environ(), "SERVICE_INCIDENT_URL="+fault.IncidentPath, "SERVICE_INCIDENT_PASSWORD="+password, "SERVICE_INCIDENT_ID="+service.ID)
		output, err := cmd.CombinedOutput()
		t.Log(string(output))
		if err != nil {
			t.Fatal(err)
		}
	}
	if noticeCount.Load() != 2 {
		t.Fatal("service lifecycle duplicated notification")
	}
}
