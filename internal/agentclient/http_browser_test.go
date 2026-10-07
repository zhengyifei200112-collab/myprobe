package agentclient

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/auth"
	"github.com/zhengyifei200112-collab/myprobe/internal/collector"
	"github.com/zhengyifei200112-collab/myprobe/internal/config"
	"github.com/zhengyifei200112-collab/myprobe/internal/httpapi"
	"github.com/zhengyifei200112-collab/myprobe/internal/scheduler"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestHTTPBrowserLive(t *testing.T) {
	if os.Getenv("MYPROBE_TEST_HTTP_BROWSER") != "1" {
		t.Skip("opt-in browser integration requires Node, Playwright and a private interface")
	}
	agentConfig, targetURL := startPrivateHTTPTarget(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "browser.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	secret := make([]byte, 24)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	password := hex.EncodeToString(secret)
	authService := auth.New(db, time.Hour)
	if _, err := authService.Bootstrap(ctx, "fixture-admin", password); err != nil {
		t.Fatal(err)
	}
	_, token, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "fixture observer"})
	if err != nil {
		t.Fatal(err)
	}
	hub := agentgateway.NewHub()
	gateway := agentgateway.New(db, hub)
	gateway.EnableHTTPProbes(true)
	api := httpapi.New(config.Config{SessionTTL: time.Hour, EncryptionKey: password}, db, authService, gateway, hub)
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	agentConfig.ServerURL, agentConfig.Token = server.URL, token
	agentConfig.CollectionPeriod, agentConfig.ReportPeriod = time.Second, time.Second
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client, err := New(agentConfig, collector.New(collector.Config{}), logger)
	if err != nil {
		t.Fatal(err)
	}
	agentDone, schedulerDone := make(chan struct{}), make(chan struct{})
	go func() { defer close(agentDone); _ = client.Run(ctx) }()
	go func() { defer close(schedulerDone); scheduler.NewHTTP(db, gateway, logger).Run(ctx) }()
	defer func() {
		cancel()
		for _, done := range []chan struct{}{agentDone, schedulerDone} {
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("fixture worker did not stop")
			}
		}
	}()
	for client.currentConnection() == nil && ctx.Err() == nil {
		time.Sleep(time.Millisecond)
	}
	command := exec.CommandContext(ctx, "node", filepath.Join("..", "..", "scripts", "http-services-live-ui.cjs"))
	command.Env = append(os.Environ(), "UI_BASE_URL="+server.URL, "HTTP_FIXTURE_TARGET="+targetURL, "HTTP_FIXTURE_PASSWORD="+password)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("live browser failed: %v\n%s", err, output)
	}
	t.Log(string(output))
}
