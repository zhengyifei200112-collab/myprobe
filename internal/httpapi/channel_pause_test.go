package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
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
	"github.com/zhengyifei200112-collab/myprobe/internal/alertpolicy"
	"github.com/zhengyifei200112-collab/myprobe/internal/alerts"
	"github.com/zhengyifei200112-collab/myprobe/internal/auth"
	"github.com/zhengyifei200112-collab/myprobe/internal/config"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

type channelPauseFixture struct {
	db                                                         *store.Store
	handler                                                    http.Handler
	calls                                                      *atomic.Int32
	channel, incident, delivery, aggregate, template, password string
}

func newChannelPauseFixture(t *testing.T) channelPauseFixture {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "pause.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	key, password := rand.Text()+rand.Text(), rand.Text()
	authService := auth.New(db, time.Hour)
	if _, err := authService.Bootstrap(ctx, "pause-admin", password); err != nil {
		t.Fatal(err)
	}
	calls := &atomic.Int32{}
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(429)
		_, _ = w.Write([]byte("private-provider-marker"))
	}))
	t.Cleanup(receiver.Close)
	svc := alerts.New(db, key, alerts.NewHTTPSender(receiver.Client()), nil)
	c, err := svc.CreateChannel(ctx, "Rate limited channel", "webhook", alerts.ChannelConfig{URL: receiver.URL})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(-2 * time.Minute)
	n, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "Paused node"})
	if err != nil {
		t.Fatal(err)
	}
	expires := at.Add(time.Hour)
	if _, err := db.UpdateNode(ctx, n.ID, store.UpdateNodeParams{Name: n.Name, ExpiresAt: &expires, LatencyMode: "ping", CollectionSeconds: 5, ReportSeconds: 5}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SavePolicy(ctx, store.AlertPolicy{Policy: alertpolicy.Policy{Key: "pause", Enabled: true, Scope: alertpolicy.Scope{Kind: "all"}}, Name: "pause", Kind: "expiry", ChannelID: c.ID, Config: json.RawMessage(`{"days_before":1,"aggregation_seconds":5}`), CooldownSeconds: 300}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Tick(ctx, at); err != nil {
		t.Fatal(err)
	}
	rule, err := svc.CreateRule(ctx, n.ID, c.ID, "cpu", alerts.RuleConfig{ThresholdPercent: 90}, 300)
	if err != nil {
		t.Fatal(err)
	}
	i, err := db.ObserveAlert(ctx, rule, n, store.AlertObservation{At: at, Known: true, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	var delivery string
	for j := 0; j < 26; j++ {
		now := at.Add(time.Duration(j) * 2 * time.Second)
		d, err := db.ClaimDelivery(ctx, now, time.Minute)
		if err != nil || d == nil {
			t.Fatal("claim fixture")
		}
		delivery = d.ID
		if err := db.CompleteNotificationChannelTest(ctx, c.ID, store.DeliveryOutcome{ChannelRevision: c.DeliveryRevision, ChannelRetryAt: now.Add(time.Second), ErrorClass: "http_rate_limited"}, now); err != nil {
			t.Fatal(err)
		}
		if err := db.CheckDeliveryLease(ctx, d.ID, d.LeaseToken, now, c.DeliveryRevision); !errors.Is(err, store.ErrDeliveryDeferred) {
			t.Fatal(err)
		}
	}
	if err := svc.TestChannel(ctx, c.ID, time.Now().UTC()); err == nil {
		t.Fatal("receiver did not reject test")
	}
	if worked, err := svc.DeliverOne(ctx, time.Now().UTC()); err != nil || worked || calls.Load() != 1 {
		t.Fatal("paused queue bypassed gate")
	}
	groups, err := db.ListNotificationAggregates(ctx, 0, 25)
	if err != nil || len(groups.Aggregates) != 1 {
		t.Fatal("aggregate fixture")
	}
	template, err := db.SaveNotificationTemplate(ctx, "", "pause template", "all", "test", "test")
	if err != nil {
		t.Fatal(err)
	}
	hub := agentgateway.NewHub()
	api := NewWithAlertService(config.Config{EncryptionKey: key}, db, authService, agentgateway.New(db, hub), hub, svc)
	return channelPauseFixture{db: db, handler: api.Handler(), calls: calls, channel: c.ID, incident: i.ID, delivery: delivery, aggregate: groups.Aggregates[0].ID, template: template.ID, password: password}
}

func TestChannelPauseManagementPrivacyAndHistoryPagination(t *testing.T) {
	f := newChannelPauseFixture(t)
	root := "/api/v1/admin/notification-channels"
	attemptPath := "/api/v1/admin/notification-deliveries/" + f.delivery + "/attempts"
	for _, path := range []string{root, attemptPath + "?before=invalid"} {
		r := httptest.NewRecorder()
		f.handler.ServeHTTP(r, httptest.NewRequest("GET", path, nil))
		if r.Code != 401 || !strings.Contains(r.Header().Get("Cache-Control"), "no-store") {
			t.Fatal("read lacks auth/no-store")
		}
	}
	login, credentials := loginSecurityRequest(t, f.handler, "pause-admin", f.password, "", "")
	if login.Code != 200 {
		t.Fatal(login.Code)
	}
	cookie := login.Result().Cookies()[0]
	call := func(method, path, body string) *httptest.ResponseRecorder {
		return authenticatedRequest(t, f.handler, cookie, credentials.CSRFToken, method, path, body)
	}
	for _, path := range []string{root, "/api/v1/admin/incidents/" + f.incident + "/deliveries", "/api/v1/admin/notification-aggregates/" + f.aggregate} {
		r := call("GET", path, "")
		if r.Code != 200 || !strings.Contains(r.Body.String(), "delivery_paused_until") {
			t.Fatalf("pause visibility: %d %s", r.Code, r.Body.String())
		}
		for _, secret := range []string{"private-provider-marker", "config_encrypted", "delivery_revision", "DeliveryRevision", "send_authorized", "SendAuthorized", "lease_token", "idempotency_key"} {
			if strings.Contains(r.Body.String(), secret) {
				t.Fatalf("private field exposed: %s", secret)
			}
		}
	}
	pageResponse := call("GET", attemptPath, "")
	var page store.DeliveryAttemptPage
	if err := json.Unmarshal(pageResponse.Body.Bytes(), &page); err != nil || pageResponse.Code != 200 || len(page.Attempts) != 25 || page.Attempts[0].Number != 2 || page.Attempts[24].Number != 26 || page.NextBefore != 2 {
		t.Fatalf("first page: %s", pageResponse.Body.String())
	}
	older := call("GET", attemptPath+"?before=2&limit=25", "")
	if err := json.Unmarshal(older.Body.Bytes(), &page); err != nil || len(page.Attempts) != 1 || page.Attempts[0].Number != 1 || page.NextBefore != 0 {
		t.Fatalf("older page: %s", older.Body.String())
	}
	for _, query := range []string{"before=-1", "before=x", "before=999999999999999999999", "before=0&before=1", "limit=0", "limit=101", "limit=1&limit=2"} {
		if r := call("GET", attemptPath+"?"+query, ""); r.Code != 400 {
			t.Fatal("invalid pagination accepted")
		}
	}
	denied := authenticatedRequest(t, f.handler, cookie, "wrong-csrf", "POST", root+"/"+f.channel+"/test", "")
	if denied.Code != 403 {
		t.Fatal("test skipped CSRF")
	}
	for _, path := range []string{root + "/" + f.channel + "/test", "/api/v1/admin/notification-templates/" + f.template + "/test"} {
		r := call("POST", path, `{"channel_id":"`+f.channel+`"}`)
		if r.Code != 429 || !strings.Contains(r.Body.String(), `"code":"channel_rate_limited"`) {
			t.Fatalf("test bypass: %d %s", r.Code, r.Body.String())
		}
	}
	public := httptest.NewRecorder()
	f.handler.ServeHTTP(public, httptest.NewRequest("GET", "/api/v1/public/nodes", nil))
	if public.Code != 200 || strings.Contains(public.Body.String(), "delivery_paused_until") || strings.Contains(public.Body.String(), "attempt_sequence") {
		t.Fatal("public pause leak")
	}
	if f.calls.Load() != 1 {
		t.Fatal("paused API test made an external request")
	}
}

func TestLiveChannelPauseBrowser(t *testing.T) {
	if os.Getenv("MYPROBE_TEST_AGGREGATION_BROWSER") != "1" {
		t.Skip("requires Playwright/Edge; opt in with MYPROBE_TEST_AGGREGATION_BROWSER=1")
	}
	f := newChannelPauseFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	server := httptest.NewServer(f.handler)
	defer server.Close()
	cmd := exec.CommandContext(ctx, "node", "../../scripts/channel-pause-ui.cjs")
	cmd.Env = append(os.Environ(), "PAUSE_BROWSER_URL="+server.URL, "PAUSE_BROWSER_PASSWORD="+f.password, "PAUSE_BROWSER_INCIDENT="+f.incident, "PAUSE_BROWSER_AGGREGATE="+f.aggregate)
	out, err := cmd.CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatal(err)
	}
	if f.calls.Load() != 1 {
		t.Fatal("browser test bypassed active channel wait")
	}
	d, err := f.db.Delivery(ctx, f.delivery)
	if err != nil || d.AttemptCount != 0 || d.AttemptSequence != 26 {
		t.Fatal("browser test consumed queue budget")
	}
}
