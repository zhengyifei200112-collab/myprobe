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
	"github.com/zhengyifei200112-collab/myprobe/internal/diagnostics"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestSystemHealthAuthorizationAndEvidence(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "private-health-database.db")
	db, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	authService := auth.New(db, time.Hour)
	if _, err := authService.Bootstrap(ctx, "admin", "health-test-password"); err != nil {
		t.Fatal(err)
	}
	_, token, err := authService.Login(ctx, "admin", "health-test-password")
	if err != nil {
		t.Fatal(err)
	}
	hub := agentgateway.NewHub()
	_, unsubscribe := hub.Subscribe()
	defer unsubscribe()
	server := New(config.Config{}, db, authService, agentgateway.New(db, hub), hub)
	request := func(cookie *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/system/health", nil)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, req)
		if response.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatal("diagnostics can be cached")
		}
		return response
	}
	for _, cookie := range []*http.Cookie{nil, {Name: sessionCookie, Value: "invalid"}, {Name: "myprobe_share", Value: token}} {
		response := request(cookie)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("unauthorized status = %d", response.Code)
		}
		if strings.Contains(response.Body.String(), "schema_version") {
			t.Fatal("unauthorized diagnostics leaked")
		}
	}
	cookie := &http.Cookie{Name: sessionCookie, Value: token}
	response := request(cookie)
	if response.Code != http.StatusOK {
		t.Fatalf("health = %d %s", response.Code, response.Body.String())
	}
	var body struct {
		Database  store.DatabaseDiagnostics `json:"database"`
		Retention struct {
			Scope string                  `json:"observation_scope"`
			Job   diagnostics.JobSnapshot `json:"job"`
		} `json:"retention"`
		BrowserSubscriptions int `json:"browser_subscriptions"`
		NotificationQueue    struct {
			Status string `json:"status"`
		} `json:"notification_queue"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Database.Status != "ok" || body.Database.SchemaVersion == "" || body.Retention.Scope != "process" || body.Retention.Job.State != "never_run" || body.BrowserSubscriptions != 1 || body.NotificationQueue.Status != "unavailable" {
		t.Fatalf("incorrect evidence: %+v", body)
	}
	for _, secret := range []string{dbPath, "private-health-database", token, "health-test-password"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatal("private value exposed")
		}
	}
	if err := authService.Logout(ctx, token); err != nil {
		t.Fatal(err)
	}
	if request(cookie).Code != http.StatusUnauthorized {
		t.Fatal("revoked session accepted")
	}
}
