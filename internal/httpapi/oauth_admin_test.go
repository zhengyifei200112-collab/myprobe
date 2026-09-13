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

func TestGitHubOAuthSettingsAreRedactedAndStateIsProtected(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	authService := auth.New(database, time.Hour)
	_, _ = authService.Bootstrap(ctx, "admin", "correct horse battery staple")
	cfg := config.Config{EncryptionKey: strings.Repeat("e", 32), SessionTTL: time.Hour}
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
	payload := `{"enabled":true,"client_id":"client-id","client_secret":"top-secret-oauth-value","callback_url":"http://localhost/api/v1/auth/github/callback","username_allowlist":["AllowedUser"]}`
	updated := authenticatedRequest(t, server.Handler(), cookie, loginBody.CSRFToken, http.MethodPatch, "/api/v1/admin/auth-settings", payload)
	if updated.Code != http.StatusOK || strings.Contains(updated.Body.String(), "top-secret-oauth-value") || strings.Contains(updated.Body.String(), "client_secret_encrypted") {
		t.Fatalf("response=%d %s", updated.Code, updated.Body.String())
	}
	stored, _ := database.GitHubOAuthSettings(ctx)
	ciphertext := stored.ClientSecretEncrypted
	if ciphertext == "" || strings.Contains(ciphertext, "top-secret-oauth-value") {
		t.Fatal("secret was not encrypted")
	}
	preserve := `{"enabled":true,"client_id":"client-id","client_secret":"","callback_url":"http://localhost/api/v1/auth/github/callback","username_allowlist":["alloweduser"]}`
	response := authenticatedRequest(t, server.Handler(), cookie, loginBody.CSRFToken, http.MethodPatch, "/api/v1/admin/auth-settings", preserve)
	if response.Code != http.StatusOK {
		t.Fatalf("preserve=%d %s", response.Code, response.Body.String())
	}
	stored, _ = database.GitHubOAuthSettings(ctx)
	if stored.ClientSecretEncrypted != ciphertext {
		t.Fatal("empty secret update replaced stored secret")
	}
	status := httptest.NewRecorder()
	server.Handler().ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/v1/auth/github/status", nil))
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"enabled":true`) {
		t.Fatalf("status=%d %s", status.Code, status.Body.String())
	}
	start := httptest.NewRecorder()
	server.Handler().ServeHTTP(start, httptest.NewRequest(http.MethodGet, "/api/v1/auth/github/start", nil))
	if start.Code != http.StatusFound || !strings.HasPrefix(start.Header().Get("Location"), "https://github.com/login/oauth/authorize?") {
		t.Fatalf("start=%d %s", start.Code, start.Header().Get("Location"))
	}
	cookies := start.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode || cookies[0].Value == "" {
		t.Fatalf("state cookie=%#v", cookies)
	}
	callback := httptest.NewRecorder()
	bad := httptest.NewRequest(http.MethodGet, "/api/v1/auth/github/callback?state=wrong&code=bad", nil)
	bad.AddCookie(cookies[0])
	server.Handler().ServeHTTP(callback, bad)
	if callback.Code != http.StatusFound || callback.Header().Get("Location") != "/admin?oauth_error=failed" {
		t.Fatalf("callback=%d %s", callback.Code, callback.Header().Get("Location"))
	}
}
