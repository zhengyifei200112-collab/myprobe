package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/auth"
	"github.com/zhengyifei200112-collab/myprobe/internal/config"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestMaintenanceConfigAndEncryptedBackupEndpoints(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "myprobe.db")
	database, err := store.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, _, err := database.CreateNode(ctx, store.CreateNodeParams{ID: "maintenance-node", Name: "Maintenance"}); err != nil {
		t.Fatal(err)
	}
	authService := auth.New(database, time.Hour)
	if _, err := authService.Bootstrap(ctx, "admin", "maintenance-password"); err != nil {
		t.Fatal(err)
	}
	hub := agentgateway.NewHub()
	server := New(config.Config{}, database, authService, agentgateway.New(database, hub), hub)

	loginResponse := httptest.NewRecorder()
	loginRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"username":"admin","password":"maintenance-password"}`))
	loginRequest.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(loginResponse, loginRequest)
	if loginResponse.Code != http.StatusOK {
		t.Fatalf("login = %d %s", loginResponse.Code, loginResponse.Body.String())
	}
	var loginBody struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(loginResponse.Body.Bytes(), &loginBody); err != nil {
		t.Fatal(err)
	}
	cookie := loginResponse.Result().Cookies()[0]

	configResponse := authenticatedRequest(t, server.Handler(), cookie, loginBody.CSRFToken, http.MethodGet, "/api/v1/admin/maintenance/config", "")
	if configResponse.Code != http.StatusOK {
		t.Fatalf("config export = %d %s", configResponse.Code, configResponse.Body.String())
	}
	if configResponse.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("cache control = %q", configResponse.Header().Get("Cache-Control"))
	}
	var snapshot store.ConfigSnapshot
	if err := json.Unmarshal(configResponse.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Version != store.ConfigSnapshotVersion || len(snapshot.Nodes) != 1 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	previewPayload := append([]byte(`{"dry_run":true,"config":`), configResponse.Body.Bytes()...)
	previewPayload = append(previewPayload, '}')
	preview := authenticatedRequest(t, server.Handler(), cookie, loginBody.CSRFToken, http.MethodPost, "/api/v1/admin/maintenance/config/import", string(previewPayload))
	if preview.Code != http.StatusOK {
		t.Fatalf("config preview = %d %s", preview.Code, preview.Body.String())
	}

	backupResponse := authenticatedRequest(t, server.Handler(), cookie, loginBody.CSRFToken, http.MethodPost, "/api/v1/admin/maintenance/backup", `{"passphrase":"correct horse battery staple"}`)
	if backupResponse.Code != http.StatusOK {
		t.Fatalf("backup export = %d %s", backupResponse.Code, backupResponse.Body.String())
	}
	if bytes.HasPrefix(backupResponse.Body.Bytes(), []byte("SQLite format 3")) {
		t.Fatal("backup endpoint returned plaintext SQLite")
	}
	if backupResponse.Header().Get("Content-Disposition") == "" {
		t.Fatal("backup download filename is missing")
	}

	var multipartBody bytes.Buffer
	writer := multipart.NewWriter(&multipartBody)
	if err := writer.WriteField("passphrase", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("file", "untrusted-name.mpb")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(backupResponse.Body.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	restoreRequest := httptest.NewRequest(http.MethodPost, "/api/v1/admin/maintenance/restore", &multipartBody)
	restoreRequest.AddCookie(cookie)
	restoreRequest.Header.Set("X-CSRF-Token", loginBody.CSRFToken)
	restoreRequest.Header.Set("Content-Type", writer.FormDataContentType())
	restoreResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(restoreResponse, restoreRequest)
	if restoreResponse.Code != http.StatusAccepted {
		t.Fatalf("restore = %d %s", restoreResponse.Code, restoreResponse.Body.String())
	}
	if _, err := os.Stat(databasePath + ".restore"); err != nil {
		t.Fatalf("pending restore: %v", err)
	}
}
