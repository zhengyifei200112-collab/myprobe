package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/auth"
	"github.com/zhengyifei200112-collab/myprobe/internal/config"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestAdminCanConfigureLatencyAssignment(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	node, _, err := database.CreateNode(ctx, store.CreateNodeParams{Name: "node"})
	if err != nil {
		t.Fatal(err)
	}
	authService := auth.New(database, time.Hour)
	if _, err := authService.Bootstrap(ctx, "admin", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	hub := agentgateway.NewHub()
	server := New(config.Config{}, database, authService, agentgateway.New(database, hub), hub)

	login := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"username":"admin","password":"correct horse battery staple"}`))
	login.Header.Set("Content-Type", "application/json")
	loginResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(loginResponse, login)
	if loginResponse.Code != http.StatusOK {
		t.Fatalf("login status = %d", loginResponse.Code)
	}
	var loginBody struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(loginResponse.Body.Bytes(), &loginBody); err != nil {
		t.Fatal(err)
	}
	cookies := loginResponse.Result().Cookies()
	if len(cookies) == 0 || loginBody.CSRFToken == "" {
		t.Fatal("login did not return session credentials")
	}

	targetResponse := authenticatedRequest(t, server.Handler(), cookies[0], loginBody.CSRFToken, http.MethodPost, "/api/v1/admin/targets", `{"name":"HTTPS","kind":"tcping","host":"example.com","port":443,"interval_seconds":30,"timeout_ms":1000}`)
	if targetResponse.Code != http.StatusCreated {
		t.Fatalf("target status = %d: %s", targetResponse.Code, targetResponse.Body.String())
	}
	var targetBody struct {
		Target store.Target `json:"target"`
	}
	_ = json.Unmarshal(targetResponse.Body.Bytes(), &targetBody)
	groupResponse := authenticatedRequest(t, server.Handler(), cookies[0], loginBody.CSRFToken, http.MethodPost, "/api/v1/admin/target-groups", `{"name":"TCP","kind":"tcping"}`)
	if groupResponse.Code != http.StatusCreated {
		t.Fatalf("group status = %d: %s", groupResponse.Code, groupResponse.Body.String())
	}
	var groupBody struct {
		Group store.TargetGroup `json:"group"`
	}
	_ = json.Unmarshal(groupResponse.Body.Bytes(), &groupBody)
	attach := authenticatedRequest(t, server.Handler(), cookies[0], loginBody.CSRFToken, http.MethodPut, "/api/v1/admin/target-groups/"+groupBody.Group.ID+"/targets/"+targetBody.Target.ID, "")
	if attach.Code != http.StatusNoContent {
		t.Fatalf("attach status = %d: %s", attach.Code, attach.Body.String())
	}
	assign := authenticatedRequest(t, server.Handler(), cookies[0], loginBody.CSRFToken, http.MethodPut, "/api/v1/admin/nodes/"+node.ID+"/target-groups/"+groupBody.Group.ID, "")
	if assign.Code != http.StatusNoContent {
		t.Fatalf("assign status = %d: %s", assign.Code, assign.Body.String())
	}
	assignments, err := database.ListTargetAssignments(ctx)
	if err != nil || len(assignments) != 1 {
		t.Fatalf("assignments = %#v, error = %v", assignments, err)
	}
}

func authenticatedRequest(t *testing.T, handler http.Handler, cookie *http.Cookie, csrf, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	request.AddCookie(cookie)
	request.Header.Set("X-CSRF-Token", csrf)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
