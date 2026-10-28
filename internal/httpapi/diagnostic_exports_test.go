package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/alerts"
	"github.com/zhengyifei200112-collab/myprobe/internal/auth"
	"github.com/zhengyifei200112-collab/myprobe/internal/config"
	"github.com/zhengyifei200112-collab/myprobe/internal/diagnostics"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestDiagnosticExportPreviewPrivacyFrozenDownloadAndSessionBinding(t *testing.T) {
	ctx := context.Background()
	base := networkQualityAPIFixtureDir(t)
	db, err := store.Open(ctx, filepath.Join(base, "private-database.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	authService := auth.New(db, time.Hour)
	if _, err = authService.Bootstrap(ctx, "admin", "synthetic-diagnostic-password"); err != nil {
		t.Fatal(err)
	}
	session, token, err := authService.Login(ctx, "admin", "synthetic-diagnostic-password")
	if err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: sessionCookie, Value: token}
	node, agentToken, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "synthetic-private-node"})
	if err != nil {
		t.Fatal(err)
	}
	secret := strings.Repeat("synthetic-key-", 4)
	hub := agentgateway.NewHub()
	server := New(config.Config{EncryptionKey: secret, BackupPassphraseFile: filepath.Join(base, "private-secret-file"), NotificationOrigin: "https://example.invalid/private", TrustedProxies: []string{"203.0.113.9"}}, db, authService, agentgateway.New(db, hub), hub,
		WithRuntimeIdentity("v1.2.3-203.0.113.9-private-build", time.Now()), WithAlertHealth(func() alerts.EngineDiagnostics {
			return alerts.EngineDiagnostics{Status: secret, Reason: secret, DeliveryConfiguration: secret, Evaluation: diagnostics.JobSnapshot{State: secret, LastErrorCode: secret}}
		}), WithSchedulerHealth(func() any { return map[string]any{"private_token": secret} }))
	path := "/api/v1/admin/system/diagnostic-exports"
	anonymous := httptest.NewRecorder()
	server.Handler().ServeHTTP(anonymous, httptest.NewRequest(http.MethodPost, path+"/preview", strings.NewReader(`{}`)))
	if anonymous.Code != 401 || !strings.Contains(anonymous.Header().Get("Cache-Control"), "no-store") {
		t.Fatal("anonymous preview/cache policy failed")
	}
	forbidden := authenticatedRequest(t, server.Handler(), cookie, "", http.MethodPost, path+"/preview", `{}`)
	if forbidden.Code != 403 || !strings.Contains(forbidden.Header().Get("Cache-Control"), "no-store") {
		t.Fatal("CSRF preview/cache policy failed")
	}
	rawDump := authenticatedRequest(t, server.Handler(), cookie, session.CSRFToken, http.MethodPost, path+"/preview", `{"include_database":true}`)
	if rawDump.Code != 400 {
		t.Fatal("unapproved content request accepted")
	}
	preview := authenticatedRequest(t, server.Handler(), cookie, session.CSRFToken, http.MethodPost, path+"/preview", `{}`)
	if preview.Code != 200 {
		t.Fatalf("preview: %d %s", preview.Code, preview.Body.String())
	}
	var result struct {
		Metadata diagnostics.ExportMetadata `json:"preview"`
		Bundle   diagnosticBundle           `json:"bundle"`
	}
	if err = json.Unmarshal(preview.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	assertDiagnosticClosedFields(t, preview.Body.Bytes())
	if result.Bundle.Runtime.Version.Base != "1.2.3" || result.Bundle.Database.DatabaseFileStatus != "ok" || result.Bundle.Configuration.Counts.Nodes != 1 || result.Bundle.Workers["notification"].Status != "unavailable" {
		t.Fatalf("projection: %+v", result.Bundle)
	}
	for _, private := range []string{secret, token, agentToken, session.ID, session.CSRFToken, node.ID, "synthetic-private-node", "203.0.113.9", base, "example.invalid"} {
		if strings.Contains(preview.Body.String(), private) {
			t.Fatalf("preview leaked private data %s", private)
		}
	}
	// A later source mutation must not change the previously reviewed file.
	if _, _, err = db.CreateNode(ctx, store.CreateNodeParams{Name: "after preview"}); err != nil {
		t.Fatal(err)
	}
	request, _ := json.Marshal(map[string]string{"preview_id": result.Metadata.ID})
	another, anotherToken, err := authService.Login(ctx, "admin", "synthetic-diagnostic-password")
	if err != nil {
		t.Fatal(err)
	}
	crossSession := authenticatedRequest(t, server.Handler(), &http.Cookie{Name: sessionCookie, Value: anotherToken}, another.CSRFToken, http.MethodPost, path+"/download", string(request))
	if crossSession.Code != 404 {
		t.Fatal("another login session could download the preview")
	}
	download := authenticatedRequest(t, server.Handler(), cookie, session.CSRFToken, http.MethodPost, path+"/download", string(request))
	if download.Code != 200 {
		t.Fatalf("download: %d %s", download.Code, download.Body.String())
	}
	hash := sha256.Sum256(download.Body.Bytes())
	if result.Metadata.SHA256 != hex.EncodeToString(hash[:]) || download.Header().Get("X-Content-SHA256") != result.Metadata.SHA256 || download.Body.Len() != result.Metadata.Bytes {
		t.Fatal("download changed reviewed bytes/hash")
	}
	var frozen diagnosticBundle
	if err = json.Unmarshal(download.Body.Bytes(), &frozen); err != nil || frozen.Configuration.Counts.Nodes != 1 {
		t.Fatal("download captured later state")
	}
	entries, err := db.ListAudit(ctx, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if entry.ObjectType == "diagnostic_bundle" {
			found = true
			if entry.ObjectID != result.Metadata.ID || !strings.Contains(string(entry.Details), "response_prepared") {
				t.Fatal("audit did not record the prepared export")
			}
		}
	}
	if !found {
		t.Fatal("download has no audit")
	}
}

