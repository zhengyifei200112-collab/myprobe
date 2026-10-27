package agentgateway_test

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"github.com/zhengyifei200112-collab/myprobe/internal/scheduler"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

const releasedNetworkAgentRef = "532352963ab47aed4af730c400f25163d0e6ed45"

func releasedNetworkFixture(t *testing.T) string {
	t.Helper()
	root := os.Getenv("MYPROBE_NETWORK_TEST_DIR")
	if root == "" {
		return t.TempDir()
	}
	if !filepath.IsAbs(root) {
		t.Fatal("MYPROBE_NETWORK_TEST_DIR must be absolute")
	}
	dir, err := os.MkdirTemp(root, "released-agent-")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// Build the unchanged pinned release source instead of simulating an old hello.
func buildReleasedNetworkAgent(t *testing.T, ctx context.Context, root string) string {
	t.Helper()
	archive, err := exec.CommandContext(ctx, "git", "archive", "--format=tar", releasedNetworkAgentRef).Output()
	if err != nil || len(archive) > 16<<20 {
		t.Fatalf("pinned release source unavailable: %v", err)
	}
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0o750); err != nil {
		t.Fatal(err)
	}
	reader := tar.NewReader(bytes.NewReader(archive))
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		name := filepath.Join(source, filepath.FromSlash(header.Name))
		rel, err := filepath.Rel(source, name)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			t.Fatal("release archive path escapes fixture")
		}
		if header.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(name, 0o750); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if header.Typeflag != tar.TypeReg || header.Size > 8<<20 {
			t.Fatal("unexpected release archive entry")
		}
		if err := os.MkdirAll(filepath.Dir(name), 0o750); err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(io.LimitReader(reader, header.Size+1))
		if err != nil || int64(len(body)) != header.Size {
			t.Fatal("incomplete release source")
		}
		if err := os.WriteFile(name, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	binary := filepath.Join(root, "released-agent")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.CommandContext(ctx, "go", "build", "-trimpath", "-ldflags", "-X main.version=v0.2.1", "-o", binary, "./cmd/agent")
	build.Dir = source
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("released Agent build: %s %v", output, err)
	}
	return binary
}

func TestNetworkReleasedAgentCompatibility(t *testing.T) {
	if os.Getenv("MYPROBE_TEST_RELEASE_AGENT") != "1" {
		t.Skip("requires pinned release source and local subprocess execution")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	root := releasedNetworkFixture(t)
	binary := buildReleasedNetworkAgent(t, ctx, root)
	database, err := store.Open(ctx, filepath.Join(root, "released-network.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	node, token, err := database.CreateNode(ctx, store.CreateNodeParams{Name: "released Agent observer"})
	if err != nil {
		t.Fatal(err)
	}
	targetListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	targetDone := make(chan struct{})
	go func() {
		defer close(targetDone)
		for {
			conn, err := targetListener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()
	defer func() { targetListener.Close(); <-targetDone }()
	port := targetListener.Addr().(*net.TCPAddr).Port
	target, err := database.CreateTarget(ctx, store.CreateTargetParams{Name: "owned loopback TCP", Kind: protocol.TaskKindTCPing, Host: "127.0.0.1", Port: &port, IntervalSeconds: 5, TimeoutMS: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AssignTarget(ctx, node.ID, target.ID); err != nil {
		t.Fatal(err)
	}
	gateway := agentgateway.New(database, agentgateway.NewHub())
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/agent/ws", gateway.WebSocket)
	server := httptest.NewServer(mux)
	defer server.Close()
	runCtx, stop := context.WithTimeout(ctx, 30*time.Second)
	defer stop()
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		scheduler.New(database, gateway, slog.New(slog.NewTextHandler(io.Discard, nil))).Run(runCtx)
	}()
	defer func() { stop(); <-workerDone }()
	logFile, err := os.Create(filepath.Join(root, "released-agent.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	process := exec.CommandContext(runCtx, binary, "-collection-interval", "1s", "-report-interval", "1s")
	for _, value := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(value), "MYPROBE_") {
			process.Env = append(process.Env, value)
		}
	}
	process.Env = append(process.Env, "MYPROBE_SERVER="+server.URL, "MYPROBE_TOKEN="+token)
	process.Stdout, process.Stderr = logFile, logFile
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	processDone := make(chan struct{})
	go func() { _ = process.Wait(); close(processDone) }()
	defer func() { stop(); <-processDone }()
	raw, err := sql.Open("sqlite", database.DatabasePath())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var completed int
	for runCtx.Err() == nil {
		if err := raw.QueryRowContext(runCtx, "SELECT COUNT(*) FROM network_probe_tasks WHERE node_id=? AND source='scheduled' AND state='completed' AND outcome='success'", node.ID).Scan(&completed); err != nil {
			t.Fatal(err)
		}
		if completed > 0 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if completed == 0 {
		t.Fatal("released Agent produced no admitted scheduled TCP result")
	}
	stop()
	<-workerDone
	<-processDone
	for deadline := time.Now().Add(2 * time.Second); gateway.NodeConnected(node.ID) && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	if gateway.NodeConnected(node.ID) {
		t.Fatal("released Agent session did not close")
	}
	// All writers have stopped before the final accounting and fixture cleanup.
	if err := raw.QueryRow("SELECT COUNT(*) FROM network_probe_tasks WHERE node_id=? AND source='scheduled' AND state='completed' AND outcome='success'", node.ID).Scan(&completed); err != nil {
		t.Fatal(err)
	}
	var samples int
	if err := raw.QueryRow("SELECT COUNT(*) FROM latency_samples WHERE node_id=? AND target_id=? AND success=1", node.ID, target.ID).Scan(&samples); err != nil || samples != completed {
		t.Fatalf("released result/task accounting: samples=%d completed=%d err=%v", samples, completed, err)
	}
	current, err := database.NodeDiagnostics(ctx, node.ID)
	if err != nil || current.AgentVersion != "v0.2.1" {
		t.Fatal("release handshake evidence missing", err)
	}
	program, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("release=%s binary_sha256=%x admitted_scheduled_results=%d; loopback only; subprocess pid=%d joined by teardown", releasedNetworkAgentRef, sha256.Sum256(program), completed, process.Process.Pid)
}
