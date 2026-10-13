package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/alertpolicy"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestNotificationAggregateManagementAPI(t *testing.T) {
	handler, db, _ := securityTestServer(t)
	defer db.Close()
	root := "/api/v1/admin/notification-aggregates"
	for _, path := range []string{root, root + "/missing?limit=bad", "/api/v1/admin/incidents/missing"} {
		r := httptest.NewRecorder()
		handler.ServeHTTP(r, httptest.NewRequest(http.MethodGet, path, nil))
		if r.Code != 401 || !strings.Contains(r.Header().Get("Cache-Control"), "no-store") {
			t.Fatalf("unauthorized: %d %s", r.Code, r.Body.String())
		}
	}
	login, credentials := loginSecurityRequest(t, handler, "admin", "correct-password", "", "")
	if login.Code != 200 {
		t.Fatal(login.Code)
	}
	cookie := login.Result().Cookies()[0]
	call := func(path string) *httptest.ResponseRecorder {
		return authenticatedRequest(t, handler, cookie, credentials.CSRFToken, http.MethodGet, path, "")
	}
	empty := call(root)
	if empty.Code != 200 || !strings.Contains(empty.Body.String(), `"aggregates":[]`) {
		t.Fatal(empty.Body.String())
	}
	ctx := context.Background()
	channel, err := db.CreateNotificationChannel(ctx, "Context receiver", "webhook", "private-channel-marker")
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 2; n++ {
		if _, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "<img src=x onerror=alert(1)>"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.SaveAlertPolicy(ctx, store.AlertPolicy{Policy: alertpolicy.Policy{Key: "context", Scope: alertpolicy.Scope{Kind: "all"}, Enabled: true}, Name: "Context", ChannelID: channel.ID, Kind: "cpu", Config: json.RawMessage(`{"threshold_percent":90,"aggregation_seconds":5}`), CooldownSeconds: 900}); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Second)
	if err := db.SyncAlertPolicyRules(ctx, at); err != nil {
		t.Fatal(err)
	}
	nodes, err := db.ListNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rules, err := db.ListAlertRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var incidentID string
	for _, rule := range rules {
		for _, node := range nodes {
			if rule.NodeID == node.ID {
				i, err := db.ObserveAlert(ctx, rule, node, store.AlertObservation{At: at, Known: true, Active: true, Message: "private-notification-context", NotificationOrigin: "https://configured.example.com"})
				if err != nil {
					t.Fatal(err)
				}
				incidentID = i.ID
			}
		}
	}
	g, err := db.ClaimAggregate(ctx, at.Add(5*time.Second), time.Minute)
	if err != nil || g == nil {
		t.Fatalf("claim: %+v %v", g, err)
	}
	path := root + "/" + g.ID
	prepared := call(path)
	if prepared.Code != 200 || !strings.Contains(prepared.Body.String(), `"content":null`) || strings.Contains(prepared.Body.String(), `"included":true`) {
		t.Fatalf("prepared: %d %s", prepared.Code, prepared.Body.String())
	}
	send, err := db.PrepareAggregateSend(ctx, g.ID, g.LeaseToken, at.Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CompleteAggregate(ctx, g.ID, g.LeaseToken, at.Add(6*time.Second), store.DeliveryOutcome{Delivered: true}); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{root, path, "/api/v1/admin/incidents/" + incidentID} {
		r := call(endpoint)
		if r.Code != 200 || !strings.Contains(r.Header().Get("Cache-Control"), "no-store") {
			t.Fatalf("read %s: %d %s", endpoint, r.Code, r.Body.String())
		}
		for _, secret := range []string{"private-channel-marker", g.LeaseToken, send.IdempotencyKey, "lease_token", "lease_until", "idempotency_key", "config_encrypted", "payload_json"} {
			if strings.Contains(r.Body.String(), secret) {
				t.Fatalf("private metadata exposed: %s", secret)
			}
		}
	}
	result := call(path)
	var page store.AggregateContext
	if err := json.Unmarshal(result.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Totals.Delivered != 2 || page.Attempts[0].Content.SnapshotID == "" || !strings.HasPrefix(page.Attempts[0].Content.GroupPath, "https://configured.example.com/admin?") {
		t.Fatalf("context: %+v", page)
	}
	for _, endpoint := range []string{root + "?limit=0", root + "?limit=101", root + "?before=-1", root + "?before=9223372036854775808", root + "?limit=1&limit=2", path + "?limit=1&limit=2", path + "?before=-1", path + "?before=no", root + "/bad%20id", "/api/v1/admin/incidents/bad%20id"} {
		r := call(endpoint)
		if r.Code != 400 || !strings.Contains(r.Header().Get("Cache-Control"), "no-store") {
			t.Fatalf("invalid %s: %d %s", endpoint, r.Code, r.Body.String())
		}
	}
	for _, endpoint := range []string{root + "/missing", "/api/v1/admin/incidents/missing"} {
		if r := call(endpoint); r.Code != 404 {
			t.Fatalf("missing: %d %s", r.Code, r.Body.String())
		}
	}
	public := httptest.NewRecorder()
	handler.ServeHTTP(public, httptest.NewRequest(http.MethodGet, "/api/v1/public/nodes", nil))
	for _, secret := range []string{g.ID, send.IdempotencyKey, "snapshot_id", "private-notification-context", "private-channel-marker"} {
		if strings.Contains(public.Body.String(), secret) {
			t.Fatal("notification context entered public API")
		}
	}
}

func TestNotificationContextStorageErrorsStayPrivate(t *testing.T) {
	handler, db, path := securityTestServer(t)
	defer db.Close()
	login, credentials := loginSecurityRequest(t, handler, "admin", "correct-password", "", "")
	if login.Code != 200 {
		t.Fatal(login.Code)
	}
	cookie := login.Result().Cookies()[0]
	broken, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer broken.Close()
	// Only this disposable test database is changed. Auth/session tables remain
	// intact, so errors exercise the context handlers rather than authentication.
	for _, table := range []string{"notification_aggregates", "alert_incidents"} {
		if _, err := broken.Exec("DROP TABLE " + table); err != nil {
			t.Fatal(err)
		}
	}
	for _, endpoint := range []string{"/api/v1/admin/notification-aggregates", "/api/v1/admin/notification-aggregates/missing", "/api/v1/admin/incidents/missing"} {
		r := authenticatedRequest(t, handler, cookie, credentials.CSRFToken, http.MethodGet, endpoint, "")
		if r.Code != 500 || !strings.Contains(r.Header().Get("Cache-Control"), "no-store") {
			t.Fatalf("storage error: %d %s", r.Code, r.Body.String())
		}
		for _, secret := range []string{"no such table", "SQL", "database", path, "notification_aggregates", "alert_incidents"} {
			if strings.Contains(r.Body.String(), secret) {
				t.Fatalf("storage detail exposed: %s", r.Body.String())
			}
		}
	}
}
