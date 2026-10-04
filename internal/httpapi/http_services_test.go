package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
}
