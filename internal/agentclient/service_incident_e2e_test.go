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
	"github.com/zhengyifei200112-collab/myprobe/internal/notificationlink"
	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
	"github.com/zhengyifei200112-collab/myprobe/internal/scheduler"
	"github.com/zhengyifei200112-collab/myprobe/internal/servicehealth"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestServiceIncidentThroughRealAgent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
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
	secondNode, secondToken, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "Second service observer"})
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
	service, err := db.SaveHTTPService(ctx, store.HTTPService{Name: "Lifecycle website", Enabled: true, IntervalSeconds: 30, NodeIDs: []string{node.ID, secondNode.ID}, Spec: httpcheck.Spec{URL: targetURL, Method: "GET", StatusCodes: []int{200}, TimeoutMS: 1000, MaxBodyBytes: 1024}})
	if err != nil {
		t.Fatal(err)
	}
	var secondClient *Client
	var secondStop context.CancelFunc
	var secondDone chan struct{}
	startSecond := func(allowed bool) {
		t.Helper()
		cfg := agentConfig
		cfg.ServerURL = server.URL
		cfg.CollectionPeriod, cfg.ReportPeriod = time.Second, time.Second
		cfg.Token = secondToken
		if !allowed {
			cfg.HTTPPrivateCIDRs = nil
			cfg.HTTPAdditionalPorts = nil
		}
		secondClient, err = New(cfg, collector.New(collector.Config{}), logger)
		if err != nil {
			t.Fatal(err)
		}
		var runCtx context.Context
		runCtx, secondStop = context.WithCancel(ctx)
		secondDone = make(chan struct{})
		current, done := secondClient, secondDone
		go func() { defer close(done); _ = current.Run(runCtx) }()
	}
	stopSecond := func() {
		if secondClient == nil {
			return
		}
		secondStop()
		secondClient.clearConnection(nil)
		select {
		case <-secondDone:
		case <-time.After(10 * time.Second):
			t.Error("second observer did not stop")
		}
		secondClient = nil
	}
	startSecond(false)
	defer stopSecond()
	// Configure the live destination through a cross-database portable import.
	// The source has no runtime and contributes only safe aliases/parameters.
	source, err := store.Open(ctx, filepath.Join(t.TempDir(), "portable-source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	sourceNode, _, err := source.CreateNode(ctx, store.CreateNodeParams{Name: "source observer"})
	if err != nil {
		t.Fatal(err)
	}
	sourceService, err := source.SaveHTTPService(ctx, store.HTTPService{Name: "portable source website", Enabled: true, IntervalSeconds: 30, NodeIDs: []string{sourceNode.ID}, Spec: httpcheck.Spec{URL: "http://example.test/source-only", Method: "GET", StatusCodes: []int{200}, TimeoutMS: 1000, MaxBodyBytes: 1024}})
	if err != nil {
		t.Fatal(err)
	}
	sourceChannel, err := source.CreateNotificationChannel(ctx, "portable source channel", "webhook", "synthetic source configuration")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.SaveServiceAlertSettings(ctx, store.ServiceAlertSettings{ServiceID: sourceService.ID, ServiceRevision: sourceService.Revision, Enabled: true, ChannelID: sourceChannel.ID, CooldownSeconds: 300}); err != nil {
		t.Fatal(err)
	}
	bundle, err := source.ExportServiceAlerts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mapping := store.ServiceAlertImportMapping{Services: map[string]string{sourceService.ID: service.ID}, Channels: map[string]string{sourceChannel.ID: channel.ID}}
	preview, err := db.PreviewServiceAlertImport(ctx, bundle, mapping)
	if err != nil {
		t.Fatal(err)
	}
	imported, err := db.ApplyServiceAlertImport(ctx, "live-service-import", bundle, mapping, preview.Digest)
	if err != nil || imported.Created != 1 || imported.Updated != 0 {
		t.Fatal("live service alert import failed")
	}
	replayed, err := db.ApplyServiceAlertImport(ctx, "live-service-import", bundle, mapping, preview.Digest)
	if err != nil || !replayed.Replayed {
		t.Fatal("live service alert import replay failed")
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
	loginBody, _ := json.Marshal(map[string]string{"username": "service-admin", "password": password})
	login := httptest.NewRecorder()
	server.Config.Handler.ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(string(loginBody))))
	if login.Code != 200 || len(login.Result().Cookies()) == 0 {
		t.Fatal("health fixture login failed")
	}
	cookie := login.Result().Cookies()[0]
	readHealth := func() (servicehealth.Summary, string, string) {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/service-monitors/"+service.ID+"/health", nil)
		request.AddCookie(cookie)
		response := httptest.NewRecorder()
		server.Config.Handler.ServeHTTP(response, request)
		if response.Code != 200 || strings.Contains(response.Body.String(), targetURL) {
			t.Fatal("private service health read failed")
		}
		var value struct {
			Health    servicehealth.Summary `json:"health"`
			Observers []struct {
				NodeID     string `json:"node_id"`
				Reason     string `json:"reason"`
				ErrorClass string `json:"error_class"`
			} `json:"observers"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		for _, o := range value.Observers {
			if o.NodeID == secondNode.ID {
				return value.Health, o.ErrorClass, o.Reason
			}
		}
		t.Fatal("second observer absent")
		return value.Health, "", ""
	}
	waitHealth := func(state string, success, failure, unknown int, class, reason string) {
		t.Helper()
		for {
			h, c, r := readHealth()
			if h.State == state && h.Success == success && h.Failure == failure && h.Unknown == unknown && (class == "" || c == class) && (reason == "" || r == reason) {
				return
			}
			select {
			case <-ctx.Done():
				t.Fatalf("health transition missing: %+v class=%s reason=%s", h, c, r)
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	healthBrowser := func(phase string) {
		t.Helper()
		if os.Getenv("MYPROBE_TEST_HTTP_BROWSER") != "1" {
			return
		}
		cmd := exec.CommandContext(ctx, "node", "../../scripts/service-health-live-ui.cjs")
		cmd.Env = append(os.Environ(), "SERVICE_HEALTH_ORIGIN="+origin, "SERVICE_HEALTH_PASSWORD="+password, "SERVICE_HEALTH_ID="+service.ID, "SERVICE_HEALTH_PHASE="+phase)
		output, err := cmd.CombinedOutput()
		t.Log(string(output))
		if err != nil {
			t.Fatal(err)
		}
	}
	var fault alerts.Notification
	select {
	case fault = <-notices:
	case <-ctx.Done():
		t.Fatal("real service fault notification missing")
	}
	if fault.State != "firing" || fault.Kind != "service_http" || fault.ServiceID != service.ID || fault.IncidentID == "" || fault.IncidentPath != origin+"/admin?incident="+fault.IncidentID {
		t.Fatal("fault notice identity")
	}
	if fault.ServiceDiagnosticPath != notificationlink.ServiceDiagnosticAt(origin, service.ID, node.ID, fault.Timestamp) {
		t.Fatal("fault diagnostic context")
	}
	nodes, err := db.ListNodes(ctx)
	if err != nil || len(nodes) != 2 || !gateway.SupportsHTTP(node.ID) || !gateway.SupportsHTTP(secondNode.ID) {
		t.Fatal("host was not online during service fault")
	}
	waitHealth("degraded", 0, 1, 1, "policy_denied", "unobserved")
	healthBrowser("degraded")
	targetStatus.Store(200)
	var recovery alerts.Notification
	select {
	case recovery = <-notices:
	case <-ctx.Done():
		t.Fatal("real service recovery notification missing")
	}
	waitHealth("unknown", 1, 0, 1, "policy_denied", "unobserved")
	healthBrowser("unknown")
	// A real restart changes only the second Agent's local target allowance.
	stopSecond()
	startSecond(true)
	waitHealth("healthy", 2, 0, 0, "", "")
	healthBrowser("healthy")
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
		cmd := exec.CommandContext(ctx, "node", "../../scripts/service-diagnostics-ui.cjs")
		cmd.Env = append(os.Environ(), "DIAGNOSTIC_URL="+fault.ServiceDiagnosticPath, "DIAGNOSTIC_USERNAME=service-admin", "DIAGNOSTIC_PASSWORD="+password)
		output, err := cmd.CombinedOutput()
		t.Log(string(output))
		if err != nil {
			t.Fatal(err)
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
	stopSecond()
	waitHealth("unknown", 1, 0, 1, "", "agent_unavailable")
}
