package agentclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestJointStartupReadinessClassification(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Error("readiness requested an unexpected endpoint")
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	client := &http.Client{Timeout: time.Second}
	t.Run("deadline", func(t *testing.T) {
		result := waitJointReadiness(context.Background(), client, server.URL, make(chan error), 100*time.Millisecond)
		if result.class != "readiness_deadline" || result.exited {
			t.Fatalf("readiness = %+v", result)
		}
	})
	t.Run("parent", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if result := waitJointReadiness(ctx, client, server.URL, make(chan error), time.Minute); result.class != "parent_context_ended" {
			t.Fatalf("readiness = %+v", result)
		}
	})
	t.Run("early-exit", func(t *testing.T) {
		done := make(chan error, 1)
		original := errors.New("synthetic process completion")
		done <- original
		result := waitJointReadiness(context.Background(), client, server.URL, done, time.Minute)
		if result.class != "process_exited" || !result.exited || result.err != original {
			t.Fatalf("readiness = %+v", result)
		}
	})
	t.Run("healthy", func(t *testing.T) {
		healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
		defer healthy.Close()
		if result := waitJointReadiness(context.Background(), client, healthy.URL, make(chan error), time.Minute); result.class != "ready" {
			t.Fatalf("readiness = %+v", result)
		}
	})
}

func TestJointStartupPrivateCaptureBoundaries(t *testing.T) {
	if runtime.GOOS == "windows" {
		if _, err := openJointProcessLog(t.TempDir()); err == nil {
			t.Fatal("capture accepted without a POSIX permission check")
		}
		return
	}
	private := t.TempDir()
	if err := os.Chmod(private, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := openJointProcessLog("relative"); err == nil {
		t.Fatal("accepted relative output")
	}
	public := filepath.Join(private, "public")
	if err := os.Mkdir(public, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := openJointProcessLog(public); err == nil {
		t.Fatal("accepted group/other readable output")
	}
	link := filepath.Join(private, "link")
	if err := os.Symlink(private, link); err != nil {
		t.Fatal(err)
	}
	if _, err := openJointProcessLog(link); err == nil {
		t.Fatal("accepted symlink output")
	}
	capture, err := openJointProcessLog(private)
	if err != nil {
		t.Fatal(err)
	}
	const sentinel = "synthetic-private-fixture-credential"
	data := bytes.Repeat([]byte(sentinel), jointProcessLogLimit/len(sentinel)+100)
	var writers sync.WaitGroup
	for range 3 {
		writers.Go(func() {
			if n, err := capture.Write(data); err != nil || n != len(data) {
				t.Error("capture returned a pipe error or short write")
			}
		})
	}
	writers.Wait()
	capture.close()
	capture.close()
	if err := capture.recordStartup("readiness_deadline", time.Second, false, nil); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(capture.file.Name())
	if err != nil || info.Size() != jointProcessLogLimit || info.Mode().Perm() != 0o600 {
		t.Fatal("private log cap or permissions incorrect")
	}
	raw, err := os.ReadFile(capture.file.Name() + ".startup.json")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(sentinel)) || bytes.Contains(raw, []byte(private)) {
		t.Fatal("startup summary copied private contents or absolute paths")
	}
	var summary jointStartupEvidence
	if err := json.Unmarshal(raw, &summary); err != nil || summary.LogBytes != jointProcessLogLimit || summary.DiscardedBytes != int64(3*len(data)-jointProcessLogLimit) || summary.LogWriteErrors != 0 || summary.ExitCode != nil {
		t.Fatal("startup summary did not describe bounded capture")
	}
	if err := capture.recordStartup("ready", 0, false, nil); err == nil {
		t.Fatal("startup evidence was overwritten")
	}
}

// The parent expects a failing child test, never copies its raw output to the
// public test log, and verifies diagnostics from the actual supplied Server.
func TestJointStartupFailureEvidence(t *testing.T) {
	if os.Getenv("MYPROBE_TEST_STARTUP_FAILURE_CHILD") == "1" {
		root := os.Getenv("MYPROBE_TEST_STARTUP_FAILURE_ROOT")
		server := jointServer{binary: os.Getenv("MYPROBE_TEST_LOAD_SERVER_BINARY"), env: []string{
			"MYPROBE_DATABASE=" + filepath.Join(root, "database-directory"), "MYPROBE_LISTEN=127.0.0.1:0",
			"MYPROBE_ADMIN_USERNAME=startup-fixture", "MYPROBE_ADMIN_PASSWORD=synthetic-fixture-password",
			"MYPROBE_ENCRYPTION_KEY=" + strings.Repeat("test-key", 8), "MYPROBE_RELEASE_CHECKS_AUTO=false",
		}}
		server.start(t, context.Background(), "http://127.0.0.1:0")
		server.stop()
		t.Fatal("invalid database unexpectedly became ready")
	}
	binary := os.Getenv("MYPROBE_TEST_LOAD_SERVER_BINARY")
	if runtime.GOOS != "linux" || binary == "" {
		t.Skip("real startup failure requires the joint runner's supplied Linux Server")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("Server must be absolute")
	}
	root := t.TempDir()
	private := filepath.Join(root, "private")
	if err := os.Mkdir(private, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "database-directory"), 0o700); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, self, "-test.run=^TestJointStartupFailureEvidence$", "-test.count=1", "-test.v")
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "MYPROBE_") {
			child.Env = append(child.Env, env)
		}
	}
	child.Env = append(child.Env, "MYPROBE_TEST_LOAD_SERVER_BINARY="+binary, "MYPROBE_TEST_STARTUP_FAILURE_CHILD=1",
		"MYPROBE_TEST_STARTUP_FAILURE_ROOT="+root, "MYPROBE_TEST_LOAD_PROCESS_LOG_DIR="+private)
	output, runErr := child.CombinedOutput()
	if err := os.WriteFile(filepath.Join(private, "child-test.log"), output, 0o600); err != nil {
		t.Fatal(err)
	}
	var exit *exec.ExitError
	if ctx.Err() != nil || !errors.As(runErr, &exit) || exit.ExitCode() != 1 {
		t.Fatal("expected prompt failing child; inspect private child log")
	}
	files, err := filepath.Glob(filepath.Join(private, "*.startup.json"))
	if err != nil || len(files) != 1 {
		t.Fatal("expected one private startup record")
	}
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var summary jointStartupEvidence
	if err := json.Unmarshal(raw, &summary); err != nil || summary.Class != "process_exited" || !summary.ExitedBeforeEnd || summary.ExitCode == nil || *summary.ExitCode != 1 || summary.LogWriteErrors != 0 || summary.DiscardedBytes != 0 {
		t.Fatal("expected real early process exit with complete private diagnostics")
	}
	log, err := os.ReadFile(filepath.Join(private, summary.LogFile))
	if err != nil {
		t.Fatal(err)
	}
	started, completed, databaseError := false, false, false
	for _, line := range bytes.Split(log, []byte("\n")) {
		var entry map[string]any
		if json.Unmarshal(line, &entry) != nil {
			continue
		}
		started = started || entry["phase"] == "database_open" && entry["state"] == "started"
		completed = completed || entry["phase"] == "database_open" && entry["state"] == "completed"
		databaseError = databaseError || entry["msg"] == "open database"
	}
	if !started || completed || !databaseError {
		t.Fatal("private log did not identify incomplete database-open phase and its error")
	}
	t.Log("actual Server early failure retained privately; database-open phase and exit 1 verified")
}
