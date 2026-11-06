package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/auth"
	backuparchive "github.com/zhengyifei200112-collab/myprobe/internal/backup"
	"github.com/zhengyifei200112-collab/myprobe/internal/config"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestMaintenanceRestoreRejectsUnsupportedMigrationHistoryBeforeStaging(t *testing.T) {
	ctx := context.Background()
	root := networkQualityAPIFixtureDir(t)
	source, err := store.Open(ctx, filepath.Join(root, "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(root, "unsupported.db")
	if err = source.ConsistentBackup(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	if err = source.Close(); err != nil {
		t.Fatal(err)
	}
	inspection, err := sql.Open("sqlite", snapshot)
	if err != nil {
		t.Fatal(err)
	}
	const privateVersion = "999_untrusted_private_ledger.sql"
	if _, err = inspection.ExecContext(ctx, `INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)`, privateVersion, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if err = inspection.Close(); err != nil {
		t.Fatal(err)
	}
	plain, err := os.ReadFile(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var encrypted bytes.Buffer
	const passphrase = "synthetic restore guard passphrase"
	if err = backuparchive.Encrypt(&encrypted, bytes.NewReader(plain), passphrase); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "destination.db")
	db, err := store.Open(ctx, destination)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, _, err = db.CreateNode(ctx, store.CreateNodeParams{ID: "destination-marker", Name: "Destination marker"}); err != nil {
		t.Fatal(err)
	}
	a := auth.New(db, time.Hour)
	if _, err = a.Bootstrap(ctx, "admin", "synthetic-restore-password"); err != nil {
		t.Fatal(err)
	}
	hub := agentgateway.NewHub()
	api := New(config.Config{}, db, a, agentgateway.New(db, hub), hub)
	login := httptest.NewRecorder()
	loginRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"username":"admin","password":"synthetic-restore-password"}`))
	loginRequest.Header.Set("Content-Type", "application/json")
	api.Handler().ServeHTTP(login, loginRequest)
	if login.Code != http.StatusOK {
		t.Fatal("fixture login failed", login.Code)
	}
	var credentials struct {
		CSRF string `json:"csrf_token"`
	}
	if err = json.Unmarshal(login.Body.Bytes(), &credentials); err != nil {
		t.Fatal(err)
	}
	cookie := login.Result().Cookies()[0]
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err = form.WriteField("passphrase", passphrase); err != nil {
		t.Fatal(err)
	}
	part, err := form.CreateFormFile("file", "unsupported.mpb")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(encrypted.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err = form.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/maintenance/restore", &body)
	req.AddCookie(cookie)
	req.Header.Set("X-CSRF-Token", credentials.CSRF)
	req.Header.Set("Content-Type", form.FormDataContentType())
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, req)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), store.ErrIncompatibleDatabaseSchema.Error()) || strings.Contains(response.Body.String(), privateVersion) {
		t.Fatal("unsupported archive was accepted or its migration data was echoed", response.Code)
	}
	if _, err = os.Stat(destination + ".restore"); !os.IsNotExist(err) {
		t.Fatal("rejected HTTP restore staged a replacement")
	}
	nodes, err := db.ListPublicNodes(ctx, time.Now().UTC())
	if err != nil || len(nodes) != 1 || nodes[0].Node.ID != "destination-marker" {
		t.Fatal("destination changed after rejected upload", err)
	}
	stillLoggedIn := authenticatedRequest(t, api.Handler(), cookie, credentials.CSRF, http.MethodGet, "/api/v1/auth/me", "")
	if stillLoggedIn.Code != http.StatusOK {
		t.Fatal("destination session changed after rejected upload", stillLoggedIn.Code)
	}
}