// A new field in a source DTO must not silently broaden the v1 export contract.
// Additions require explicit privacy/format review instead of dumping the source.
func assertDiagnosticClosedFields(t *testing.T, raw []byte) {
	t.Helper()
	var envelope map[string]any
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	object := func(value any) map[string]any {
		result, ok := value.(map[string]any)
		if !ok {
			t.Fatal("expected diagnostic object")
		}
		return result
	}
	check := func(value any, keys string) {
		allow := map[string]bool{}
		for _, key := range strings.Fields(keys) {
			allow[key] = true
		}
		for key := range object(value) {
			if !allow[key] {
				t.Fatalf("unreviewed diagnostic field: %s", key)
			}
		}
	}
	bundle := object(envelope["bundle"])
	check(bundle, "format generated_at observation_scope included excluded runtime configuration_structure configuration_counts database jobs workers notification_queue transport")
	check(envelope["preview"], "preview_id file_name bytes sha256 expires_at")
	check(bundle["runtime"], "version go_version os architecture uptime_seconds")
	for _, key := range []string{"version", "go_version"} {
		check(object(bundle["runtime"])[key], "status base prerelease build_metadata")
	}
	check(bundle["configuration_structure"], "http_execution_enabled https_only_cookie trusted_proxy_entries application_key_configured notification_origin_configured independent_backup_secret_configured raw_retention_seconds minute_retention_seconds five_minute_retention_seconds retention_interval_seconds")
	config := object(bundle["configuration_counts"])
	check(config, "status agent_version_scope observed_at counts agent_versions agent_version_unrecognized agent_no_hello agent_version_overflow")
	if counts, ok := config["counts"]; ok {
		check(counts, "nodes users latency_targets target_groups http_services notification_channels alert_rules alert_policies")
	}
	if versions, ok := config["agent_versions"].([]any); ok {
		for _, version := range versions {
			check(version, "version count")
			check(object(version)["version"], "status base prerelease build_metadata")
		}
	}
	check(bundle["database"], "status schema_version journal_mode allocated_bytes reusable_bytes database_bytes wal_bytes database_file_status wal_file_status")
	check(bundle["jobs"], "host_retention http_retention manual_backup_generation notification_evaluation latency_scheduler http_scheduler")
	for _, job := range object(bundle["jobs"]) {
		check(job, "state active completed failed cancelled error_class last_completed_at last_success_at duration_seconds")
	}
	check(bundle["workers"], "retention notification latency_scheduler http_scheduler scheduled_backup")
	for _, worker := range object(bundle["workers"]) {
		check(worker, "status error_class operation")
	}
	queue := object(bundle["notification_queue"])
	check(queue, "status counting_contract incident aggregate system")
	for _, key := range []string{"incident", "aggregate", "system"} {
		if counts, ok := queue[key]; ok {
			check(counts, "pending due_by_time future inflight expired_leases")
		}
	}
	check(bundle["transport"], "status agent_connections v1_connections v2_connections unknown_connections pending_results expired_results http_status http_pending http_expired browser_subscriptions")
}

func TestDiagnosticExportAuditFailureDoesNotReleaseFile(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{store: db}
	session := store.Session{ID: "synthetic-session", UserID: "synthetic-user"}
	metadata, err := server.diagnosticExports.Prepare(session.ID, []byte(`{"safe_counter":0}`), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	raw, _ := json.Marshal(map[string]string{"preview_id": metadata.ID})
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Set("session", session)
	c.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(raw))
	server.downloadDiagnosticExport(c)
	if response.Code != 503 || strings.Contains(response.Body.String(), "safe_counter") || response.Header().Get("Content-Disposition") != "" {
		t.Fatal("audit failure released the diagnostic file")
	}
}
