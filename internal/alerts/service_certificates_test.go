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

func TestCertificateReminderUsesExistingOutboxAndPreservesUnknown(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	received := make(chan Notification, 4)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var n Notification
		if json.NewDecoder(r.Body).Decode(&n) != nil {
			t.Error("invalid fixture notice")
		}
		received <- n
		w.WriteHeader(204)
	}))
	defer receiver.Close()
	svc := New(db, strings.Repeat("s", 32), NewHTTPSender(receiver.Client()), nil, "https://probe.example.test")
	svc.EnableHTTPServiceAlerts(true)
	node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "observer"})
	if err != nil {
		t.Fatal(err)
	}
	channel, err := svc.CreateChannel(ctx, "fixture", "webhook", ChannelConfig{URL: receiver.URL})
	if err != nil {
		t.Fatal(err)
	}
	service, err := db.SaveHTTPService(ctx, store.HTTPService{Name: "literal <alias>", Enabled: true, IntervalSeconds: 30, NodeIDs: []string{node.ID}, Spec: httpcheck.Spec{URL: "https://example.test/private-marker", Method: "GET", StatusCodes: []int{200}, TimeoutMS: 1000, MaxBodyBytes: 1024}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveServiceAlertSettings(ctx, store.ServiceAlertSettings{ServiceID: service.ID, ServiceRevision: service.Revision, ChannelID: channel.ID, CertificateRemindersEnabled: true, CooldownSeconds: 300}); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(time.Second).Truncate(time.Millisecond)
	if err := db.SaveAgentMetadata(ctx, node.ID, protocol.Hello{Capabilities: []string{"http_probe.v1"}}); err != nil {
		t.Fatal(err)
	}
	save := func(offset time.Duration, certificates []httpcheck.Certificate, outcome, class string) {
		t.Helper()
		point := at.Add(offset)
		if err := db.SaveReport(ctx, node.ID, protocol.Report{CapturedAt: point}); err != nil {
			t.Fatal(err)
		}
		task, err := db.CreateHTTPTask(ctx, service.ID, node.ID, point, point)
		if err != nil {
			t.Fatal(err)
		}
		result := httpcheck.Result{TaskID: task.ID, ServiceID: service.ID, Revision: task.Revision, ScheduledAt: point, CompletedAt: point.Add(time.Millisecond), Outcome: outcome, ErrorClass: class, Certificates: certificates}
		if outcome == "success" {
			result.StatusCode = 200
		}
		if err := db.SaveHTTPResult(ctx, node.ID, result, result.CompletedAt); err != nil {
			t.Fatal(err)
		}
	}
	tick := func(offset time.Duration) {
		t.Helper()
		if err := svc.Tick(ctx, at.Add(offset)); err != nil {
			t.Fatal(err)
		}
	}
	c := httpcheck.Certificate{SHA256: strings.Repeat("ab", 32), NotBefore: at.Add(-time.Hour), NotAfter: at.Add(6 * 24 * time.Hour), Verified: true}
	save(0, nil, "unobserved", "busy")
	tick(time.Second)
	if items, err := db.ListCertificateReminders(ctx, service.ID, 0, 25); err != nil || len(items) != 0 {
		t.Fatal("missing evidence manufactured warning")
	}
	save(2*time.Second, []httpcheck.Certificate{c}, "success", "")
	tick(3 * time.Second)
	if worked, err := svc.DeliverOne(ctx, at.Add(3*time.Second)); err != nil || !worked {
		t.Fatal("existing queue did not deliver certificate reminder", err)
	}
	first := <-received
	if first.Kind != "service_certificate" || first.ServiceID != service.ID || first.Title != "MyProbe 证书到期提醒" || !strings.Contains(first.Message, "7 天提醒档") || strings.Contains(first.Message, "private-marker") {
		t.Fatal("certificate notice contract")
	}
	save(4*time.Second, nil, "unobserved", "busy")
	tick(5 * time.Second)
	unknown, err := db.Incident(ctx, first.IncidentID)
	if err != nil || unknown.State != "firing" || !unknown.ObservationStale {
		t.Fatal("unknown certified renewal")
	}
	svc.EnableHTTPServiceAlerts(false)
	tick(6 * time.Second)
	unknown, err = db.Incident(ctx, first.IncidentID)
	if err != nil || unknown.State != "firing" || !unknown.ObservationStale {
		t.Fatal("disabled execution certified renewal")
	}
	svc.EnableHTTPServiceAlerts(true)
	save(7*time.Second, []httpcheck.Certificate{c}, "success", "")
	tick(8 * time.Second)
	if worked, err := svc.DeliverOne(ctx, at.Add(8*time.Second)); err != nil || worked {
		t.Fatal("duplicate current reminder", err)
	}
	next := c
	next.SHA256 = strings.Repeat("cd", 32)
	next.NotAfter = at.Add(12 * time.Hour)
	save(9*time.Second, []httpcheck.Certificate{next}, "success", "")
	tick(10 * time.Second)
	old, err := db.Incident(ctx, first.IncidentID)
	if err != nil || old.State != "resolved" || old.ResolutionReason != "certificate_no_longer_observed" {
		t.Fatal("verified renewal did not end old warning")
	}
	if worked, err := svc.DeliverOne(ctx, at.Add(10*time.Second)); err != nil || !worked {
		t.Fatal("new identity warning missing", err)
	}
	second := <-received
	if second.IncidentID == first.IncidentID || second.State != "firing" || !strings.Contains(second.Message, "1 天提醒档") {
		t.Fatal("new certificate reused old identity or sent recovery")
	}
	if worked, err := svc.DeliverOne(ctx, at.Add(11*time.Second)); err != nil || worked {
		t.Fatal("renewal generated extra notification", err)
	}
}
