package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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

// Actual cookie/CSRF, Gin, Store, checker and embedded UI; only the fixed public
// release transport is owned synthetic data. No external release is required.
func TestReleaseAwarenessLiveBrowser(t *testing.T) {
	if os.Getenv("MYPROBE_TEST_RELEASE_BROWSER") != "1" {
		t.Skip("opt-in actual release UI")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	db, err := store.Open(ctx, filepath.Join(networkQualityAPIFixtureDir(t), "release-browser.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	a := auth.New(db, time.Hour)
	if _, err = a.Bootstrap(ctx, "admin", "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "版本验收节点"})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.SaveAgentMetadata(ctx, node.ID, protocol.Hello{AgentVersion: "v1.2.0", Hostname: "synthetic-host", OS: "linux", Architecture: "amd64", CollectionSeconds: 5, ReportSeconds: 5, Capabilities: []string{"metrics.v1"}}); err != nil {
		t.Fatal(err)
	}
	unknown, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "尚无握手节点"})
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	checker := releasecheck.New(ctx, false, releaseTransport(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"tag_name":"v1.2.3","published_at":"2026-01-01T00:00:00Z"}`))}, nil
	}))
	hub := agentgateway.NewHub()
	api := New(config.Config{}, db, a, agentgateway.New(db, hub), hub, WithReleaseChecker(checker), WithRuntimeIdentity("v1.2.1", time.Now()))
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	command := exec.CommandContext(ctx, "node", "../../scripts/release-awareness-browser.cjs")
	command.Env = append(os.Environ(), "UI_BASE_URL="+server.URL, "QA_RELEASE_NODE="+node.ID, "QA_RELEASE_UNKNOWN_NODE="+unknown.ID)
	output, err := command.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("six views bypassed cooldown: upstream calls=%d", calls.Load())
	}
}
