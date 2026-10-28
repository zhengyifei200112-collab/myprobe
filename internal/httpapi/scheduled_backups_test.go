package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/alerts"
	"github.com/zhengyifei200112-collab/myprobe/internal/auth"
	"github.com/zhengyifei200112-collab/myprobe/internal/backup"
	"github.com/zhengyifei200112-collab/myprobe/internal/config"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestScheduledBackupAdminContractAndDetachedExecution(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	base := networkQualityAPIFixtureDir(t)
	db, err := store.Open(ctx, filepath.Join(base, "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	authService := auth.New(db, time.Hour)
	if _, err = authService.Bootstrap(ctx, "admin", "synthetic-backup-admin-password"); err != nil {
		t.Fatal(err)
	}
	service := alerts.New(db, strings.Repeat("synthetic-key-", 4), nil, nil)
	channel, err := service.CreateChannel(ctx, "synthetic destination", "webhook", alerts.ChannelConfig{URL: "https://example.invalid/backup"})
	if err != nil {
		t.Fatal(err)
	}
	passFile := filepath.Join(base, "separate-secret-file")
	if err = os.WriteFile(passFile, []byte("synthetic-backup-passphrase"), 0o600); err != nil {
		t.Fatal(err)
	}
	worker, err := backup.NewScheduler(ctx, db, passFile, service.VerifyRecoveryChannels, service.ValidateBackupFailureChannels)
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	hub := agentgateway.NewHub()
	server := NewWithAlertService(config.Config{}, db, authService, agentgateway.New(db, hub), hub, service, WithBackupScheduler(worker))
	path := "/api/v1/admin/maintenance/backup-schedule"
	anonymous := httptest.NewRecorder()
	server.Handler().ServeHTTP(anonymous, httptest.NewRequest(http.MethodGet, path, nil))
	if anonymous.Code != 401 || !strings.Contains(anonymous.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("anonymous contract: %d %s", anonymous.Code, anonymous.Header())
	}
	login := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"username":"admin","password":"synthetic-backup-admin-password"}`))
	request.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(login, request)
	if login.Code != 200 {
		t.Fatal("synthetic login failed")
	}
	var session struct {
		CSRF string `json:"csrf_token"`
	}
	if err = json.Unmarshal(login.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	cookie := login.Result().Cookies()[0]
	forbidden := authenticatedRequest(t, server.Handler(), cookie, "", http.MethodPost, path+"/run", "")
	if forbidden.Code != 403 || !strings.Contains(forbidden.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("CSRF contract: %d %s", forbidden.Code, forbidden.Header())
	}
	cfg := store.BackupSchedule{Enabled: true, Directory: filepath.Join(base, "archives"), DailyTime: "03:00", Timezone: "UTC", Keep: 2, FailureChannelIDs: []string{channel.ID}}
	raw, _ := json.Marshal(cfg)
	unknown := strings.TrimSuffix(string(raw), "}") + `,"passphrase":"must never persist"}`
	rejected := authenticatedRequest(t, server.Handler(), cookie, session.CSRF, http.MethodPut, path, unknown)
	if rejected.Code != 400 {
		t.Fatal("secret field accepted")
	}
	saved := authenticatedRequest(t, server.Handler(), cookie, session.CSRF, http.MethodPut, path, string(raw))
	if saved.Code != 200 {
		t.Fatalf("save: %d %s", saved.Code, saved.Body.String())
	}
	accepted := authenticatedRequest(t, server.Handler(), cookie, session.CSRF, http.MethodPost, path+"/run", "")
	if accepted.Code != 202 {
		t.Fatalf("accept: %d %s", accepted.Code, accepted.Body.String())
	}
	var completed store.BackupRun
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		runs, e := db.BackupRuns(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if len(runs) > 0 && runs[0].Status == "completed" {
			completed = runs[0]
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if completed.ID == "" {
		t.Fatal("accepted background execution never completed")
	}
	verify := authenticatedRequest(t, server.Handler(), cookie, session.CSRF, http.MethodPost, path+"/runs/"+completed.ID+"/verify", "")
	if verify.Code != 202 {
		t.Fatalf("recovery accept: %d %s", verify.Code, verify.Body.String())
	}
	verified := false
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		raw, e := db.BackupRunArtifact(ctx, completed.ID)
		if e != nil {
			t.Fatal(e)
		}
		if strings.Contains(string(raw), `"recovery_verified":true`) {
			verified = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !verified {
		t.Fatal("background recovery did not persist its evidence")
	}
	status := authenticatedRequest(t, server.Handler(), cookie, session.CSRF, http.MethodGet, path, "")
	if status.Code != 200 || !strings.Contains(status.Header().Get("Cache-Control"), "no-store") {
		t.Fatal("private status contract failed")
	}
	for _, secret := range []string{passFile, "synthetic-backup-passphrase", "synthetic-key-", "lease_token", "config_encrypted"} {
		if strings.Contains(status.Body.String(), secret) {
			t.Fatalf("status leaked %s", secret)
		}
	}
}
