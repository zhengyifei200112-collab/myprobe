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

func TestMaintenanceMemberPreviewAPI(t *testing.T) {
	handler, db, _ := securityTestServer(t)
	defer db.Close()
	const path = "/api/v1/admin/maintenance-windows/preview-members"
	const payload = `{"selection":{"kind":"tags","tags":["prod"],"tag_mode":"all"}}`
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, path, strings.NewReader(payload)))
	if unauthorized.Code != http.StatusUnauthorized || !strings.Contains(unauthorized.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("unauthorized: %d %v", unauthorized.Code, unauthorized.Header())
	}
	login, credentials := loginSecurityRequest(t, handler, "admin", "correct-password", "", "")
	if login.Code != http.StatusOK {
		t.Fatal(login.Code)
	}
	cookie := login.Result().Cookies()[0]
	if response := authenticatedRequest(t, handler, cookie, "", http.MethodPost, path, payload); response.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF: %d %s", response.Code, response.Body.String())
	}
	ctx := context.Background()
	node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "Preview fixture", Tags: []string{"prod"}})
	if err != nil {
		t.Fatal(err)
	}
	response := authenticatedRequest(t, handler, cookie, credentials.CSRFToken, http.MethodPost, path, payload)
	var body struct {
		Selection store.MaintenanceSelection `json:"selection"`
		Members   []store.MaintenanceMember  `json:"members"`
		Count     int                        `json:"member_count"`
	}
	if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("preview: %d %s", response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Count != 1 || len(body.Members) != 1 || body.Members[0].ID != node.ID || body.Members[0].Name != node.Name || body.Selection.Kind != "tags" {
		t.Fatalf("preview body: %+v", body)
	}
	for _, invalid := range []string{
		`{}`, `null`, payload + `{}`, `{"selection":{"kind":"tags","tags":["prod"],"tag_mode":"all","extra":true}}`,
		`{"selection":{"kind":"nodes","node_ids":["missing"]}}`,
		`{"selection":{"kind":"tags","tags":["absent"],"tag_mode":"any"}}`,
		`{"extra":true,"selection":{"kind":"nodes"}}`,
		`{"selection":{"kind":"nodes","node_ids":["` + strings.Repeat("x", 64<<10) + `"]}}`,
	} {
		response := authenticatedRequest(t, handler, cookie, credentials.CSRFToken, http.MethodPost, path, invalid)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid request: %d %s", response.Code, response.Body.String())
		}
	}
	windows, _, err := db.ListMaintenanceWindows(ctx, "", 10)
	if err != nil || len(windows) != 0 {
		t.Fatalf("preview wrote windows: %v %v", windows, err)
	}
}

func TestMaintenanceListAPI(t *testing.T) {
	handler, db, _ := securityTestServer(t)
	defer db.Close()
	const path = "/api/v1/admin/maintenance-windows"
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, path, nil))
	if unauthorized.Code != 401 || !strings.Contains(unauthorized.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("auth: %d %v", unauthorized.Code, unauthorized.Header())
	}
	login, credentials := loginSecurityRequest(t, handler, "admin", "correct-password", "", "")
	if login.Code != 200 {
		t.Fatal(login.Code)
	}
	cookie := login.Result().Cookies()[0]
	read := func(query string) *httptest.ResponseRecorder {
		return authenticatedRequest(t, handler, cookie, credentials.CSRFToken, http.MethodGet, path+query, "")
	}
	empty := read("")
	if empty.Code != 200 || !strings.Contains(empty.Body.String(), `"windows":[]`) {
		t.Fatalf("empty: %d %s", empty.Code, empty.Body.String())
	}
	ctx := context.Background()
	node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "maintenance fixture"})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Millisecond)
	expected := map[string]bool{}
	for range 2 {
		w, err := db.CreateMaintenanceWindow(ctx, store.MaintenanceWindow{Reason: "planned work", Timezone: "UTC", CreatedBy: "fixture", NodeIDs: []string{node.ID}, StartsAt: at, EndsAt: at.Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		expected[w.ID] = true
	}
	var page struct {
		Windows []store.MaintenanceWindow `json:"windows"`
		Next    string                    `json:"next_cursor"`
	}
	first := read("?limit=1")
	if first.Code != 200 || !strings.Contains(first.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("list: %d", first.Code)
	}
	if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil || len(page.Windows) != 1 || page.Next == "" {
		t.Fatalf("first page: %+v %v", page, err)
	}
	firstID := page.Windows[0].ID
	if !expected[firstID] {
		t.Fatalf("unexpected first window: %s", firstID)
	}
	second := read("?limit=1&after=" + page.Next)
	if second.Code != http.StatusOK {
		t.Fatalf("second status: %d %s", second.Code, second.Body.String())
	}
	if err := json.Unmarshal(second.Body.Bytes(), &page); err != nil || len(page.Windows) != 1 || page.Next != "" || page.Windows[0].ID == firstID || !expected[page.Windows[0].ID] || len(page.Windows[0].NodeIDs) != 1 || page.Windows[0].NodeIDs[0] != node.ID {
		t.Fatalf("second page: %+v %v", page, err)
	}
	for _, query := range []string{"?limit=", "?limit=1&limit=2", "?limit=0", "?limit=101", "?limit=x", "?after=" + strings.Repeat("x", 129)} {
		if response := read(query); response.Code != 400 {
			t.Fatalf("invalid query %s: %d", query, response.Code)
		}
	}
}
