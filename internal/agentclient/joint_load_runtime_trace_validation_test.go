package agentclient

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestJointRuntimeGCTraceEnvironment(t *testing.T) {
	input := []string{"OWNED=value", "GODEBUG=gctrace=0", "GODEBUG=invalid-duplicate"}
	original := append([]string(nil), input...)
	if got := jointRuntimeGCEnvironment(input, false); !reflect.DeepEqual(got, input) {
		t.Fatal("disabled tracing changed the supplied environment")
	}
	if got := jointRuntimeGCEnvironment(input, true); !reflect.DeepEqual(got, []string{"OWNED=value", "GODEBUG=gctrace=1"}) {
		t.Fatal("enabled tracing did not select exactly one child runtime setting")
	}
	if !reflect.DeepEqual(input, original) {
		t.Fatal("environment preparation mutated the reusable Server configuration")
	}
	t.Setenv("MYPROBE_TEST_LOAD_RUNTIME_GC_TRACE", "")
	if enabled, err := jointRuntimeGCTraceSettings(""); err != nil || enabled {
		t.Fatal("tracing is not disabled by default")
	}
	for _, value := range []string{"0", "true", "gctrace=1"} {
		t.Setenv("MYPROBE_TEST_LOAD_RUNTIME_GC_TRACE", value)
		if _, err := jointRuntimeGCTraceSettings(""); err == nil {
			t.Fatal("ambiguous trace switch was accepted")
		}
	}
	t.Setenv("MYPROBE_TEST_LOAD_RUNTIME_GC_TRACE", "1")
	if _, err := jointRuntimeGCTraceSettings(""); err == nil {
		t.Fatal("tracing accepted without a private process log directory")
	}
}

func TestJointRuntimeGCTracePrivateBoundAndTerminalReceipt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("private capture requires POSIX permissions")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MYPROBE_TEST_LOAD_RUNTIME_GC_TRACE", "1")
	capture, err := openJointProcessLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	chunk := bytes.Repeat([]byte{'x'}, 64<<10)
	capture.bindProcess(12345, "myprobe-joint-load-synthetic")
	for i := 0; i < jointRuntimeGCLogLimit/len(chunk)+1; i++ {
		if n, err := capture.Write(chunk); n != len(chunk) || err != nil {
			t.Fatal("bounded trace capture disrupted the child's pipe")
		}
	}
	if err := capture.close(); err != nil {
		t.Fatal(err)
	}
	if err := capture.close(); err != nil {
		t.Fatal("closing twice overwrote terminal evidence")
	}
	info, err := os.Stat(capture.file.Name())
	if err != nil || info.Size() != jointRuntimeGCLogLimit || info.Mode().Perm() != 0o600 {
		t.Fatal("trace exceeded its fixed private bound")
	}
	raw, err := os.ReadFile(capture.file.Name() + ".closeout.json")
	if err != nil {
		t.Fatal(err)
	}
	var terminal jointProcessCloseout
	if err := json.Unmarshal(raw, &terminal); err != nil || !terminal.Closed || !terminal.RuntimeGCTrace || terminal.LogLimitBytes != jointRuntimeGCLogLimit || terminal.LogBytes != jointRuntimeGCLogLimit || terminal.DiscardedBytes != int64(len(chunk)) || terminal.LogWriteErrors != 0 || terminal.ServerPID != 12345 || terminal.ServerUnit != "myprobe-joint-load-synthetic" {
		t.Fatal("terminal evidence lost the actual truncation or trace mode")
	}
	if bytes.Contains(raw, []byte(dir)) || terminal.LogFile != filepath.Base(capture.file.Name()) {
		t.Fatal("terminal evidence contains an absolute private path")
	}
}

// Only this disposable child forces GC, to verify capture of the Go runtime's
// actual stderr. No application process or acceptance run is forced to collect.
func TestJointRuntimeGCTraceChild(t *testing.T) {
	if os.Getenv("MYPROBE_TEST_RUNTIME_GC_CHILD") != "1" {
		return
	}
	for i := 0; i < 3; i++ {
		runtime.GC()
	}
	// GC returns before its background worker necessarily finishes printing.
	// Stay alive until the parent has observed three complete trace lines.
	if _, err := io.ReadFull(os.Stdin, make([]byte, 1)); err != nil {
		t.Fatal("runtime trace parent did not release the owned child")
	}
}

func TestJointRuntimeGCTraceActualRuntime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("private capture requires POSIX permissions")
	}
	dir := httpFixtureDir(t)
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MYPROBE_TEST_LOAD_RUNTIME_GC_TRACE", "1")
	capture, err := openJointProcessLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestJointRuntimeGCTraceChild$", "-test.count=1")
	command.Env = jointRuntimeGCEnvironment(append(os.Environ(), "MYPROBE_TEST_RUNTIME_GC_CHILD=1"), true)
	command.Stdout, command.Stderr = io.Discard, capture
	input, err := command.StdinPipe()
	if err != nil {
		capture.close()
		t.Fatal(err)
	}
	defer input.Close()
	if err := command.Start(); err != nil {
		capture.close()
		t.Fatal("owned runtime trace child did not start")
	}
	defer func() {
		if command.ProcessState == nil {
			command.Process.Kill()
			command.Wait()
		}
		capture.close()
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	complete := regexp.MustCompile(`(?m)^gc [0-9]+ @[^\n]*\n`)
	for {
		raw, err := os.ReadFile(capture.file.Name())
		if err == nil && len(complete.FindAll(raw, -1)) >= 3 && bytes.HasSuffix(raw, []byte{'\n'}) {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("owned runtime did not finish three GC trace lines")
		case <-ticker.C:
		}
	}
	if _, err := input.Write([]byte{1}); err != nil {
		t.Fatal("owned runtime trace child could not be released")
	}
	if err := command.Wait(); err != nil {
		t.Fatal("owned runtime trace child did not finish")
	}
	capture.bindProcess(command.Process.Pid, "")
	if err := capture.close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(capture.file.Name())
	if err != nil {
		t.Fatal(err)
	}
	if len(regexp.MustCompile(`(?m)^gc [0-9]+ @`).FindAll(raw, -1)) < 3 || !strings.Contains(string(raw), " MB, ") {
		t.Fatal("actual runtime GC events were not captured privately")
	}
	if node, err := exec.LookPath("node"); err == nil {
		review := exec.CommandContext(ctx, node, filepath.Join("../..", "scripts", "review-joint-gc-trace.cjs"), capture.file.Name())
		output, err := review.CombinedOutput()
		if err != nil {
			// The reviewer emits only its fixed rejection classifications here.
			t.Fatalf("GC reviewer rejected owned runtime output: %s", output)
		}
		var summary struct {
			InputVerified        bool `json:"input_verified"`
			DiagnosticOnly       bool `json:"diagnostic_only"`
			CapacityAccepted     bool `json:"capacity_accepted"`
			SteadyMemoryAccepted bool `json:"steady_memory_accepted"`
			Cycles               int  `json:"cycles"`
		}
		if err := json.Unmarshal(output, &summary); err != nil || !summary.InputVerified || !summary.DiagnosticOnly || summary.Cycles < 3 || summary.CapacityAccepted || summary.SteadyMemoryAccepted {
			t.Fatal("actual runtime review lost its diagnostic-only boundary")
		}
	}
}
