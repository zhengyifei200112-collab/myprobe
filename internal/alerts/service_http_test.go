package alerts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestServiceHTTPFaultUnknownRecoveryUsesExistingOutbox(t *testing.T) {
	for _, class := range []string{"status_mismatch", "transport"} {
		t.Run(class, func(t *testing.T) { serviceHTTPFaultUnknownRecovery(t, class) })
	}
}

func serviceHTTPFaultUnknownRecovery(t *testing.T, failureClass string) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	received := make(chan Notification, 2)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var n Notification
		if err := json.NewDecoder(r.Body).Decode(&n); err != nil {
			t.Error(err)
		}
		received <- n
		w.WriteHeader(204)
	}))
	defer receiver.Close()
	svc := New(db, strings.Repeat("s", 32), NewHTTPSender(receiver.Client()), nil, "https://probe.example.test")
	svc.EnableHTTPServiceAlerts(true)
	node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "online observer"})
	if err != nil {
		t.Fatal(err)
	}
	channel, err := svc.CreateChannel(ctx, "local", "webhook", ChannelConfig{URL: receiver.URL})
	if err != nil {
		t.Fatal(err)
	}
	service, err := db.SaveHTTPService(ctx, store.HTTPService{Name: "website alias", Enabled: true, IntervalSeconds: 30, NodeIDs: []string{node.ID}, Spec: httpcheck.Spec{URL: "http://example.test/private?opaque=private-marker", Method: "GET", StatusCodes: []int{200}, TimeoutMS: 1000, MaxBodyBytes: 1024}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveServiceAlertSettings(ctx, store.ServiceAlertSettings{ServiceID: service.ID, ServiceRevision: service.Revision, Enabled: true, ChannelID: channel.ID, CooldownSeconds: 300}); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(time.Millisecond)
	if err := db.SaveReport(ctx, node.ID, protocol.Report{CapturedAt: at}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveAgentMetadata(ctx, node.ID, protocol.Hello{AgentVersion: "fixture", Hostname: "fixture", OS: "fixture", Capabilities: []string{"http_probe.v1"}}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Tick(ctx, at); err != nil {
		t.Fatal(err)
	}
	if events, err := db.ListIncidents(ctx, store.IncidentFilter{ServiceID: service.ID, Limit: 25}); err != nil || len(events) != 0 {
		t.Fatal("missing result manufactured fault")
	}
	save := func(when time.Time, outcome, class string, code int) {
		t.Helper()
		task, err := db.CreateHTTPTask(ctx, service.ID, node.ID, when, when)
		if err != nil {
			t.Fatal(err)
		}
		result := httpcheck.Result{TaskID: task.ID, ServiceID: service.ID, Revision: task.Revision, ScheduledAt: when, CompletedAt: when.Add(time.Millisecond), Outcome: outcome, ErrorClass: class, StatusCode: code, DurationMS: 1}
		if err := db.SaveHTTPResult(ctx, node.ID, result, result.CompletedAt); err != nil {
			t.Fatal(err)
		}
	}
	status := 503
	if failureClass == "transport" {
		status = 0
	}
	save(at.Add(time.Second), "failure", failureClass, status)
	if err := svc.Tick(ctx, at.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	events, err := db.ListIncidents(ctx, store.IncidentFilter{ServiceID: service.ID, Limit: 25})
	if err != nil || len(events) != 1 || events[0].State != "firing" || events[0].ServiceID != service.ID {
		t.Fatal("service fault missing")
	}
	original := events[0]
	if worked, err := svc.DeliverOne(ctx, at.Add(2*time.Second)); err != nil || !worked {
		t.Fatal("existing outbox did not deliver")
	}
	notice := <-received
	if notice.ServiceID != service.ID || notice.Kind != "service_http" || notice.IncidentID != original.ID || !strings.Contains(notice.IncidentPath, original.ID) || strings.Contains(notice.Message, "private-marker") {
		t.Fatal("service notice identity/privacy")
	}
	if failureClass == "transport" && !strings.Contains(notice.Message, "连接中断或响应不完整") {
		t.Fatal("transport failure reason missing from durable notification")
	}
	save(at.Add(3*time.Second), "unobserved", "unsupported", 0)
	if err := svc.Tick(ctx, at.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	unknown, err := db.Incident(ctx, original.ID)
	if err != nil || unknown.State != "firing" || !unknown.ObservationStale {
		t.Fatal("unsupported manufactured recovery")
	}
	svc.EnableHTTPServiceAlerts(false)
	save(at.Add(5*time.Second), "success", "", 200)
	if err := svc.Tick(ctx, at.Add(6*time.Second)); err != nil {
		t.Fatal(err)
	}
	unknown, err = db.Incident(ctx, original.ID)
	if err != nil || unknown.State != "firing" || !unknown.ObservationStale {
		t.Fatal("disabled execution manufactured recovery")
	}
	svc.EnableHTTPServiceAlerts(true)
	if err := svc.Tick(ctx, at.Add(7*time.Second)); err != nil {
		t.Fatal(err)
	}
	restored, err := db.Incident(ctx, original.ID)
	if err != nil || restored.State != "resolved" || restored.ResolutionReason != "recovered" || restored.ObservationStale {
		t.Fatal("fresh service success did not recover")
	}
	if worked, err := svc.DeliverOne(ctx, at.Add(7*time.Second)); err != nil || !worked {
		t.Fatal("recovery missing")
	}
	recovery := <-received
	if recovery.IncidentID != original.ID || recovery.State != "resolved" {
		t.Fatal("recovery changed identity")
	}
	if policies, err := db.ListAlertPolicies(ctx); err != nil || len(policies) != 0 {
		t.Fatal("service alert migrated into host scope")
	}
}

func TestServiceHTTPRequiresFreshResultAndCurrentObserver(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "observer"})
	if err != nil {
		t.Fatal(err)
	}
	channel, err := db.CreateNotificationChannel(ctx, "fixture", "webhook", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	service, err := db.SaveHTTPService(ctx, store.HTTPService{Name: "alias", Enabled: true, IntervalSeconds: 30, NodeIDs: []string{node.ID}, Spec: httpcheck.Spec{URL: "http://example.test", Method: "GET", StatusCodes: []int{200}, TimeoutMS: 1000, MaxBodyBytes: 1024}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveServiceAlertSettings(ctx, store.ServiceAlertSettings{ServiceID: service.ID, ServiceRevision: service.Revision, Enabled: true, ChannelID: channel.ID, CooldownSeconds: 300}); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(time.Second).Truncate(time.Millisecond)
	if _, err := db.PrepareAlertPolicyRules(ctx, at); err != nil {
		t.Fatal(err)
	}
	rules, err := db.ListAlertRules(ctx)
	if err != nil || len(rules) != 1 {
		t.Fatal("service rule missing", err)
	}
	task, err := db.CreateHTTPTask(ctx, service.ID, node.ID, at, at)
	if err != nil {
		t.Fatal(err)
	}
	result := httpcheck.Result{TaskID: task.ID, ServiceID: service.ID, Revision: task.Revision, ScheduledAt: at, CompletedAt: at.Add(time.Millisecond), Outcome: "success", StatusCode: 200, DurationMS: 1}
	if err := db.SaveHTTPResult(ctx, node.ID, result, result.CompletedAt); err != nil {
		t.Fatal(err)
	}
	svc := New(db, strings.Repeat("s", 32), nil, nil)
	for _, mode := range []string{"fresh", "freshness-boundary", "expired", "offline", "no-last-seen", "future-host-clock", "no-metadata", "old-agent", "replaced-session", "execution-disabled"} {
		t.Run(mode, func(t *testing.T) {
			now := at.Add(time.Second)
			if mode == "freshness-boundary" {
				now = at.Add(90 * time.Second)
			}
			if mode == "expired" {
				now = at.Add(90*time.Second + time.Nanosecond)
			}
			n := node
			n.LastSeenAt = &now
			n.Agent = &store.AgentMetadata{Capabilities: []string{"http_probe.v1"}}
			svc.EnableHTTPServiceAlerts(mode != "execution-disabled")
			svc.SetHTTPAgentAvailability(func(string) bool { return mode != "replaced-session" })
			switch mode {
			case "offline":
				seen := now.Add(-time.Duration(max(n.ReportSeconds*3, 20))*time.Second - time.Nanosecond)
				n.LastSeenAt = &seen
			case "no-last-seen":
				n.LastSeenAt = nil
			case "future-host-clock":
				seen := now.Add(30*time.Second + time.Nanosecond)
				n.LastSeenAt = &seen
			case "no-metadata":
				n.Agent = nil
			case "old-agent":
				n.Agent = &store.AgentMetadata{Capabilities: []string{"latency"}}
			}
			active, _, known, err := svc.evaluateHTTPService(ctx, rules[0], n, now)
			want := mode == "fresh" || mode == "freshness-boundary"
			if err != nil || active || known != want {
				t.Fatalf("known=%v active=%v wantKnown=%v err=%v", known, active, want, err)
			}
		})
	}
	svc.EnableHTTPServiceAlerts(true)
	svc.SetHTTPAgentAvailability(func(string) bool { return true })
	now := at.Add(31 * time.Second)
	node.LastSeenAt, node.Agent = &now, &store.AgentMetadata{Capabilities: []string{"http_probe.v1"}}
	if _, err := db.CreateHTTPTask(ctx, service.ID, node.ID, at.Add(30*time.Second), at.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, _, known, err := svc.evaluateHTTPService(ctx, rules[0], node, now); err != nil || known {
		t.Fatal("new pending slot reused old success", err)
	}
}
