package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/auth"
	"github.com/zhengyifei200112-collab/myprobe/internal/config"
	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"github.com/zhengyifei200112-collab/myprobe/internal/servicehealth"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestServiceHealthPrivateSummaryUsesCurrentSessionsAndRevision(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	password := rand.Text() + rand.Text()
	authentication := auth.New(db, time.Hour)
	if _, err := authentication.Bootstrap(ctx, "service-admin", password); err != nil {
		t.Fatal(err)
	}
	hub := agentgateway.NewHub()
	gateway := agentgateway.New(db, hub)
	gateway.EnableHTTPProbes(true)
	handler := New(config.Config{EncryptionKey: password, HTTPProbesEnabled: true}, db, authentication, gateway, hub).Handler()
	server := httptest.NewServer(handler)
	defer server.Close()
	first, firstToken, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "first"})
	if err != nil {
		t.Fatal(err)
	}
	second, secondToken, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "second"})
	if err != nil {
		t.Fatal(err)
	}
	connect := func(token string, version int) *websocket.Conn {
		t.Helper()
		endpoint := "/api/v2/agent/ws"
		if version == 1 {
			endpoint = "/api/v1/agent/ws"
		}
		connection, _, err := websocket.Dial(ctx, strings.Replace(server.URL, "http", "ws", 1)+endpoint, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Bearer " + token}}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { connection.CloseNow() })
		hello, _ := protocol.NewEnvelope(protocol.TypeHello, 1, protocol.Hello{AgentVersion: "fixture", OS: "fixture", Hostname: "fixture", Architecture: "amd64", CollectionSeconds: 5, ReportSeconds: 5, Capabilities: []string{"http_probe.v1"}})
		hello.Version = version
		if err := wsjson.Write(ctx, connection, hello); err != nil {
			t.Fatal(err)
		}
		var reply protocol.Envelope
		if err := wsjson.Read(ctx, connection, &reply); err != nil || reply.Type != protocol.TypeWelcome {
			t.Fatal("handshake failed", err)
		}
		go func() {
			for {
				var incoming protocol.Envelope
				if wsjson.Read(ctx, connection, &incoming) != nil {
					return
				}
			}
		}()
		return connection
	}
	firstConnection := connect(firstToken, 2)
	secondConnection := connect(secondToken, 2)
	defer firstConnection.CloseNow()
	defer secondConnection.CloseNow()
	for _, id := range []string{first.ID, second.ID} {
		if err := db.SaveReport(ctx, id, protocol.Report{CapturedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	service, err := db.SaveHTTPService(ctx, store.HTTPService{Name: "Private service alias", Enabled: true, IntervalSeconds: 30, NodeIDs: []string{first.ID, second.ID}, Spec: httpcheck.Spec{URL: "http://example.test/private-spec-marker", Method: "GET", StatusCodes: []int{200}, TimeoutMS: 1000, MaxBodyBytes: 1024}})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	save := func(id string, when time.Time, outcome string, code int) {
		t.Helper()
		task, err := db.CreateHTTPTask(ctx, service.ID, id, when, when)
		if err != nil {
			t.Fatal(err)
		}
		r := httpcheck.Result{TaskID: task.ID, ServiceID: task.ServiceID, Revision: task.Revision, ScheduledAt: task.ScheduledAt, CompletedAt: when.Add(time.Millisecond), Outcome: outcome, StatusCode: code, DurationMS: 1}
		if outcome == "failure" {
			r.ErrorClass = "status_mismatch"
		}
		if err := db.SaveHTTPResult(ctx, id, r, r.CompletedAt); err != nil {
			t.Fatal(err)
		}
	}
	save(first.ID, at, "failure", 503)
	save(second.ID, at, "success", 200)
	path := "/api/v1/admin/service-monitors/" + service.ID + "/health"
	for _, resource := range []string{path, path + "?limit=bad", "/api/v1/admin/service-monitors/missing/health"} {
		denied := httptest.NewRecorder()
		handler.ServeHTTP(denied, httptest.NewRequest("GET", resource, nil))
		if denied.Code != 401 || !strings.Contains(denied.Header().Get("Cache-Control"), "no-store") {
			t.Fatal("health validation before authentication")
		}
	}
	login, credentials := loginSecurityRequest(t, handler, "service-admin", password, "", "")
	if login.Code != 200 {
		t.Fatal(login.Code)
	}
	cookie := login.Result().Cookies()[0]
	read := func() (servicehealth.Summary, []serviceObserverHealth) {
		t.Helper()
		r := authenticatedRequest(t, handler, cookie, credentials.CSRFToken, "GET", path, "")
		if r.Code != 200 || !strings.Contains(r.Header().Get("Cache-Control"), "no-store") {
			t.Fatal("health read failed", r.Code)
		}
		for _, private := range []string{"example.test", "private-spec-marker", "spec", "capabilities", "last_seen", "token"} {
			if strings.Contains(r.Body.String(), private) {
				t.Fatal("private health scope leaked", private)
			}
		}
		var value struct {
			Health    servicehealth.Summary   `json:"health"`
			Observers []serviceObserverHealth `json:"observers"`
		}
		if err := json.Unmarshal(r.Body.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		return value.Health, value.Observers
	}
	summary, observers := read()
	if summary.State != "degraded" || summary.Success != 1 || summary.Failure != 1 || summary.Unknown != 0 || len(observers) != 2 || summary.CurrentCoverage == nil || *summary.CurrentCoverage != 1 {
		t.Fatal("multi-point failure policy", summary)
	}
	// A v1 replacement advertises the same persisted metadata, but cannot execute HTTP.
	legacy := connect(secondToken, 1)
	defer legacy.CloseNow()
	summary, observers = read()
	if summary.State != "degraded" || summary.Failure != 1 || summary.Unknown != 1 || summary.CurrentCoverage == nil || *summary.CurrentCoverage != .5 {
		t.Fatal("old session evidence counted as current", summary)
	}
	for _, o := range observers {
		if o.NodeID == second.ID && (o.Status != "unknown" || o.Reason != "agent_unavailable" || o.StatusCode != 0) {
			t.Fatal("old result shown as current success")
		}
	}
	save(first.ID, at.Add(time.Nanosecond), "success", 200)
	summary, _ = read()
	if summary.State != "unknown" || summary.Success != 1 || summary.Unknown != 1 {
		t.Fatal("partial success manufactured health", summary)
	}
	replacement := connect(secondToken, 2)
	defer replacement.CloseNow()
	summary, _ = read()
	if summary.State != "healthy" || summary.Success != 2 || summary.CurrentCoverage == nil || *summary.CurrentCoverage != 1 {
		t.Fatal("all observers did not recover", summary)
	}
	list := authenticatedRequest(t, handler, cookie, credentials.CSRFToken, "GET", "/api/v1/admin/service-monitors", "")
	var page struct {
		Services []serviceHealthListItem `json:"services"`
	}
	if list.Code != 200 || json.Unmarshal(list.Body.Bytes(), &page) != nil || len(page.Services) != 1 || page.Services[0].Health.State != "healthy" {
		t.Fatal("overview summary differs from detail")
	}
	actor, err := db.CreateUser(ctx, "maintenance fixture", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateMaintenanceWindow(ctx, store.MaintenanceWindow{Reason: "work", Timezone: "UTC", CreatedBy: actor.ID, NodeIDs: []string{first.ID, second.ID}, StartsAt: time.Now().UTC().Add(-time.Second), EndsAt: time.Now().UTC().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	summary, _ = read()
	if summary.State != "healthy" || summary.Success != 2 {
		t.Fatal("notification maintenance changed check evidence")
	}
	incidents, err := db.ListIncidents(ctx, store.IncidentFilter{Limit: 25})
	if err != nil || len(incidents) != 0 {
		t.Fatal("read-only health created incidents")
	}
	settings, err := db.ServiceAlertSettings(ctx, service.ID)
	if err != nil || settings.Enabled || settings.Revision != 0 {
		t.Fatal("health requires or mutates alert configuration")
	}
	gateway.EnableHTTPProbes(false)
	summary, observers = read()
	if summary.State != "unknown" || summary.Success != 0 || summary.Unknown != 2 || *summary.CurrentCoverage != 0 {
		t.Fatal("disabled execution reused success")
	}
	gateway.EnableHTTPProbes(true)
	service.Name = "Changed alias"
	service, err = db.SaveHTTPService(ctx, service)
	if err != nil {
		t.Fatal(err)
	}
	summary, observers = read()
	if summary.State != "unknown" || summary.Unknown != 2 {
		t.Fatal("previous revision reused", summary)
	}
	for _, o := range observers {
		if o.Reason != "not_observed" || o.TaskID != "" {
			t.Fatal("old revision task exposed")
		}
	}
	for _, resource := range []string{path + "?limit=1", "/api/v1/admin/service-monitors/missing/health"} {
		r := authenticatedRequest(t, handler, cookie, credentials.CSRFToken, "GET", resource, "")
		want := 400
		if strings.Contains(resource, "missing") {
			want = 404
		}
		if r.Code != want {
			t.Fatal("health query contract", r.Code)
		}
	}
	public := httptest.NewRecorder()
	handler.ServeHTTP(public, httptest.NewRequest("GET", "/api/v1/public/nodes", nil))
	if public.Code != 200 || strings.Contains(public.Body.String(), service.ID) || strings.Contains(public.Body.String(), "Private service alias") || strings.Contains(public.Body.String(), "private-spec-marker") {
		t.Fatal("private service summary in public nodes")
	}
}
