package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestMaintenanceCreationAndCancellationAPI(t *testing.T) {
	handler, db, _ := securityTestServer(t)
	defer db.Close()
	const path = "/api/v1/admin/maintenance-windows"
	for _, target := range []string{path, path + "/missing/cancel"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, target, strings.NewReader(`{}`)))
		if response.Code != 401 || !strings.Contains(response.Header().Get("Cache-Control"), "no-store") {
			t.Fatalf("auth %s: %d", target, response.Code)
		}
	}
	login, credentials := loginSecurityRequest(t, handler, "admin", "correct-password", "", "")
	if login.Code != 200 {
		t.Fatal(login.Code)
	}
	cookie := login.Result().Cookies()[0]
	post := func(target, body string) *httptest.ResponseRecorder {
		return authenticatedRequest(t, handler, cookie, credentials.CSRFToken, http.MethodPost, target, body)
	}
	for _, target := range []string{path, path + "/missing/cancel"} {
		if response := authenticatedRequest(t, handler, cookie, "", http.MethodPost, target, `{}`); response.Code != 403 {
			t.Fatalf("csrf: %d", response.Code)
		}
	}
	ctx := context.Background()
	node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "window fixture", Tags: []string{"prod"}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	p, err := db.PreviewMaintenanceWindow(ctx, store.MaintenanceDraft{Selection: store.MaintenanceSelection{Kind: "tags", Tags: []string{"prod"}, TagMode: "all"}, Reason: "work", Timezone: "UTC", StartsAt: now.Add(-time.Minute), EndsAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	request := struct {
		Draft  store.MaintenanceDraft `json:"draft"`
		Digest string                 `json:"preview_digest"`
		ID     string                 `json:"request_id"`
	}{p.Draft, p.Digest, "request-1"}
	encode := func() string {
		raw, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	payload := encode()
	for _, body := range []string{`null`, `{}`, payload + `{}`, strings.Replace(payload, `"draft":`, `"created_by":"forged","draft":`, 1), strings.Replace(payload, `"request-1"`, `""`, 1), strings.Replace(payload, "work", strings.Repeat("x", 64<<10), 1)} {
		if response := post(path, body); response.Code != 400 {
			t.Fatalf("invalid create: %d %s", response.Code, response.Body.String())
		}
	}
	request.Draft.Reason = "unconfirmed change"
	if response := post(path, encode()); response.Code != 409 || !strings.Contains(response.Body.String(), "maintenance_preview_changed") {
		t.Fatalf("confirmation: %d %s", response.Code, response.Body.String())
	}
	request.Draft = p.Draft
	created := post(path, payload)
	if created.Code != 201 || !strings.Contains(created.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	var result store.MaintenanceCreateResult
	if err := json.Unmarshal(created.Body.Bytes(), &result); err != nil || result.WindowID == "" || result.Replayed {
		t.Fatalf("create result: %+v %v", result, err)
	}
	windowID := result.WindowID
	retry := post(path, payload)
	if err := json.Unmarshal(retry.Body.Bytes(), &result); err != nil || retry.Code != 200 || !result.Replayed || result.WindowID != windowID {
		t.Fatalf("create replay: %s %v", retry.Body.String(), err)
	}
	request.Draft.Reason = "different content"
	if response := post(path, encode()); response.Code != 409 || !strings.Contains(response.Body.String(), "maintenance_request_conflict") {
		t.Fatalf("conflict: %d %s", response.Code, response.Body.String())
	}
	windows, _, err := db.ListMaintenanceWindows(ctx, "", 10)
	if err != nil || len(windows) != 1 || windows[0].Selection == nil || windows[0].Selection.Kind != "tags" || windows[0].CreatedBy == "" || windows[0].CreatedBy == "forged" {
		t.Fatalf("saved: %+v %v", windows, err)
	}
	cancelPath := path + "/" + windowID + "/cancel"
	for _, body := range []string{`null`, `{}`, `{"revision":0}`, `{"revision":1,"created_by":"forged"}`, `{"revision":1}{}`, `{"revision":` + strings.Repeat("1", 5<<10) + `}`} {
		if response := post(cancelPath, body); response.Code != 400 {
			t.Fatalf("invalid cancel: %d %s", response.Code, response.Body.String())
		}
	}
	if response := post(cancelPath, `{"revision":2}`); response.Code != 409 {
		t.Fatalf("cancel revision: %d", response.Code)
	}
	canceled := post(cancelPath, `{"revision":1}`)
	var action store.MaintenanceCancelResult
	if err := json.Unmarshal(canceled.Body.Bytes(), &action); err != nil || canceled.Code != 200 || action.Replayed || action.Revision != 2 || action.CanceledAt.IsZero() {
		t.Fatalf("cancel: %s %v", canceled.Body.String(), err)
	}
	firstTime := action.CanceledAt
	repeated := post(cancelPath, `{"revision":1}`)
	if err := json.Unmarshal(repeated.Body.Bytes(), &action); err != nil || repeated.Code != 200 || !action.Replayed || !action.CanceledAt.Equal(firstTime) {
		t.Fatalf("cancel replay: %s %v", repeated.Body.String(), err)
	}
	if response := post(path+"/missing/cancel", `{"revision":1}`); response.Code != 404 {
		t.Fatalf("missing cancel: %d", response.Code)
	}
	if err := db.DeleteNode(ctx, node.ID); err != nil {
		t.Fatal(err)
	}
	if response := post(path, payload); response.Code != 200 || !strings.Contains(response.Body.String(), `"replayed":true`) {
		t.Fatalf("retry after deleting node: %d %s", response.Code, response.Body.String())
	}
	windows, _, err = db.ListMaintenanceWindows(ctx, "", 10)
	if err != nil || len(windows) != 1 || windows[0].CanceledBy != windows[0].CreatedBy || windows[0].CanceledAt == nil || len(windows[0].NodeIDs) != 1 || windows[0].NodeIDs[0] != node.ID {
		t.Fatalf("retained history: %+v %v", windows, err)
	}
}
