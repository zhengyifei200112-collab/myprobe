package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIncidentSilenceAPI(t *testing.T) {
	handler, db, _ := securityTestServer(t)
	defer db.Close()
	for _, pair := range [][2]string{{"GET", "/api/v1/admin/incidents/missing/silences"}, {"POST", "/api/v1/admin/incidents/missing/silences"}, {"POST", "/api/v1/admin/incidents/missing/silences/missing/cancel"}, {"GET", "/api/v1/admin/notification-deliveries/missing/silences"}} {
		r := httptest.NewRecorder()
		handler.ServeHTTP(r, httptest.NewRequest(pair[0], pair[1], strings.NewReader(`{}`)))
		if r.Code != 401 || !strings.Contains(r.Header().Get("Cache-Control"), "no-store") {
			t.Fatalf("auth: %d", r.Code)
		}
	}
	login, credentials := loginSecurityRequest(t, handler, "admin", "correct-password", "", "")
	if login.Code != 200 {
		t.Fatal(login.Code)
	}
	cookie := login.Result().Cookies()[0]
	call := func(method, path, body string) *httptest.ResponseRecorder {
		return authenticatedRequest(t, handler, cookie, credentials.CSRFToken, method, path, body)
	}
	ctx := context.Background()
	node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "silence fixture"})
	if err != nil {
		t.Fatal(err)
	}
	channel, err := db.CreateNotificationChannel(ctx, "fixture", "webhook", "unused")
	if err != nil {
		t.Fatal(err)
	}
	rule, err := db.CreateAlertRule(ctx, node.ID, channel.ID, "cpu", json.RawMessage(`{"threshold_percent":90}`), 900)
	if err != nil {
		t.Fatal(err)
	}
	i, err := db.ObserveAlert(ctx, rule, node, store.AlertObservation{At: time.Now().UTC().Add(-time.Second), Known: true, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/admin/incidents/" + i.ID + "/silences"
	body := `{"request_id":"api-silence","duration_seconds":60,"reason":"private-api-silence","acknowledge":true}`
	if r := authenticatedRequest(t, handler, cookie, "", http.MethodPost, path, body); r.Code != 403 || !strings.Contains(r.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("csrf: %d", r.Code)
	}
	for _, invalid := range []string{`null`, `{}`, body + `{}`, strings.Replace(body, `"request_id":`, `"created_by":"forged","request_id":`, 1), strings.Replace(body, `"request_id":`, `"starts_at":"2020-01-01T00:00:00Z","request_id":`, 1), strings.Replace(body, `60`, `59`, 1), strings.Replace(body, `60`, `604801`, 1), strings.Replace(body, `private-api-silence`, strings.Repeat("界", 334), 1), strings.Replace(body, `private-api-silence`, strings.Repeat("x", 16<<10), 1), strings.Replace(body, `private-api-silence`, string([]byte{0xff}), 1), strings.Replace(body, `private-api-silence`, `bad\u0000value`, 1)} {
		if r := call("POST", path, invalid); r.Code != 400 {
			t.Fatalf("invalid: %d %s", r.Code, r.Body.String())
		}
	}
	r := call("POST", path, body)
	var first store.SilenceMutationResult
	if err := json.Unmarshal(r.Body.Bytes(), &first); err != nil || r.Code != 201 || !first.AcknowledgementCreated || first.Replayed {
		t.Fatalf("create: %d %s %v", r.Code, r.Body.String(), err)
	}
	if r := call("POST", path, body); r.Code != 200 || !strings.Contains(r.Body.String(), `"replayed":true`) {
		t.Fatal(r.Body.String())
	}
	if r := call("POST", path, strings.Replace(body, "private-api-silence", "changed", 1)); r.Code != 409 || !strings.Contains(r.Body.String(), "silence_request_conflict") {
		t.Fatal(r.Body.String())
	}
	if r := call("POST", path, strings.Replace(body, "api-silence", "other-id", 1)); r.Code != 409 || !strings.Contains(r.Body.String(), "silence_active") {
		t.Fatal(r.Body.String())
	}
	if r := call("GET", path, ""); r.Code != 200 || !strings.Contains(r.Body.String(), `"creator_name":"admin"`) || !strings.Contains(r.Body.String(), "private-api-silence") || strings.Contains(r.Body.String(), "request_digest") || strings.Contains(r.Body.String(), "request_id") {
		t.Fatal(r.Body.String())
	}
	for _, query := range []string{"?limit=0", "?limit=101", "?limit=1&limit=2", "?after=bad%20key", "?after=x&after=y"} {
		if r := call("GET", path+query, ""); r.Code != 400 {
			t.Fatal(r.Body.String())
		}
	}
	jobs, err := db.ListIncidentDeliveries(ctx, i.ID, 0, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatal(err)
	}
	if r := call("GET", "/api/v1/admin/notification-deliveries/"+jobs[0].ID+"/silences", ""); r.Code != 200 || !strings.Contains(r.Body.String(), "recorded_at") {
		t.Fatal(r.Body.String())
	}
	cancelPath := path + "/" + first.SilenceID + "/cancel"
	cancelBody := `{"request_id":"api-cancel","revision":1}`
	if r := call("POST", cancelPath, `{"request_id":"bad-revision","revision":2}`); r.Code != 409 {
		t.Fatal(r.Body.String())
	}
	if r := call("POST", cancelPath, cancelBody); r.Code != 200 || strings.Contains(r.Body.String(), `"replayed":true`) {
		t.Fatal(r.Body.String())
	}
	if _, err := db.ObserveAlert(ctx, rule, node, store.AlertObservation{At: time.Now().UTC().Add(time.Second), Known: true, Active: false}); err != nil {
		t.Fatal(err)
	}
	if r := call("POST", cancelPath, cancelBody); r.Code != 200 || !strings.Contains(r.Body.String(), `"replayed":true`) {
		t.Fatal(r.Body.String())
	}
	if r := call("POST", path, body); r.Code != 200 || !strings.Contains(r.Body.String(), `"replayed":true`) {
		t.Fatal(r.Body.String())
	}
	if r := call("POST", path, strings.Replace(body, "api-silence", "resolved-id", 1)); r.Code != 409 || !strings.Contains(r.Body.String(), "incident_not_firing") {
		t.Fatal(r.Body.String())
	}
	if r := call("GET", path, ""); r.Code != 200 || !strings.Contains(r.Body.String(), `"incident_state":"resolved"`) {
		t.Fatal(r.Body.String())
	}
	if r := call("GET", "/api/v1/admin/incidents/missing/silences", ""); r.Code != 404 {
		t.Fatal(r.Body.String())
	}
	public := httptest.NewRecorder()
	handler.ServeHTTP(public, httptest.NewRequest("GET", "/api/v1/public/nodes", nil))
	for _, secret := range []string{"private-api-silence", "silences", "actor_name", "creator_name", first.SilenceID} {
		if strings.Contains(public.Body.String(), secret) {
			t.Fatalf("public leak: %s", secret)
		}
	}
	// Authentication remains usable while an isolated table failure reaches the API.
	faultDB, err := sql.Open("sqlite", filepath.ToSlash(db.DatabasePath()))
	if err != nil {
		t.Fatal(err)
	}
	defer faultDB.Close()
	if _, err := faultDB.ExecContext(ctx, `ALTER TABLE incident_silences RENAME TO unavailable_silence_fixture`); err != nil {
		t.Fatal(err)
	}
	if r := call("GET", path, ""); r.Code != 500 || !strings.Contains(r.Body.String(), "silence_unavailable") || strings.Contains(r.Body.String(), "unavailable_silence_fixture") || !strings.Contains(r.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("generic storage error: %d %s", r.Code, r.Body.String())
	}
}
