package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestIncidentAcknowledgementAPI(t *testing.T) {
	handler, db, _ := securityTestServer(t)
	defer db.Close()
	for _, path := range []string{"/api/v1/admin/incidents", "/api/v1/admin/incidents/missing/acknowledgement"} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			if path == "/api/v1/admin/incidents" && method == http.MethodPost {
				continue
			}
			r := httptest.NewRecorder()
			handler.ServeHTTP(r, httptest.NewRequest(method, path, strings.NewReader(`{}`)))
			if r.Code != 401 || !strings.Contains(r.Header().Get("Cache-Control"), "no-store") {
				t.Fatalf("auth %s %s: %d", method, path, r.Code)
			}
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
	node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "ack fixture"})
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
	at := time.Now().UTC().Truncate(time.Second)
	i, err := db.ObserveAlert(ctx, rule, node, store.AlertObservation{At: at, Known: true, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/admin/incidents/" + i.ID + "/acknowledgement"
	if r := call(http.MethodGet, path, ""); r.Code != 200 || !strings.Contains(r.Body.String(), `"acknowledgement":null`) || !strings.Contains(r.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("empty view: %d %s", r.Code, r.Body.String())
	}
	body := `{"request_id":"api-ack-id","note":"private-api-note-marker"}`
	if r := authenticatedRequest(t, handler, cookie, "", http.MethodPost, path, body); r.Code != 403 || !strings.Contains(r.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("csrf: %d %s", r.Code, r.Body.String())
	}
	for _, invalid := range []string{
		`null`, `{}`, body + `{}`, strings.Replace(body, `"request_id":`, `"actor_id":"forged","request_id":`, 1),
		strings.Replace(body, `"request_id":`, `"acknowledged_at":"2020-01-01T00:00:00Z","request_id":`, 1),
		strings.Replace(body, `"api-ack-id"`, `"bad key"`, 1),
		strings.Replace(body, "private-api-note-marker", strings.Repeat("界", 334), 1),
		strings.Replace(body, "private-api-note-marker", strings.Repeat("x", 16<<10), 1),
		strings.Replace(body, "private-api-note-marker", `bad\u0000value`, 1),
		strings.Replace(body, "private-api-note-marker", string([]byte{0xff}), 1),
	} {
		if r := call(http.MethodPost, path, invalid); r.Code != 400 || !strings.Contains(r.Header().Get("Cache-Control"), "no-store") {
			t.Fatalf("invalid request: %d %s", r.Code, r.Body.String())
		}
	}
	created := call(http.MethodPost, path, body)
	var first store.AcknowledgeIncidentResult
	if err := json.Unmarshal(created.Body.Bytes(), &first); err != nil || created.Code != 201 || first.Replayed || first.Acknowledgement.ActorName != "admin" || first.Acknowledgement.Note != "private-api-note-marker" {
		t.Fatalf("create: %d %s %v", created.Code, created.Body.String(), err)
	}
	actor, err := db.UserByUsername(ctx, "admin")
	if err != nil || actor.ID != first.Acknowledgement.ActorID {
		t.Fatalf("forged actor: %+v %v", first, err)
	}
	if r := call(http.MethodPost, path, body); r.Code != 200 || !strings.Contains(r.Body.String(), `"replayed":true`) {
		t.Fatalf("replay: %d %s", r.Code, r.Body.String())
	}
	if r := call(http.MethodPost, path, strings.Replace(body, "private-api-note-marker", "changed", 1)); r.Code != 409 || !strings.Contains(r.Body.String(), "acknowledgement_request_conflict") {
		t.Fatalf("identity conflict: %d %s", r.Code, r.Body.String())
	}
	if r := call(http.MethodPost, path, strings.Replace(body, "api-ack-id", "new-key", 1)); r.Code != 409 || !strings.Contains(r.Body.String(), "incident_already_acknowledged") {
		t.Fatalf("overwrite: %d %s", r.Code, r.Body.String())
	}
	list := call(http.MethodGet, "/api/v1/admin/incidents?limit=10", "")
	if list.Code != 200 || !strings.Contains(list.Header().Get("Cache-Control"), "no-store") || !strings.Contains(list.Body.String(), `"actor_name":"admin"`) || strings.Contains(list.Body.String(), "private-api-note-marker") || strings.Contains(list.Body.String(), "request_id") || strings.Contains(list.Body.String(), "actor_id") {
		t.Fatalf("private list summary: %d %s", list.Code, list.Body.String())
	}
	public := httptest.NewRecorder()
	handler.ServeHTTP(public, httptest.NewRequest(http.MethodGet, "/api/v1/public/nodes", nil))
	if public.Code != 200 {
		t.Fatalf("public nodes: %d", public.Code)
	}
	for _, forbidden := range []string{"private-api-note-marker", "api-ack-id", "acknowledgement", "actor_name", first.Acknowledgement.ActorID} {
		if strings.Contains(public.Body.String(), forbidden) {
			t.Fatalf("public response leaked %q", forbidden)
		}
	}
	if _, err := db.ObserveAlert(ctx, rule, node, store.AlertObservation{At: at.Add(time.Second), Known: true, Active: false}); err != nil {
		t.Fatal(err)
	}
	if r := call(http.MethodPost, path, body); r.Code != 200 || !strings.Contains(r.Body.String(), `"replayed":true`) {
		t.Fatalf("replay after recovery: %d %s", r.Code, r.Body.String())
	}
	if r := call(http.MethodGet, path, ""); r.Code != 200 || !strings.Contains(r.Body.String(), `"incident_state":"resolved"`) || !strings.Contains(r.Body.String(), "private-api-note-marker") {
		t.Fatalf("resolved history: %d %s", r.Code, r.Body.String())
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		want := 404
		if method == http.MethodPost {
			want = 409 // This original key is already bound to another incident.
		}
		if r := call(method, "/api/v1/admin/incidents/missing/acknowledgement", body); r.Code != want {
			t.Fatalf("missing: %d %s", r.Code, r.Body.String())
		}
	}
	if r := call(http.MethodPost, "/api/v1/admin/incidents/missing/acknowledgement", strings.Replace(body, "api-ack-id", "missing-new-key", 1)); r.Code != 404 {
		t.Fatalf("new missing request: %d %s", r.Code, r.Body.String())
	}
	// Fault only the acknowledgement table in this isolated on-disk fixture,
	// leaving session authentication usable so the storage error reaches the API.
	faultDB, err := sql.Open("sqlite", filepath.ToSlash(db.DatabasePath()))
	if err != nil {
		t.Fatal(err)
	}
	defer faultDB.Close()
	if _, err := faultDB.ExecContext(ctx, `ALTER TABLE incident_acknowledgements RENAME TO unavailable_ack_fixture`); err != nil {
		t.Fatal(err)
	}
	if r := call(http.MethodGet, path, ""); r.Code != 500 || !strings.Contains(r.Header().Get("Cache-Control"), "no-store") || strings.Contains(r.Body.String(), "database") {
		t.Fatalf("storage failure: %d %s", r.Code, r.Body.String())
	}
}
