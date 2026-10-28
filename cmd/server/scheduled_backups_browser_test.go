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

func TestLiveScheduledBackupMainBrowser(t *testing.T) {
	if os.Getenv("MYPROBE_TEST_SCHEDULED_BACKUPS") != "1" {
		t.Skip("requires opt-in Edge/Playwright and a disposable actual Server")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	baseDir := httpFixtureDir(t)
	binary := filepath.Join(baseDir, "backup-server")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.CommandContext(ctx, "go", "build", "-ldflags", "-X main.version=backup-live-acceptance", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("fixture build: %s %v", output, err)
	}
	address := browserFixtureAddress(t)
	passFile := filepath.Join(baseDir, "independent-secret")
	if err := os.WriteFile(passFile, []byte("synthetic-backup-passphrase\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	serverCtx, stopServer := context.WithCancel(ctx)
	server := exec.CommandContext(serverCtx, binary)
	server.Env = append(os.Environ(), "MYPROBE_LISTEN="+address, "MYPROBE_DATABASE="+filepath.Join(baseDir, "source.db"), "MYPROBE_ADMIN_USERNAME=admin", "MYPROBE_ADMIN_PASSWORD=backup-local-test-password", "MYPROBE_ENCRYPTION_KEY="+strings.Repeat("synthetic-key-", 4), "MYPROBE_BACKUP_PASSPHRASE_FILE="+passFile)
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
	command := exec.CommandContext(ctx, "node", "../../scripts/scheduled-backups-live-acceptance.cjs")
	command.Env = append(os.Environ(), "BACKUP_TEST_URL="+base, "BACKUP_TEST_DIRECTORY="+filepath.Join(baseDir, "archives"), "BACKUP_TEST_PASSPHRASE_FILE="+passFile)
	output, err := command.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatal(err)
	}
}
