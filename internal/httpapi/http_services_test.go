package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestHTTPServiceAdminConfiguration(t *testing.T) {
	handler, db, _ := securityTestServer(t)
	defer db.Close()
	node, _, err := db.CreateNode(context.Background(), store.CreateNodeParams{Name: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/admin/service-monitors"
	unauth := httptest.NewRecorder()
	handler.ServeHTTP(unauth, httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}")))
	if unauth.Code != 401 || !strings.Contains(unauth.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("unauth: %d %v", unauth.Code, unauth.Header())
	}
	login, credentials := loginSecurityRequest(t, handler, "admin", "correct-password", "", "")
	if login.Code != 200 {
		t.Fatal(login.Code)
	}
	cookie := login.Result().Cookies()[0]
	body := fmt.Sprintf(`{"name":"fixture","enabled":true,"interval_seconds":30,"node_ids":[%q],"spec":{"url":"http://example.com/private-marker","method":"GET","status_codes":[200],"timeout_ms":5000,"max_redirects":0,"max_body_bytes":1024}}`, node.ID)
	csrf := authenticatedRequest(t, handler, cookie, "", http.MethodPost, path, body)
	if csrf.Code != 403 {
		t.Fatalf("csrf: %d", csrf.Code)
	}
	created := authenticatedRequest(t, handler, cookie, credentials.CSRFToken, http.MethodPost, path, body)
	if created.Code != 201 {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	var response struct {
		Service   store.HTTPService `json:"service"`
		Execution bool              `json:"execution_enabled"`
	}
	if err = json.Unmarshal(created.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Execution || response.Service.Revision != 1 {
		t.Fatalf("response: %+v", response)
	}
	resource := path + "/" + response.Service.ID
	at := time.Now().UTC()
	if _, err = db.CreateHTTPTask(context.Background(), response.Service.ID, node.ID, at, at); err != nil {
		t.Fatal(err)
	}
	observed := authenticatedRequest(t, handler, cookie, credentials.CSRFToken, http.MethodGet, resource+"/results?limit=1", "")
	if observed.Code != 200 || strings.Contains(observed.Body.String(), "private-marker") || !strings.Contains(observed.Body.String(), `"result":null`) || !strings.Contains(observed.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("results: %d %s", observed.Code, observed.Body.String())
	}
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, httptest.NewRequest(http.MethodGet, resource+"/results", nil))
	if denied.Code != 401 {
		t.Fatalf("results auth: %d", denied.Code)
	}
	if r := authenticatedRequest(t, handler, cookie, credentials.CSRFToken, http.MethodGet, resource+"/results?limit=101", ""); r.Code != 400 {
		t.Fatal(r.Code)
	}
	read := authenticatedRequest(t, handler, cookie, credentials.CSRFToken, http.MethodGet, resource, "")
	if read.Code != 200 || !strings.Contains(read.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("read: %d", read.Code)
	}
	update := strings.Replace(body, `"name":"fixture"`, `"revision":1,"name":"updated"`, 1)
	for _, want := range []int{200, 409} {
		r := authenticatedRequest(t, handler, cookie, credentials.CSRFToken, http.MethodPut, resource, update)
		if r.Code != want {
			t.Fatalf("update: %d %s", r.Code, r.Body.String())
		}
	}
	for _, invalid := range []string{body + "{}", strings.Replace(body, `"enabled":true,`, "", 1), strings.Replace(body, `"enabled":true`, `"enabled":true,"unexpected":1`, 1), strings.Replace(body, node.ID, "missing-node", 1)} {
		r := authenticatedRequest(t, handler, cookie, credentials.CSRFToken, http.MethodPost, path, invalid)
		if r.Code != 400 || strings.Contains(r.Body.String(), "private-marker") {
			t.Fatalf("invalid: %d %s", r.Code, r.Body.String())
		}
	}
	audit := authenticatedRequest(t, handler, cookie, credentials.CSRFToken, http.MethodGet, "/api/v1/admin/audit?limit=20", "")
	if audit.Code != 200 || !strings.Contains(audit.Body.String(), "http_service") || strings.Contains(audit.Body.String(), "private-marker") {
		t.Fatalf("audit: %d %s", audit.Code, audit.Body.String())
	}
	second := authenticatedRequest(t, handler, cookie, credentials.CSRFToken, http.MethodPost, path, body)
	if second.Code != 201 {
		t.Fatal(second.Code)
	}
	seen := map[string]bool{}
	cursor := ""
	for page := 0; page < 2; page++ {
		r := authenticatedRequest(t, handler, cookie, credentials.CSRFToken, http.MethodGet, path+"?limit=1&after="+cursor, "")
		var list struct {
			Services []store.HTTPServiceSummary `json:"services"`
			Next     string                     `json:"next_cursor"`
		}
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &list) != nil || len(list.Services) != 1 || strings.Contains(r.Body.String(), "private-marker") {
			t.Fatalf("list: %d %s", r.Code, r.Body.String())
		}
		if seen[list.Services[0].ID] {
			t.Fatal("duplicate page entry")
		}
		seen[list.Services[0].ID] = true
		cursor = list.Next
		if (page == 0) != (cursor != "") {
			t.Fatal("incorrect next cursor")
		}
	}
	for _, query := range []string{"?limit=0", "?limit=101", "?limit=no"} {
		if r := authenticatedRequest(t, handler, cookie, credentials.CSRFToken, http.MethodGet, path+query, ""); r.Code != 400 {
			t.Fatal(r.Code)
		}
	}
	if r := authenticatedRequest(t, handler, cookie, "", http.MethodDelete, resource+"?revision=2", ""); r.Code != 403 {
		t.Fatalf("delete csrf: %d", r.Code)
	}
	for _, tc := range []struct {
		query string
		want  int
	}{{"", 400}, {"?revision=1", 409}, {"?revision=2", 204}, {"?revision=2", 409}} {
		r := authenticatedRequest(t, handler, cookie, credentials.CSRFToken, http.MethodDelete, resource+tc.query, "")
		if r.Code != tc.want {
			t.Fatalf("delete: %d %s", r.Code, r.Body.String())
		}
	}
	if r := authenticatedRequest(t, handler, cookie, credentials.CSRFToken, http.MethodGet, resource, ""); r.Code != 404 {
		t.Fatalf("deleted read: %d", r.Code)
	}
}
