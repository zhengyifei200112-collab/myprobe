package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestPolicyImportPreviewAPI(t *testing.T) {
	handler, db, _ := securityTestServer(t)
	defer db.Close()
	channel, err := db.CreateNotificationChannel(context.Background(), "fixture", store.ChannelKindWebhook, "secret-marker")
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/admin/alert-policies/import/preview"
	body := fmt.Sprintf(`{"bundle":{"format":"myprobe-alert-policies","version":1,"policies":[{"source_id":"source","name":"CPU","policy_key":"cpu","enabled":true,"scope":{"kind":"all"},"channel_id":"source-channel","kind":"cpu","config":{"threshold_percent":90,"recovery_seconds":0},"cooldown_seconds":900}]},"mapping":{"channels":{"source-channel":%q}}}`, channel.ID)
	r := httptest.NewRecorder()
	handler.ServeHTTP(r, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
	if r.Code != 401 || !strings.Contains(r.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("auth: %d", r.Code)
	}
	login, credentials := loginSecurityRequest(t, handler, "admin", "correct-password", "", "")
	if login.Code != 200 {
		t.Fatal(login.Code)
	}
	cookie := login.Result().Cookies()[0]
	if r := authenticatedRequest(t, handler, cookie, "", http.MethodPost, path, body); r.Code != 403 {
		t.Fatalf("CSRF: %d", r.Code)
	}
	r = authenticatedRequest(t, handler, cookie, credentials.CSRFToken, http.MethodPost, path, body)
	if r.Code != 200 || !strings.Contains(r.Header().Get("Cache-Control"), "no-store") || !strings.Contains(r.Body.String(), `"create_count":1`) || !strings.Contains(r.Body.String(), channel.ID) || !strings.Contains(r.Body.String(), `"recovery_seconds":0`) || strings.Contains(r.Body.String(), "secret-marker") {
		t.Fatalf("preview: %d %s", r.Code, r.Body.String())
	}
	for _, bad := range []string{body + `{}`, strings.Replace(body, `"threshold_percent":90`, `"unknown":90`, 1), strings.Replace(body, `"version":1`, `"version":2`, 1), strings.Replace(body, channel.ID, "missing", 1), strings.Replace(body, `"name":"CPU"`, `"name":"`+strings.Repeat("x", 16<<20)+`"`, 1)} {
		if r := authenticatedRequest(t, handler, cookie, credentials.CSRFToken, http.MethodPost, path, bad); r.Code != 400 {
			t.Fatalf("bad request: %d %s", r.Code, r.Body.String())
		}
	}
	items, err := db.ListAlertPolicies(context.Background())
	if err != nil || len(items) != 0 {
		t.Fatalf("preview mutated policies: %+v %v", items, err)
	}
}

func TestAlertPolicyAdministration(t *testing.T) {
	handler, db, _ := securityTestServer(t)
	defer db.Close()
	ctx := context.Background()
	node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	channel, err := db.CreateNotificationChannel(ctx, "fixture", store.ChannelKindWebhook, "private-channel-marker")
	if err != nil {
		t.Fatal(err)
	}
	root := "/api/v1/admin/alert-policies"
	for _, path := range []string{root, root + "/export", root + "/effective/" + node.ID, root + "/missing"} {
		r := httptest.NewRecorder()
		handler.ServeHTTP(r, httptest.NewRequest(http.MethodGet, path, nil))
		if r.Code != 401 || !strings.Contains(r.Header().Get("Cache-Control"), "no-store") {
			t.Fatalf("unauthenticated access %d %v", r.Code, r.Header())
		}
	}
	login, credentials := loginSecurityRequest(t, handler, "admin", "correct-password", "", "")
	if login.Code != 200 {
		t.Fatal(login.Code)
	}
	cookie := login.Result().Cookies()[0]
	body := fmt.Sprintf(`{"name":"fixture","policy_key":"cpu.warning","enabled":true,"priority":0,"scope":{"kind":"all"},"kind":"cpu","channel_id":%q,"config":{"threshold_percent":80},"cooldown_seconds":60}`, channel.ID)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		return authenticatedRequest(t, handler, cookie, credentials.CSRFToken, method, path, body)
	}
	if r := authenticatedRequest(t, handler, cookie, "", http.MethodPost, root, body); r.Code != 403 {
		t.Fatalf("CSRF %d", r.Code)
	}
	created := request(http.MethodPost, root, body)
	if created.Code != 201 || !strings.Contains(created.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("create %d %s", created.Code, created.Body.String())
	}
	var value struct {
		Policy     store.AlertPolicy `json:"policy"`
		Evaluation bool              `json:"evaluation_enabled"`
	}
	if err = json.Unmarshal(created.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	if !value.Evaluation || value.Policy.Revision != 1 || !strings.Contains(created.Body.String(), `"state":"pending"`) || strings.Contains(created.Body.String(), "private-channel-marker") {
		t.Fatal("incorrect state or leaked channel configuration")
	}
	id := value.Policy.ID
	exported := request(http.MethodGet, root+"/export", "")
	if exported.Code != 200 || !strings.Contains(exported.Body.String(), `"source_id":"`+id+`"`) || !strings.Contains(exported.Body.String(), `"format":"myprobe-alert-policies"`) || strings.Contains(exported.Body.String(), "private-channel-marker") || exported.Header().Get("Content-Disposition") == "" || !strings.Contains(exported.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("policy export contract: %d %s", exported.Code, exported.Body.String())
	}
	if err = db.SyncAlertPolicyRules(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	rules, err := db.ListAlertRules(ctx)
	if err != nil || len(rules) != 1 || rules[0].PolicyID != id {
		t.Fatalf("materialized rules: %+v %v", rules, err)
	}
	listedRules := request(http.MethodGet, "/api/v1/admin/alert-rules", "")
	if listedRules.Code != 200 || !strings.Contains(listedRules.Body.String(), `"policy_id":"`+id+`"`) {
		t.Fatalf("rule ownership missing: %d %s", listedRules.Code, listedRules.Body.String())
	}
	rulePath := "/api/v1/admin/alert-rules/" + rules[0].ID
	legacyUpdate := fmt.Sprintf(`{"node_id":%q,"channel_id":%q,"kind":"cpu","config":{"threshold_percent":95},"enabled":false,"cooldown_seconds":60}`, node.ID, channel.ID)
	for _, method := range []string{http.MethodPatch, http.MethodDelete} {
		r := request(method, rulePath, legacyUpdate)
		if r.Code != 409 || !strings.Contains(r.Body.String(), "managed by a scoped policy") {
			t.Fatalf("managed write: %d %s", r.Code, r.Body.String())
		}
	}
	unchanged, err := db.AlertRule(ctx, rules[0].ID)
	if err != nil || unchanged.PolicyID != id || !unchanged.Enabled || string(unchanged.Config) != string(rules[0].Config) {
		t.Fatal("managed rule changed through legacy endpoint", err)
	}
	channelPath := "/api/v1/admin/notification-channels/" + channel.ID
	blockedDelete := request(http.MethodDelete, channelPath, "")
	if blockedDelete.Code != 409 || !strings.Contains(blockedDelete.Body.String(), "update or delete the policy first") || strings.Contains(blockedDelete.Body.String(), "FOREIGN KEY") {
		t.Fatalf("referenced channel deletion: %d %s", blockedDelete.Code, blockedDelete.Body.String())
	}
	if r := request(http.MethodPost, root, body); r.Code != 409 {
		t.Fatalf("scope conflict %d", r.Code)
	}
	for _, bad := range []string{strings.Replace(body, `"enabled":true,`, "", 1), strings.Replace(body, `"threshold_percent":80`, `"threshold_percnt":80`, 1), body + ` {}`, strings.Replace(body, `"priority":0`, `"priority":0,"extra":true`, 1), strings.Replace(body, `"name":"fixture"`, `"name":"`+strings.Repeat("x", 65536)+`"`, 1)} {
		if r := request(http.MethodPost, root, bad); r.Code != 400 {
			t.Fatalf("invalid input %d", r.Code)
		}
	}
	updatedBody := strings.Replace(body, `"priority":0`, `"priority":1,"revision":1`, 1)
	if r := request(http.MethodPut, root+"/"+id, updatedBody); r.Code != 200 {
		t.Fatalf("update %d %s", r.Code, r.Body.String())
	}
	if r := request(http.MethodPut, root+"/"+id, updatedBody); r.Code != 409 {
		t.Fatalf("stale update %d", r.Code)
	}
	preview := request(http.MethodGet, root+"/effective/"+node.ID, "")
	if preview.Code != 200 || !strings.Contains(preview.Body.String(), `"selected_id":"`+id+`"`) || !strings.Contains(preview.Body.String(), `"evaluation_enabled":true`) {
		t.Fatalf("preview %d %s", preview.Code, preview.Body.String())
	}
	if r := request(http.MethodGet, root+"/effective/missing", ""); r.Code != 404 {
		t.Fatal(r.Code)
	}
	if r := request(http.MethodGet, root+"?limit=101", ""); r.Code != 400 {
		t.Fatal(r.Code)
	}
	if r := request(http.MethodGet, root+"/"+id, ""); r.Code != 200 {
		t.Fatal(r.Code)
	}
	if r := request(http.MethodDelete, root+"/"+id+"?revision=1", ""); r.Code != 409 {
		t.Fatal(r.Code)
	}
	if r := authenticatedRequest(t, handler, cookie, "", http.MethodDelete, root+"/"+id+"?revision=2", ""); r.Code != 403 {
		t.Fatal(r.Code)
	}
	if r := request(http.MethodDelete, root+"/"+id+"?revision=2", ""); r.Code != 204 {
		t.Fatal(r.Code)
	}
	if r := request(http.MethodGet, root+"/"+id, ""); r.Code != 404 {
		t.Fatal(r.Code)
	}
	if r := request(http.MethodDelete, channelPath, ""); r.Code != 204 {
		t.Fatalf("unreferenced channel deletion: %d %s", r.Code, r.Body.String())
	}
	if r := request(http.MethodGet, root, ""); r.Code != 200 || !strings.Contains(r.Body.String(), `"policies":[]`) {
		t.Fatalf("list %d %s", r.Code, r.Body.String())
	}
}
