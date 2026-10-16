package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/auth"
	"github.com/zhengyifei200112-collab/myprobe/internal/config"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"github.com/zhengyifei200112-collab/myprobe/internal/releasecheck"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

type releaseTransport func(*http.Request) (*http.Response, error)

func (f releaseTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestReleaseStatusAdminContract(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "private-release-fixture.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	authService := auth.New(db, time.Hour)
	if _, err = authService.Bootstrap(ctx, "admin", "release-test-password"); err != nil {
		t.Fatal(err)
	}
	session, token, err := authService.Login(ctx, "admin", "release-test-password")
	if err != nil {
		t.Fatal(err)
	}
	node, nodeToken, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "private-node-marker"})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.SaveAgentMetadata(ctx, node.ID, protocol.Hello{AgentVersion: "v1.2.0", Hostname: "private-host-marker", OS: "linux", Architecture: "amd64", CollectionSeconds: 5, ReportSeconds: 5, Capabilities: []string{"metrics.v1"}}); err != nil {
		t.Fatal(err)
	}
	unknown, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "no-hello"})
	if err != nil {
		t.Fatal(err)
	}
	var upstreamCalls atomic.Int32
	checker := releasecheck.New(ctx, false, releaseTransport(func(r *http.Request) (*http.Response, error) {
		upstreamCalls.Add(1)
		if r.URL.String() != releasecheck.Endpoint || strings.Contains(r.URL.String(), node.ID) || r.Header.Get("Authorization") != "" {
			t.Error("private data in outbound request")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"tag_name":"v1.2.3","published_at":"2026-01-01T00:00:00Z","body":"private-upstream-marker","html_url":"https://untrusted.invalid"}`))}, nil
	}))
	hub := agentgateway.NewHub()
	server := New(config.Config{}, db, authService, agentgateway.New(db, hub), hub, WithReleaseChecker(checker), WithRuntimeIdentity("v1.2.1", time.Now()))
	request := func(method, path, cookie, csrf string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
		}
		if csrf != "" {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, r)
		if w.Header().Get("Cache-Control") != "private, no-store" {
			t.Error("release response cacheable")
		}
		return w
	}
	base := "/api/v1/admin/system/releases"
	shareRequest := httptest.NewRequest(http.MethodGet, base, nil)
	shareRequest.AddCookie(&http.Cookie{Name: "myprobe_share", Value: token})
	shareResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(shareResponse, shareRequest)
	if shareResponse.Code != http.StatusUnauthorized {
		t.Fatal("share cookie authorized private releases")
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		path := base
		if method == http.MethodPost {
			path += "/check"
		}
		for _, cookie := range []string{"", "invalid"} {
			if got := request(method, path, cookie, ""); got.Code != 401 {
				t.Fatalf("unauthorized: %d", got.Code)
			}
		}
	}
	for _, csrf := range []string{"", "wrong"} {
		if got := request(http.MethodPost, base+"/check", token, csrf); got.Code != 403 {
			t.Fatalf("CSRF: %d", got.Code)
		}
	}
	if upstreamCalls.Load() != 0 {
		t.Fatal("unauthorized outbound request")
	}
	read := request(http.MethodGet, base, token, "")
	if read.Code != 200 || upstreamCalls.Load() != 0 || !strings.Contains(read.Body.String(), `"status":"not_checked"`) {
		t.Fatalf("GET performed check: %s", read.Body)
	}
	for _, query := range []string{"?url=https://untrusted.invalid", "?node_id=", "?node_id=a&node_id=b", "?force=true", "?node_id=%zz", "?node_id=" + strings.Repeat("a", 129)} {
		if got := request(http.MethodPost, base+"/check"+query, token, session.CSRFToken); got.Code != 400 {
			t.Fatalf("query %q: %d", query, got.Code)
		}
	}
	if got := request(http.MethodPost, base+"/check?node_id=absent", token, session.CSRFToken); got.Code != 404 || upstreamCalls.Load() != 0 {
		t.Fatal("unknown node consumed check")
	}
	w := request(http.MethodPost, base+"/check?node_id="+node.ID, token, session.CSRFToken)
	if w.Code != 200 {
		t.Fatalf("check: %d %s", w.Code, w.Body)
	}
	var response releaseStatusResponse
	if err = json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Upstream.Status != "available" || response.Server.Status != "update_available" || response.Agent == nil || response.Agent.Comparison.Status != "update_available" || response.Agent.Evidence != "last_advertised" || response.Agent.HelloReceivedAt == nil {
		t.Fatalf("response: %+v", response)
	}
	for _, secret := range []string{token, session.CSRFToken, nodeToken, "private-node-marker", "private-host-marker", "private-upstream-marker", "untrusted.invalid", "capabilities", "compatible"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Errorf("response leaked %q", secret)
		}
	}
	var top map[string]json.RawMessage
	if err = json.Unmarshal(w.Body.Bytes(), &top); err != nil || len(top) != 5 {
		t.Fatal("release DTO whitelist changed")
	}
	w = request(http.MethodGet, base+"?node_id="+unknown.ID, token, "")
	if err = json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Agent == nil || response.Agent.Evidence != "not_advertised" || response.Agent.Comparison.Status != "current_version_unknown" {
		t.Fatalf("invented Agent version: %s", w.Body)
	}
	request(http.MethodPost, base+"/check", token, session.CSRFToken)
	if upstreamCalls.Load() != 1 {
		t.Fatal("manual cooldown bypassed")
	}
}
