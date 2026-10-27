package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/notificationlink"
	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func diagnosticAPIFixture(t *testing.T, db *store.Store) (store.HTTPService, store.Node, time.Time) {
	t.Helper()
	ctx := context.Background()
	node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "Diagnostic observer"})
	if err != nil {
		t.Fatal(err)
	}
	v, err := db.SaveHTTPService(ctx, store.HTTPService{Name: "Diagnostic fixture", Enabled: true, IntervalSeconds: 30, NodeIDs: []string{node.ID}, Spec: httpcheck.Spec{URL: "http://example.test/private-marker", Method: "GET", StatusCodes: []int{200}, TimeoutMS: 1000, MaxBodyBytes: 1024, Assertion: &httpcheck.Assertion{Kind: "text_contains", Text: "private-content-marker"}}})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	for i, class := range []string{"", "dns", "refused", "tls_invalid", "status_mismatch", "content_mismatch", "timeout", "unsupported", "policy_denied"} {
		point := at.Add(time.Duration(i) * time.Millisecond)
		task, err := db.CreateHTTPTask(ctx, v.ID, node.ID, point, point)
		if err != nil {
			t.Fatal(err)
		}
		r := httpcheck.Result{TaskID: task.ID, ServiceID: v.ID, Revision: task.Revision, ScheduledAt: point, CompletedAt: point, Outcome: "failure", ErrorClass: class, DurationMS: 5}
		switch class {
		case "":
			r.Outcome, r.StatusCode = "success", 200
		case "status_mismatch":
			r.StatusCode = 503
		case "content_mismatch":
			r.StatusCode = 200
		case "unsupported", "policy_denied":
			r.Outcome = "unobserved"
		}
		if err := db.SaveHTTPResult(ctx, node.ID, r, point); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.CreateHTTPTask(ctx, v.ID, node.ID, at.Add(10*time.Millisecond), at); err != nil {
		t.Fatal(err)
	}
	return v, node, at
}

func TestHTTPDiagnosticsAuthValidationAndPrivacy(t *testing.T) {
	handler, db, _ := securityTestServer(t)
	defer db.Close()
	v, node, at := diagnosticAPIFixture(t, db)
	start, end := at.Add(-time.Hour).Format(time.RFC3339Nano), at.Add(time.Hour).Format(time.RFC3339Nano)
	path := "/api/v1/admin/service-monitors/" + v.ID + "/diagnostics?" + url.Values{"node_id": {node.ID}, "start": {start}, "end": {end}, "limit": {"2"}}.Encode()
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, httptest.NewRequest(http.MethodGet, path, nil))
	if denied.Code != 401 || !strings.Contains(denied.Header().Get("Cache-Control"), "no-store") || strings.Contains(denied.Body.String(), v.Name) {
		t.Fatal("diagnostics auth/privacy")
	}
	login, auth := loginSecurityRequest(t, handler, "admin", "correct-password", "", "")
	if login.Code != 200 {
		t.Fatal("login")
	}
	cookie := login.Result().Cookies()[0]
	r := authenticatedRequest(t, handler, cookie, auth.CSRFToken, http.MethodGet, path, "")
	var data store.HTTPDiagnostics
	if r.Code != 200 || !strings.Contains(r.Header().Get("Cache-Control"), "no-store") || json.Unmarshal(r.Body.Bytes(), &data) != nil || len(data.Observations) != 2 || data.NextCursor == "" || data.Statistics.Expected != 0 || data.Observations[0].Status != "pending" {
		t.Fatal("diagnostic response")
	}
	for _, marker := range []string{"private-marker", "private-content-marker", "spec", "task_json", "certificates", "headers"} {
		if strings.Contains(r.Body.String(), marker) {
			t.Fatal("private diagnostic field")
		}
	}
	next := authenticatedRequest(t, handler, cookie, auth.CSRFToken, http.MethodGet, path+"&after="+data.NextCursor, "")
	if next.Code != 200 || !strings.Contains(next.Body.String(), `"error_class":"unsupported"`) {
		t.Fatal("API cursor")
	}
	for _, suffix := range []string{"&start=" + url.QueryEscape(start), "&end=" + url.QueryEscape(end), "&node_id=other", "&limit=2", "&limit=101", "&after=bad", "&target=private-marker", "&after=x&after=y", "&bad=%ZZ"} {
		r := authenticatedRequest(t, handler, cookie, auth.CSRFToken, http.MethodGet, path+suffix, "")
		if r.Code != 400 || strings.Contains(r.Body.String(), "private-marker") {
			t.Fatal("invalid query accepted/leaked")
		}
		denied := httptest.NewRecorder()
		handler.ServeHTTP(denied, httptest.NewRequest(http.MethodGet, path+suffix, nil))
		if denied.Code != 401 {
			t.Fatal("validation before auth")
		}
	}
	for _, rangeQuery := range []url.Values{{"node_id": {node.ID}, "start": {"invalid"}, "end": {end}}, {"node_id": {node.ID}, "start": {start}, "end": {at.Add(32 * 24 * time.Hour).Format(time.RFC3339Nano)}}, {"start": {start}, "end": {end}}} {
		r := authenticatedRequest(t, handler, cookie, auth.CSRFToken, http.MethodGet, strings.Split(path, "?")[0]+"?"+rangeQuery.Encode(), "")
		if r.Code != 400 {
			t.Fatal("invalid range")
		}
	}
	for _, missing := range []string{strings.Replace(path, v.ID, "missing-service", 1), strings.Replace(path, node.ID, "missing-node", 1)} {
		if r := authenticatedRequest(t, handler, cookie, auth.CSRFToken, http.MethodGet, missing, ""); r.Code != 404 {
			t.Fatal("missing resource")
		}
	}
}

func TestLiveServiceDiagnosticsControlledBrowser(t *testing.T) {
	if os.Getenv("MYPROBE_TEST_HTTP_BROWSER") != "1" {
		t.Skip("requires Edge and Playwright")
	}
	handler, db, _ := securityTestServer(t)
	defer db.Close()
	v, node, at := diagnosticAPIFixture(t, db)
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "../../scripts/service-diagnostics-ui.cjs")
	cmd.Env = append(os.Environ(), "DIAGNOSTIC_URL="+notificationlink.ServiceDiagnosticAt(server.URL, v.ID, node.ID, at), "DIAGNOSTIC_USERNAME=admin", "DIAGNOSTIC_PASSWORD=correct-password", "DIAGNOSTIC_CONTROLLED=1")
	output, err := cmd.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatal(err)
	}
}
