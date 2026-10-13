package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/auth"
	"github.com/zhengyifei200112-collab/myprobe/internal/config"
	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestServiceAlertingAuthorizationRevisionsAndPrivacy(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	password := rand.Text()
	authentication := auth.New(db, time.Hour)
	if _, err := authentication.Bootstrap(ctx, "service-admin", password); err != nil {
		t.Fatal(err)
	}
	node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "observer"})
	if err != nil {
		t.Fatal(err)
	}
	service, err := db.SaveHTTPService(ctx, store.HTTPService{Name: "alias", Enabled: true, IntervalSeconds: 30, NodeIDs: []string{node.ID}, Spec: httpcheck.Spec{URL: "http://example.test/private-marker", Method: "GET", StatusCodes: []int{200}, TimeoutMS: 1000, MaxBodyBytes: 1024}})
	if err != nil {
		t.Fatal(err)
	}
	channel, err := db.CreateNotificationChannel(ctx, "channel", "webhook", "private-config-marker")
	if err != nil {
		t.Fatal(err)
	}
	hub := agentgateway.NewHub()
	api := New(config.Config{EncryptionKey: password, HTTPProbesEnabled: true}, db, authentication, agentgateway.New(db, hub), hub)
	handler := api.Handler()
	root := "/api/v1/admin/service-monitors/" + service.ID
	for _, path := range []string{root + "/alerting", root + "/incidents?before=invalid", root + "/certificate-reminders?before=invalid"} {
		r := httptest.NewRecorder()
		handler.ServeHTTP(r, httptest.NewRequest("GET", path, nil))
		if r.Code != 401 || !strings.Contains(r.Header().Get("Cache-Control"), "no-store") {
			t.Fatal("private read validation preceded authentication")
		}
	}
	login, credentials := loginSecurityRequest(t, handler, "service-admin", password, "", "")
	if login.Code != 200 {
		t.Fatal(login.Code)
	}
	cookie := login.Result().Cookies()[0]
	call := func(method, path, body string) *httptest.ResponseRecorder {
		return authenticatedRequest(t, handler, cookie, credentials.CSRFToken, method, path, body)
	}
	r := call("GET", root+"/alerting", "")
	var initial struct {
		Alerting store.ServiceAlertSettings `json:"alerting"`
	}
	if json.Unmarshal(r.Body.Bytes(), &initial) != nil || r.Code != 200 || initial.Alerting.Revision != 0 || initial.Alerting.ServiceRevision != service.Revision {
		t.Fatal("initial settings missing")
	}
	body := fmt.Sprintf(`{"service_revision":%d,"revision":0,"channel_id":%q,"enabled":true,"duration_seconds":0,"recovery_seconds":0,"repeat_seconds":0,"cooldown_seconds":300}`, service.Revision, channel.ID)
	csrf := authenticatedRequest(t, handler, cookie, "invalid", "PUT", root+"/alerting", body)
	if csrf.Code != 403 {
		t.Fatal("bad CSRF accepted")
	}
	if r := call("PUT", root+"/alerting", body); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r := call("PUT", root+"/alerting", body); r.Code != 409 {
		t.Fatal("stale write accepted")
	}
	for _, invalid := range []string{`{}`, `{"enabled":true,"target_url":"private-marker"}`, body + ` {}`, strings.Replace(body, `"revision":0`, `"revision":9223372036854775808`, 1), strings.Replace(body, `"cooldown_seconds":300`, `"cooldown_seconds":1`, 1)} {
		r := call("PUT", root+"/alerting", invalid)
		if r.Code != 400 || strings.Contains(r.Body.String(), "private-marker") {
			t.Fatal("invalid write contract")
		}
	}
	at := time.Now().UTC()
	if _, err := db.PrepareAlertPolicyRules(ctx, at); err != nil {
		t.Fatal(err)
	}
	rules, err := db.ListAlertRules(ctx)
	if err != nil || len(rules) != 1 {
		t.Fatal(err)
	}
	event, err := db.ObserveAlert(ctx, rules[0], node, store.AlertObservation{At: at, Known: true, Active: true, Message: "safe classified failure"})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{root + "/alerting", root + "/incidents", root + "/certificate-reminders", "/api/v1/admin/incidents/" + event.ID} {
		r := call("GET", path, "")
		if r.Code != 200 || !strings.Contains(r.Header().Get("Cache-Control"), "no-store") {
			t.Fatal("protected service read")
		}
		for _, private := range []string{"private-marker", "private-config-marker", "rule_snapshot", "spec", "config_encrypted"} {
			if strings.Contains(r.Body.String(), private) {
				t.Fatal("private service data in event/settings")
			}
		}
	}
	r = call("GET", root+"/incidents", "")
	if !strings.Contains(r.Body.String(), `"service_id":"`+service.ID+`"`) {
		t.Fatal("service identity omitted")
	}
	for _, query := range []string{"before=-1", "limit=101", "before=1&before=2"} {
		if r := call("GET", root+"/incidents?"+query, ""); r.Code != 400 {
			t.Fatal("bad service pagination")
		}
		if r := call("GET", root+"/certificate-reminders?"+query, ""); r.Code != 400 {
			t.Fatal("bad certificate pagination")
		}
	}
	if r := call("GET", "/api/v1/admin/alert-rules", ""); r.Code != 200 || r.Body.String() != `{"rules":[]}` {
		t.Fatal("service execution rules exposed as host editor rows")
	}
	public := httptest.NewRecorder()
	handler.ServeHTTP(public, httptest.NewRequest("GET", "/api/v1/public/nodes", nil))
	if public.Code != 200 || strings.Contains(public.Body.String(), "service_id") || strings.Contains(public.Body.String(), "private-marker") {
		t.Fatal("public service alert data leaked")
	}
	service.Spec.URL = "https://example.test/private-marker"
	service, err = db.SaveHTTPService(ctx, service)
	if err != nil {
		t.Fatal(err)
	}
	certificateBody := fmt.Sprintf(`{"service_revision":%d,"revision":1,"channel_id":%q,"enabled":false,"certificate_reminders_enabled":true,"duration_seconds":0,"recovery_seconds":0,"repeat_seconds":0,"cooldown_seconds":300}`, service.Revision, channel.ID)
	if r := authenticatedRequest(t, handler, cookie, "invalid", "PUT", root+"/alerting", certificateBody); r.Code != 403 {
		t.Fatal("certificate write bypassed CSRF")
	}
	if r := call("PUT", root+"/alerting", certificateBody); r.Code != 200 {
		t.Fatal("certificate-only write rejected", r.Code)
	}
	legacyBody := strings.Replace(strings.Replace(certificateBody, `"revision":1`, `"revision":2`, 1), `"certificate_reminders_enabled":true,`, "", 1)
	if r := call("PUT", root+"/alerting", legacyBody); r.Code != 200 {
		t.Fatal("legacy client write rejected", r.Code)
	}
	settings, err := db.ServiceAlertSettings(ctx, service.ID)
	if err != nil || !settings.CertificateRemindersEnabled || settings.Enabled || settings.Revision != 3 {
		t.Fatal("omitted field disabled certificate-only reminders")
	}
	if r := call("PUT", root+"/alerting", legacyBody); r.Code != 409 {
		t.Fatal("stale certificate write accepted")
	}
	point := time.Now().UTC().Add(time.Second)
	if err := db.SaveReport(ctx, node.ID, protocol.Report{CapturedAt: point}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveAgentMetadata(ctx, node.ID, protocol.Hello{Capabilities: []string{"http_probe.v1"}}); err != nil {
		t.Fatal(err)
	}
	task, err := db.CreateHTTPTask(ctx, service.ID, node.ID, point, point)
	if err != nil {
		t.Fatal(err)
	}
	certificate := httpcheck.Certificate{SHA256: strings.Repeat("ab", 32), NotBefore: point.Add(-time.Hour), NotAfter: point.Add(6 * 24 * time.Hour), Verified: true}
	if err := db.SaveHTTPResult(ctx, node.ID, httpcheck.Result{TaskID: task.ID, ServiceID: service.ID, Revision: task.Revision, ScheduledAt: point, CompletedAt: point.Add(time.Millisecond), Outcome: "success", StatusCode: 200, Certificates: []httpcheck.Certificate{certificate}}, point.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.PrepareCertificateReminders(ctx, store.CertificateAlertTarget{ServiceID: service.ID, ServiceRevision: service.Revision, AlertRevision: settings.Revision}, map[string]bool{node.ID: true}, point.Add(2*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	rules, err = db.ListAlertRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var warning *store.Incident
	for _, rule := range rules {
		if rule.Kind == "service_certificate" && rule.Enabled {
			warning, err = db.ObserveAlert(ctx, rule, node, store.AlertObservation{At: point.Add(3 * time.Millisecond), Known: true, Active: true, Message: "safe certificate reminder"})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if warning == nil {
		t.Fatal("certificate event missing")
	}
	for _, path := range []string{root + "/certificate-reminders?limit=1", root + "/incidents", "/api/v1/admin/incidents/" + warning.ID} {
		r := call("GET", path, "")
		if r.Code != 200 || !strings.Contains(r.Header().Get("Cache-Control"), "no-store") {
			t.Fatal("certificate private read rejected")
		}
		for _, private := range []string{"private-marker", "private-config-marker", "rule_snapshot", "config_encrypted", "idempotency_key", "payload_json"} {
			if strings.Contains(r.Body.String(), private) {
				t.Fatal("certificate history leaked private execution data")
			}
		}
	}
	r = call("GET", root+"/certificate-reminders?limit=1", "")
	var firstPage struct {
		Reminders []store.CertificateReminder `json:"reminders"`
		Next      int64                       `json:"next_before"`
	}
	if json.Unmarshal(r.Body.Bytes(), &firstPage) != nil || len(firstPage.Reminders) != 1 || firstPage.Next == 0 || firstPage.Reminders[0].DaysBefore != 7 || firstPage.Reminders[0].DeliveryStatus != "pending" {
		t.Fatal("certificate ledger contract changed")
	}
	r = call("GET", fmt.Sprintf("%s/certificate-reminders?limit=1&before=%d", root, firstPage.Next), "")
	var secondPage struct {
		Reminders []store.CertificateReminder `json:"reminders"`
	}
	if json.Unmarshal(r.Body.Bytes(), &secondPage) != nil || len(secondPage.Reminders) != 1 || secondPage.Reminders[0].DaysBefore != 14 || !secondPage.Reminders[0].Skipped {
		t.Fatal("certificate cursor repeated or lost skipped level")
	}
	if r := call("GET", "/api/v1/admin/incidents/"+warning.ID, ""); !strings.Contains(r.Body.String(), `"severity":"warning"`) {
		t.Fatal("certificate marked as technical fault")
	}
	if r := call("GET", "/api/v1/admin/alert-rules", ""); r.Code != 200 || r.Body.String() != `{"rules":[]}` {
		t.Fatal("certificate owned rule exposed in host editor")
	}
	if r := call("GET", "/api/v1/admin/service-monitors/missing/certificate-reminders", ""); r.Code != 404 {
		t.Fatal("missing service reminder endpoint manufactured history")
	}
}
