package agentclient

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

type jointBrowserStats struct {
	Mode           string `json:"mode"`
	Pages          int    `json:"pages"`
	Snapshots      int64  `json:"snapshots"`
	Events         int64  `json:"events"`
	PageErrors     int    `json:"pageErrors"`
	ConvergedNodes int    `json:"convergedNodes"`
	ConvergedPages int    `json:"convergedPages"`
}
type jointBrowser struct {
	stopFile, resultFile, expectedFile string
	cmd                                *exec.Cmd
	cancel                             context.CancelFunc
	done                               chan error
}

func startJointBrowser(t *testing.T, ctx context.Context, base, root string, duration time.Duration) *jointBrowser {
	t.Helper()
	script, err := filepath.Abs("../../scripts/joint-load-browser.cjs")
	if err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(root, "browser-ready")
	b := &jointBrowser{stopFile: filepath.Join(root, "browser-stop"), resultFile: filepath.Join(root, "browser-result.json"), expectedFile: filepath.Join(root, "browser-expected.json")}
	config := map[string]any{"base": base, "readyFile": ready, "stopFile": b.stopFile, "resultFile": b.resultFile, "expectedFile": b.expectedFile, "timeoutMs": (duration + 2*time.Minute).Milliseconds(), "playwrightModule": os.Getenv("PLAYWRIGHT_MODULE"), "channel": os.Getenv("MYPROBE_TEST_LOAD_BROWSER_CHANNEL")}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	childCtx, cancel := context.WithCancel(ctx)
	b.cancel = cancel
	b.cmd = exec.CommandContext(childCtx, "node", script, string(raw))
	b.cmd.Stdout, b.cmd.Stderr = os.Stdout, os.Stderr
	if err := b.cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	b.done = make(chan error, 1)
	go func() { b.done <- b.cmd.Wait() }()
	t.Cleanup(func() { b.finish(t) })
	for deadline := time.Now().Add(40 * time.Second); time.Now().Before(deadline); {
		if _, err := os.Stat(ready); err == nil {
			return b
		}
		select {
		case err := <-b.done:
			b.cmd = nil
			cancel()
			t.Fatalf("rendered browser exited before readiness: %v", err)
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Fatal("five rendered browser pages did not become ready")
	return b
}
func (b *jointBrowser) expect(t *testing.T, expected map[string]float64) {
	t.Helper()
	raw, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b.expectedFile, raw, 0600); err != nil {
		t.Fatal(err)
	}
}
func (b *jointBrowser) finish(t *testing.T) jointBrowserStats {
	t.Helper()
	if b.cmd == nil {
		return jointBrowserStats{}
	}
	if err := os.WriteFile(b.stopFile, []byte("stop\n"), 0600); err != nil {
		t.Error(err)
	}
	select {
	case err := <-b.done:
		if err != nil {
			t.Errorf("rendered browsers: %v", err)
		}
	case <-time.After(30 * time.Second):
		b.cancel()
		<-b.done
		t.Error("rendered browser cleanup timed out")
	}
	b.cancel()
	b.cmd = nil
	raw, err := os.ReadFile(b.resultFile)
	if err != nil {
		t.Error("rendered browser result missing")
		return jointBrowserStats{}
	}
	var stats jointBrowserStats
	if err := json.Unmarshal(raw, &stats); err != nil {
		t.Error(err)
	}
	return stats
}
