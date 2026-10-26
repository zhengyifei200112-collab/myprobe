package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/auth"
	"github.com/zhengyifei200112-collab/myprobe/internal/config"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestNodeOrderAPIContract(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, name := range []string{"a", "b"} {
		if _, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	nodes, err := db.ListNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	service := auth.New(db, time.Hour)
	if _, err := service.Bootstrap(ctx, "admin", "local-test-password"); err != nil {
		t.Fatal(err)
	}
	hub := agentgateway.NewHub()
	handler := New(config.Config{}, db, service, agentgateway.New(db, hub), hub).Handler()
	login := httptest.NewRecorder()
	handler.ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"local-test-password"}`)))
	var session struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil || session.CSRF == "" {
		t.Fatal("login failed")
	}
	cookie := login.Result().Cookies()[0]
	endpoint := "/api/v1/admin/nodes/reorder"
	request := store.NodeOrderRequest{Expected: []store.NodeOrderEntry{{ID: nodes[0].ID, SortOrder: nodes[0].SortOrder}, {ID: nodes[1].ID, SortOrder: nodes[1].SortOrder}}, NodeIDs: []string{nodes[1].ID, nodes[0].ID}}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	unauth := httptest.NewRecorder()
	handler.ServeHTTP(unauth, httptest.NewRequest(http.MethodPost, endpoint, strings.NewReader(string(raw))))
	if unauth.Code != 401 {
		t.Fatal(unauth.Code)
	}
	if res := authenticatedRequest(t, handler, cookie, "", http.MethodPost, endpoint, string(raw)); res.Code != 403 {
		t.Fatal(res.Code)
	}
	for _, invalid := range []string{string(raw) + ` {}`, `{"expected":[],"node_ids":[]}`, `{"expected":[{"id":"a","sort_order":0,"token":"forbidden"}],"node_ids":["a"]}`, `{"expected":[],"node_ids":[],"unknown":1}`, strings.Repeat(" ", 256<<10) + string(raw)} {
		if res := authenticatedRequest(t, handler, cookie, session.CSRF, http.MethodPost, endpoint, invalid); res.Code != 400 {
			t.Fatalf("invalid status=%d", res.Code)
		}
	}
	for _, changed := range []bool{true, false} {
		res := authenticatedRequest(t, handler, cookie, session.CSRF, http.MethodPost, endpoint, string(raw))
		if res.Code != 200 {
			t.Fatalf("status=%d %s", res.Code, res.Body.String())
		}
		var body struct {
			Changed bool `json:"changed"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil || body.Changed != changed {
			t.Fatalf("response=%s", res.Body.String())
		}
	}
	public := httptest.NewRecorder()
	handler.ServeHTTP(public, httptest.NewRequest(http.MethodGet, "/api/v1/public/nodes", nil))
	var result struct {
		Nodes []store.PublicNode `json:"nodes"`
	}
	if err := json.Unmarshal(public.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Nodes) != 2 || result.Nodes[0].Node.ID != nodes[1].ID {
		t.Fatal("public order not persisted")
	}
	for _, private := range []string{"expected", "agent_token", "token_hash"} {
		if strings.Contains(public.Body.String(), private) {
			t.Fatal("private field exposed")
		}
	}
	// Same old snapshot proposing a different order is not a replay.
	request.NodeIDs = []string{nodes[0].ID, nodes[1].ID}
	raw, _ = json.Marshal(request)
	if res := authenticatedRequest(t, handler, cookie, session.CSRF, http.MethodPost, endpoint, string(raw)); res.Code != 409 {
		t.Fatalf("stale status=%d", res.Code)
	}
}
