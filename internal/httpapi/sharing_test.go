package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/auth"
	"github.com/zhengyifei200112-collab/myprobe/internal/config"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestPasswordProtectedChartShareEnforcesNodeScope(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	selected, _, _ := database.CreateNode(ctx, store.CreateNodeParams{Name: "selected"})
	other, _, _ := database.CreateNode(ctx, store.CreateNodeParams{Name: "other"})
	report := protocol.Report{CapturedAt: time.Now().UTC(), CPU: protocol.CPUMetric{UsagePercent: 20}, Memory: protocol.MemoryMetric{TotalBytes: 100, UsedBytes: 20, UsagePercent: 20}}
	_ = database.SaveReport(ctx, selected.ID, report)
	_ = database.SaveReport(ctx, other.ID, report)
	authService := auth.New(database, time.Hour)
	_, _ = authService.Bootstrap(ctx, "admin", "correct horse battery staple")
	hub := agentgateway.NewHub()
	server := New(config.Config{}, database, authService, agentgateway.New(database, hub), hub)

	adminCookie, csrf := loginAdminForShare(t, server.Handler())
	create := authenticatedRequest(t, server.Handler(), adminCookie, csrf, http.MethodPost, "/api/v1/admin/chart-shares", `{"name":"customer","password":"share-password","node_ids":["`+selected.ID+`"]}`)
	if create.Code != http.StatusCreated || strings.Contains(create.Body.String(), "password_hash") {
		t.Fatalf("create = %d %s", create.Code, create.Body.String())
	}
	var created struct {
		Share store.ChartShare `json:"share"`
		Path  string           `json:"path"`
	}
	_ = json.Unmarshal(create.Body.Bytes(), &created)
	if created.Path != "/share/"+created.Share.ID {
		t.Fatalf("created = %#v", created)
	}

	meta := httptest.NewRecorder()
	server.Handler().ServeHTTP(meta, httptest.NewRequest(http.MethodGet, "/api/v1/share/"+created.Share.ID+"/meta", nil))
	if meta.Code != http.StatusOK || !strings.Contains(meta.Body.String(), `"authenticated":false`) {
		t.Fatalf("meta = %d %s", meta.Code, meta.Body.String())
	}
	unauthorized := httptest.NewRecorder()
	server.Handler().ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/v1/share/"+created.Share.ID+"/nodes", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized = %d", unauthorized.Code)
	}

	login := httptest.NewRequest(http.MethodPost, "/api/v1/share/"+created.Share.ID+"/login", bytes.NewBufferString(`{"password":"share-password"}`))
	login.RemoteAddr = "192.0.2.10:1234"
	loginResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(loginResponse, login)
	if loginResponse.Code != http.StatusOK || len(loginResponse.Result().Cookies()) != 1 {
		t.Fatalf("login = %d %s", loginResponse.Code, loginResponse.Body.String())
	}
	shareCookie := loginResponse.Result().Cookies()[0]

	nodesRequest := httptest.NewRequest(http.MethodGet, "/api/v1/share/"+created.Share.ID+"/nodes", nil)
	nodesRequest.AddCookie(shareCookie)
	nodesResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(nodesResponse, nodesRequest)
	if nodesResponse.Code != http.StatusOK || !strings.Contains(nodesResponse.Body.String(), selected.Name) || strings.Contains(nodesResponse.Body.String(), other.Name) {
		t.Fatalf("nodes = %d %s", nodesResponse.Code, nodesResponse.Body.String())
	}
	allowedHistory := shareRequest(t, server.Handler(), shareCookie, http.MethodGet, "/api/v1/share/"+created.Share.ID+"/nodes/"+selected.ID+"/history?range=1h")
	if allowedHistory.Code != http.StatusOK {
		t.Fatalf("allowed history = %d %s", allowedHistory.Code, allowedHistory.Body.String())
	}
	deniedHistory := shareRequest(t, server.Handler(), shareCookie, http.MethodGet, "/api/v1/share/"+created.Share.ID+"/nodes/"+other.ID+"/history?range=1h")
	if deniedHistory.Code != http.StatusNotFound {
		t.Fatalf("denied history = %d", deniedHistory.Code)
	}

	disable := authenticatedRequest(t, server.Handler(), adminCookie, csrf, http.MethodPatch, "/api/v1/admin/chart-shares/"+created.Share.ID, `{"name":"customer","node_ids":["`+selected.ID+`"],"enabled":false}`)
	if disable.Code != http.StatusOK {
		t.Fatalf("disable = %d %s", disable.Code, disable.Body.String())
	}
	disabledNodes := shareRequest(t, server.Handler(), shareCookie, http.MethodGet, "/api/v1/share/"+created.Share.ID+"/nodes")
	if disabledNodes.Code != http.StatusUnauthorized {
		t.Fatalf("disabled nodes = %d", disabledNodes.Code)
	}
}

func loginAdminForShare(t *testing.T, handler http.Handler) (*http.Cookie, string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"username":"admin","password":"correct horse battery staple"}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var body struct {
		CSRFToken string `json:"csrf_token"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &body)
	return response.Result().Cookies()[0], body.CSRFToken
}

func shareRequest(t *testing.T, handler http.Handler, cookie *http.Cookie, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
