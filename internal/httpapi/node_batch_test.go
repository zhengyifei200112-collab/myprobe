package httpapi

import (
	"bytes"
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

func TestBatchAPIAuthenticationPreviewApplyAndPublicContract(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "batch fixture"})
	if err != nil {
		t.Fatal(err)
	}
	authService := auth.New(db, time.Hour)
	if _, err = authService.Bootstrap(ctx, "admin", "local-test-password"); err != nil {
		t.Fatal(err)
	}
	hub := agentgateway.NewHub()
	server := New(config.Config{}, db, authService, agentgateway.New(db, hub), hub)
	login := httptest.NewRecorder()
	server.Handler().ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"username":"admin","password":"local-test-password"}`)))
	var session struct {
		CSRF string `json:"csrf_token"`
	}
	if err = json.Unmarshal(login.Body.Bytes(), &session); err != nil || session.CSRF == "" {
		t.Fatalf("login %s", login.Body.String())
	}
	cookie := login.Result().Cookies()[0]
	previewPath := "/api/v1/admin/nodes/batch/preview"
	body := `{"node_ids":["` + node.ID + `"],"hidden":true}`
	unauth := httptest.NewRecorder()
	server.Handler().ServeHTTP(unauth, httptest.NewRequest(http.MethodPost, previewPath, strings.NewReader(body)))
	if unauth.Code != 401 {
		t.Fatal(unauth.Code)
	}
	if res := authenticatedRequest(t, server.Handler(), cookie, "", http.MethodPost, previewPath, body); res.Code != 403 {
		t.Fatal(res.Code)
	}
	for _, invalid := range []string{`{"node_ids":["` + node.ID + `"],"price_minor":100}`, body + ` {}`, `{"node_ids":[]}`, `{"node_ids":["` + node.ID + `"],"targets":{"mode":"add","ids":[],"unknown":1}}`} {
		if res := authenticatedRequest(t, server.Handler(), cookie, session.CSRF, http.MethodPost, previewPath, invalid); res.Code != 400 {
			t.Fatalf("invalid request %d %s", res.Code, res.Body.String())
		}
	}
	res := authenticatedRequest(t, server.Handler(), cookie, session.CSRF, http.MethodPost, previewPath, body)
	if res.Code != 200 {
		t.Fatalf("preview %d %s", res.Code, res.Body.String())
	}
	var preview store.NodeBatchPreview
	if err = json.Unmarshal(res.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	public := httptest.NewRecorder()
	server.Handler().ServeHTTP(public, httptest.NewRequest(http.MethodGet, "/api/v1/public/nodes", nil))
	if strings.Contains(public.Body.String(), "revision") || strings.Contains(public.Body.String(), "preview") {
		t.Fatal("batch metadata exposed publicly")
	}
	events, unsubscribe := hub.Subscribe()
	defer unsubscribe()
	applyBody := `{"preview_id":"` + preview.ID + `","idempotency_key":"api-idempotency-key"}`
	for range 2 {
		res = authenticatedRequest(t, server.Handler(), cookie, session.CSRF, http.MethodPost, "/api/v1/admin/nodes/batch/apply", applyBody)
		if res.Code != 200 {
			t.Fatalf("apply %d %s", res.Code, res.Body.String())
		}
	}
	select {
	case event := <-events:
		if event.Type != "refresh" {
			t.Fatal(event.Type)
		}
	default:
		t.Fatal("missing public refresh")
	}
	public = httptest.NewRecorder()
	server.Handler().ServeHTTP(public, httptest.NewRequest(http.MethodGet, "/api/v1/public/nodes", nil))
	if strings.Contains(public.Body.String(), node.ID) {
		t.Fatal("hidden node returned")
	}
	res = authenticatedRequest(t, server.Handler(), cookie, session.CSRF, http.MethodGet, "/api/v1/admin/audit", "")
	if strings.Count(res.Body.String(), `"action":"batch_update"`) != 1 {
		t.Fatal("replay duplicated audit")
	}
}
