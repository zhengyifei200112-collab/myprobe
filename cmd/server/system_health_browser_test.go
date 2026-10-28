package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLiveSystemHealthMainBrowser(t *testing.T) {
	if os.Getenv("MYPROBE_TEST_SYSTEM_HEALTH") != "1" {
		t.Skip("requires opt-in Edge/Playwright and a disposable real Server")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	binary := filepath.Join(httpFixtureDir(t), "health-server")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.CommandContext(ctx, "go", "build", "-ldflags", "-X main.version=health-live-acceptance", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("fixture build: %s %v", output, err)
	}
	address := browserFixtureAddress(t)
	serverCtx, stopServer := context.WithCancel(ctx)
	server := exec.CommandContext(serverCtx, binary)
	server.Env = append(os.Environ(), "MYPROBE_RELEASE_CHECKS_AUTO=false", "MYPROBE_LISTEN="+address, "MYPROBE_DATABASE="+filepath.Join(httpFixtureDir(t), "health.db"), "MYPROBE_ADMIN_USERNAME=admin", "MYPROBE_ADMIN_PASSWORD=health-local-test-password", "MYPROBE_ENCRYPTION_KEY="+strings.Repeat("s", 32), "MYPROBE_HTTP_PROBES_ENABLED=true")
	server.Stdout, server.Stderr = io.Discard, io.Discard
	if err := server.Start(); err != nil {
		stopServer()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Wait() }()
	defer func() { stopServer(); <-done }()
	base := "http://" + address
	ready := false
	client := &http.Client{Timeout: time.Second}
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
		response, err := client.Get(base + "/healthz")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == 200 {
				ready = true
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ready {
		t.Fatal("disposable Server did not become ready")
	}
	command := exec.CommandContext(ctx, "node", "../../scripts/system-health-live-acceptance.cjs")
	command.Env = append(os.Environ(), "HEALTH_TEST_URL="+base, "HEALTH_TEST_PASSWORD=health-local-test-password")
	output, err := command.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("MYPROBE_TEST_AGENT_RUNTIME_BROWSER") == "1" {
		agentBinary := filepath.Join(httpFixtureDir(t), "runtime-agent")
		if runtime.GOOS == "windows" {
			agentBinary += ".exe"
		}
		buildAgent := exec.CommandContext(ctx, "go", "build", "-ldflags", "-X main.version=runtime-evidence-acceptance", "-o", agentBinary, "../agent")
		if output, err := buildAgent.CombinedOutput(); err != nil {
			t.Fatalf("Agent fixture build: %s %v", output, err)
		}
		command := exec.CommandContext(ctx, "node", "../../scripts/agent-runtime-live-acceptance.cjs")
		command.Env = append(os.Environ(), "HEALTH_TEST_URL="+base, "HEALTH_TEST_PASSWORD=health-local-test-password", "RUNTIME_TEST_AGENT="+agentBinary)
		output, err := command.CombinedOutput()
		t.Log(string(output))
		if err != nil {
			t.Fatal(err)
		}
	}
}
