package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func serviceTransferAPIFixture(t *testing.T, db *store.Store) (store.HTTPService, store.ServiceAlertBundle, store.ServiceAlertImportMapping) {
	t.Helper()
	ctx := context.Background()
	v, _, _ := diagnosticAPIFixture(t, db)
	channel, err := db.CreateNotificationChannel(ctx, "Transfer channel", "webhook", "private-channel-credential-marker")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateNotificationChannel(ctx, "Destination telegram", "telegram", "private-second-credential-marker"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveServiceAlertSettings(ctx, store.ServiceAlertSettings{ServiceID: v.ID, ServiceRevision: v.Revision, ChannelID: channel.ID, Enabled: false, CertificateRemindersEnabled: true, DurationSeconds: 30, RecoverySeconds: 60, RepeatSeconds: 600, CooldownSeconds: 300}); err != nil {
		t.Fatal(err)
	}
	b, err := db.ExportServiceAlerts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return v, b, store.ServiceAlertImportMapping{Services: map[string]string{v.ID: v.ID}, Channels: map[string]string{channel.ID: channel.ID}}
}

func TestServiceAlertTransferAPIAuthStrictnessPrivacyAndReplay(t *testing.T) {
	handler, db, _ := securityTestServer(t)
	defer db.Close()
	v, b, m := serviceTransferAPIFixture(t, db)
	login, auth := loginSecurityRequest(t, handler, "admin", "correct-password", "", "")
	if login.Code != 200 {
		t.Fatal("login")
	}
	cookie := login.Result().Cookies()[0]
	base := "/api/v1/admin/service-alert-transfers"
	for _, resource := range []string{"/export", "/destinations"} {
		denied := httptest.NewRecorder()
		handler.ServeHTTP(denied, httptest.NewRequest(http.MethodGet, base+resource, nil))
		if denied.Code != 401 || !strings.Contains(denied.Header().Get("Cache-Control"), "no-store") {
			t.Fatal("auth/no-store")
		}
		r := authenticatedRequest(t, handler, cookie, auth.CSRFToken, http.MethodGet, base+resource, "")
		if r.Code != 200 || strings.Contains(r.Body.String(), "private-marker") || strings.Contains(r.Body.String(), "private-channel-credential-marker") || strings.Contains(r.Body.String(), "config_encrypted") {
			t.Fatal("private transfer output")
		}
	}
	request := map[string]any{"bundle": b, "mapping": m}
	raw, _ := json.Marshal(request)
	for _, csrf := range []string{"", auth.CSRFToken} {
		r := authenticatedRequest(t, handler, cookie, csrf, http.MethodPost, base+"/preview", string(raw))
		if (csrf == "" && r.Code != 403) || (csrf != "" && r.Code != 200) {
			t.Fatal("preview CSRF")
		}
	}
	preview := authenticatedRequest(t, handler, cookie, auth.CSRFToken, http.MethodPost, base+"/preview", string(raw))
	var p struct {
		Preview store.ServiceAlertImportPreview `json:"preview"`
	}
	if json.Unmarshal(preview.Body.Bytes(), &p) != nil {
		t.Fatal("preview response")
	}
	for _, malformed := range []string{string(raw) + "{}", strings.Replace(string(raw), `"version":1`, `"version":99`, 1), strings.Replace(string(raw), `"enabled":false`, `"enabled":null`, 1), strings.Replace(string(raw), `"enabled":false,`, "", 1), strings.Replace(string(raw), `"cooldown_seconds":300`, `"cooldown_seconds":300,"credential":"private-marker"`, 1), `{"bundle":{},"mapping":{},"unknown":"private-marker"}`} {
		r := authenticatedRequest(t, handler, cookie, auth.CSRFToken, http.MethodPost, base+"/preview", malformed)
		if r.Code != 400 || strings.Contains(r.Body.String(), "private-marker") {
			t.Fatal("malformed/unknown field")
		}
	}
	request["request_id"] = "api-request"
	request["preview_digest"] = p.Preview.Digest
	raw, _ = json.Marshal(request)
	for i := 0; i < 2; i++ {
		r := authenticatedRequest(t, handler, cookie, auth.CSRFToken, http.MethodPost, base+"/apply", string(raw))
		if (i == 0 && r.Code != 201) || (i == 1 && r.Code != 200) {
			t.Fatal("API replay")
		}
	}
	current, err := db.ServiceAlertSettings(context.Background(), v.ID)
	if err != nil || current.Revision != 2 || !current.CertificateRemindersEnabled || current.Enabled {
		t.Fatal("wrong imported flags")
	}
	request["request_id"] = "stale-request"
	raw, _ = json.Marshal(request)
	r := authenticatedRequest(t, handler, cookie, auth.CSRFToken, http.MethodPost, base+"/apply", string(raw))
	if r.Code != 409 {
		t.Fatal("stale apply")
	}
}

func TestLiveServiceAlertTransferBrowser(t *testing.T) {
	if os.Getenv("MYPROBE_TEST_HTTP_BROWSER") != "1" {
		t.Skip("requires Edge and Playwright")
	}
	handler, db, _ := securityTestServer(t)
	defer db.Close()
	serviceTransferAPIFixture(t, db)
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "../../scripts/service-alert-transfer-ui.cjs")
	cmd.Env = append(os.Environ(), "UI_BASE_URL="+server.URL)
	output, err := cmd.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatal(err)
	}
}
