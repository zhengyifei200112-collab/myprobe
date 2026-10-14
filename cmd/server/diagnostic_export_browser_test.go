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

func TestLiveDiagnosticExportMainBrowser(t *testing.T) {
	if os.Getenv("MYPROBE_TEST_DIAGNOSTIC_EXPORT") != "1" {
		t.Skip("requires opt-in Edge/Playwright and a disposable actual Server")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	baseDir := t.TempDir()
	binary := filepath.Join(baseDir, "diagnostic-server")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.CommandContext(ctx, "go", "build", "-ldflags", "-X main.version=v1.2.3-private-build-203.0.113.9", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("fixture build: %s %v", output, err)
	}
	address := browserFixtureAddress(t)
	serverCtx, stopServer := context.WithCancel(ctx)
	server := exec.CommandContext(serverCtx, binary)
	key := strings.Repeat("synthetic-diagnostic-key-", 3)
	server.Env = append(os.Environ(), "MYPROBE_LISTEN="+address, "MYPROBE_DATABASE="+filepath.Join(baseDir, "private-source.db"), "MYPROBE_ADMIN_USERNAME=admin", "MYPROBE_ADMIN_PASSWORD=diagnostic-local-test-password", "MYPROBE_ENCRYPTION_KEY="+key, "MYPROBE_BACKUP_PASSPHRASE_FILE="+filepath.Join(baseDir, "private-secret-file"), "MYPROBE_NOTIFICATION_ORIGIN=https://example.invalid")
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
		t.Fatal("actual fixture Server did not become ready")
	}
	command := exec.CommandContext(ctx, "node", "../../scripts/diagnostic-export-live-acceptance.cjs")
	command.Env = append(os.Environ(), "DIAGNOSTIC_TEST_URL="+base, "DIAGNOSTIC_TEST_PRIVATE_PATH="+baseDir, "DIAGNOSTIC_TEST_PRIVATE_KEY="+key)
	output, err := command.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatal(err)
	}
}
