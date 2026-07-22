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
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestAdminNotificationAndAlertAPIsDoNotLeakCredentials(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	node, _, _ := database.CreateNode(ctx, store.CreateNodeParams{Name: "edge"})
	authService := auth.New(database, time.Hour)
	_, _ = authService.Bootstrap(ctx, "admin", "correct horse battery staple")
	var deliveries int
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		deliveries++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()
	cfg := config.Config{EncryptionKey: strings.Repeat("e", 32)}
	hub := agentgateway.NewHub()
	server := New(cfg, database, authService, agentgateway.New(database, hub), hub)

	loginRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"username":"admin","password":"correct horse battery staple"}`))
	loginResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(loginResponse, loginRequest)
	var loginBody struct {
		CSRFToken string `json:"csrf_token"`
	}
	_ = json.Unmarshal(loginResponse.Body.Bytes(), &loginBody)
	cookie := loginResponse.Result().Cookies()[0]

	channelPayload := `{"name":"ops","kind":"webhook","config":{"url":"` + receiver.URL + `"}}`
	channelResponse := authenticatedRequest(t, server.Handler(), cookie, loginBody.CSRFToken, http.MethodPost, "/api/v1/admin/notification-channels", channelPayload)
	if channelResponse.Code != http.StatusCreated || strings.Contains(channelResponse.Body.String(), receiver.URL) {
		t.Fatalf("channel response = %d %s", channelResponse.Code, channelResponse.Body.String())
	}
	var channelBody struct {
		Channel store.NotificationChannel `json:"channel"`
	}
	_ = json.Unmarshal(channelResponse.Body.Bytes(), &channelBody)

	listResponse := authenticatedRequest(t, server.Handler(), cookie, loginBody.CSRFToken, http.MethodGet, "/api/v1/admin/notification-channels", "")
	if listResponse.Code != http.StatusOK || strings.Contains(listResponse.Body.String(), receiver.URL) || strings.Contains(listResponse.Body.String(), "config_encrypted") {
		t.Fatalf("list response = %d %s", listResponse.Code, listResponse.Body.String())
	}
	testResponse := authenticatedRequest(t, server.Handler(), cookie, loginBody.CSRFToken, http.MethodPost, "/api/v1/admin/notification-channels/"+channelBody.Channel.ID+"/test", "")
	if testResponse.Code != http.StatusNoContent || deliveries != 1 {
		t.Fatalf("test response = %d, deliveries = %d", testResponse.Code, deliveries)
	}

	rulePayload := `{"node_id":"` + node.ID + `","channel_id":"` + channelBody.Channel.ID + `","kind":"offline","config":{"offline_seconds":60},"cooldown_seconds":300}`
	ruleResponse := authenticatedRequest(t, server.Handler(), cookie, loginBody.CSRFToken, http.MethodPost, "/api/v1/admin/alert-rules", rulePayload)
	if ruleResponse.Code != http.StatusCreated {
		t.Fatalf("rule response = %d %s", ruleResponse.Code, ruleResponse.Body.String())
	}
	eventsResponse := authenticatedRequest(t, server.Handler(), cookie, loginBody.CSRFToken, http.MethodGet, "/api/v1/admin/alert-events", "")
	if eventsResponse.Code != http.StatusOK || eventsResponse.Body.String() != "{\"events\":[]}" {
		t.Fatalf("events response = %d %s", eventsResponse.Code, eventsResponse.Body.String())
	}
}

func TestNotificationChannelRequiresEncryptionKey(t *testing.T) {
	ctx := context.Background()
	database, _ := store.Open(ctx, ":memory:")
	defer database.Close()
	authService := auth.New(database, time.Hour)
	_, _ = authService.Bootstrap(ctx, "admin", "correct horse battery staple")
	hub := agentgateway.NewHub()
	server := New(config.Config{}, database, authService, agentgateway.New(database, hub), hub)
	login := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"username":"admin","password":"correct horse battery staple"}`))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, login)
	var body struct {
		CSRFToken string `json:"csrf_token"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &body)
	created := authenticatedRequest(t, server.Handler(), response.Result().Cookies()[0], body.CSRFToken, http.MethodPost, "/api/v1/admin/notification-channels", `{"name":"ops","kind":"webhook","config":{"url":"https://example.com"}}`)
	if created.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", created.Code, created.Body.String())
	}
}
