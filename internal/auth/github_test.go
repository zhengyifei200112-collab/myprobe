package auth

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

func TestGitHubOAuthAllowlistStateAndSecretProtection(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	password := New(database, time.Hour)
	if _, err := password.Bootstrap(ctx, "admin", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			json.NewEncoder(w).Encode(map[string]string{"access_token": "temporary-access-token"})
		case "/user":
			if r.Header.Get("Authorization") != "Bearer temporary-access-token" {
				t.Error("missing bearer token")
			}
			json.NewEncoder(w).Encode(map[string]string{"login": "AllowedUser"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()
	service, err := NewGitHubService(database, strings.Repeat("k", 32), time.Hour, provider.Client())
	if err != nil {
		t.Fatal(err)
	}
	service.authorizeURL = provider.URL + "/authorize"
	service.tokenURL = provider.URL + "/token"
	service.userURL = provider.URL + "/user"
	settings, err := service.UpdateSettings(ctx, true, "client-id", "super-secret-value", "http://127.0.0.1/api/v1/auth/github/callback", []string{"AllowedUser", "alloweduser"})
	if err != nil {
		t.Fatal(err)
	}
	if settings.ClientSecretEncrypted != "" || !settings.ClientSecretSet || len(settings.UsernameAllowlist) != 1 {
		t.Fatalf("settings leaked or invalid: %#v", settings)
	}
	stored, _ := database.GitHubOAuthSettings(ctx)
	if stored.ClientSecretEncrypted == "" || strings.Contains(stored.ClientSecretEncrypted, "super-secret-value") {
		t.Fatal("OAuth secret was not encrypted")
	}
	now := time.Now().UTC().Truncate(time.Second)
	target, state, err := service.Begin(ctx, now)
	if err != nil || !strings.Contains(target, "client_id=client-id") || strings.Contains(target, "super-secret-value") {
		t.Fatalf("target=%q state=%q err=%v", target, state, err)
	}
	session, token, login, err := service.Complete(ctx, state, state, "code", now)
	if err != nil || session.UserID == "" || token == "" || login != "AllowedUser" {
		t.Fatalf("session=%#v login=%q err=%v", session, login, err)
	}
	if _, _, _, err := service.Complete(ctx, state, state, "code", now); err == nil {
		t.Fatal("OAuth state was replayed")
	}
}

func TestGitHubOAuthRejectsUnlistedUserAndUnsafeCallback(t *testing.T) {
	ctx := context.Background()
	database, _ := store.Open(ctx, ":memory:")
	defer database.Close()
	_, _ = New(database, time.Hour).Bootstrap(ctx, "admin", "correct horse battery staple")
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			json.NewEncoder(w).Encode(map[string]string{"access_token": "token"})
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"login": "intruder"})
	}))
	defer provider.Close()
	service, _ := NewGitHubService(database, strings.Repeat("k", 32), time.Hour, provider.Client())
	service.authorizeURL = provider.URL
	service.tokenURL = provider.URL + "/token"
	service.userURL = provider.URL + "/user"
	if _, err := service.UpdateSettings(ctx, true, "id", "secret", "http://metadata.google.internal/callback", []string{"admin"}); err == nil {
		t.Fatal("unsafe callback accepted")
	}
	_, _ = service.UpdateSettings(ctx, true, "id", "secret", "http://localhost/callback", []string{"admin"})
	now := time.Now().UTC()
	_, state, _ := service.Begin(ctx, now)
	if _, _, _, err := service.Complete(ctx, state, state, "code", now); err != ErrOAuthDenied {
		t.Fatalf("error=%v", err)
	}
}
