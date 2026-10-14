package agentclient

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

// Uses the published artifact, not a reconstruction with today's dependencies.
// CI pins the release SHA and supplies its path; normal offline tests skip it.
func TestReleased021AgentRuntimeCompatibility(t *testing.T) {
	binary := os.Getenv("MYPROBE_TEST_RELEASED_AGENT")
	if binary == "" {
		t.Skip("MYPROBE_TEST_RELEASED_AGENT is not configured")
	}
	expected := map[string]string{
		"windows": "76c8a7a0805ea151db344e03fa1d81080f6f415a1ad603b9350a5b45716394bf",
		"linux":   "006fc5b316d3168ed22b1578bdc37012c1b13b82bab740116824ffc71b43b8dc",
	}[runtime.GOOS]
	if expected == "" || runtime.GOARCH != "amd64" {
		t.Fatal("this acceptance fixture requires the pinned amd64 Linux or Windows release")
	}
	f, err := os.Open(binary)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	_, err = io.Copy(hash, f)
	f.Close()
	if err != nil || fmt.Sprintf("%x", hash.Sum(nil)) != expected {
		t.Fatal("released Agent checksum mismatch")
	}
	for _, transport := range []string{"websocket", "http"} {
		t.Run(transport, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			db, err := store.Open(ctx, filepath.Join(t.TempDir(), "released-agent.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			node, token, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "synthetic released Agent", CollectionSeconds: 2, ReportSeconds: 3})
			if err != nil {
				t.Fatal(err)
			}
			gateway := agentgateway.New(db, agentgateway.NewHub())
			mux := http.NewServeMux()
			mux.HandleFunc("/api/v1/agent/report", gateway.HTTPReport)
			mux.HandleFunc("/api/v1/agent/hello", gateway.HTTPHello)
			if transport == "websocket" {
				mux.HandleFunc("/api/v1/agent/ws", gateway.WebSocket)
			}
			server := httptest.NewServer(mux)
			defer server.Close()
			process := exec.CommandContext(ctx, binary, "--server", server.URL, "--token", token, "--collection-interval", "1s", "--report-interval", "1s")
			process.Stdout, process.Stderr = io.Discard, io.Discard
			if err := process.Start(); err != nil {
				t.Fatal("released Agent failed to start")
			}
			done := make(chan error, 1)
			go func() { done <- process.Wait() }()
			defer func() { _ = process.Process.Kill(); <-done }()
			for ctx.Err() == nil {
				ingress, err := db.LastReportIngress(ctx, node.ID)
				if err != nil {
					t.Fatal(err)
				}
				if ingress.Status == "observed" {
					if ingress.Transport != transport || ingress.WireVersion == nil || *ingress.WireVersion != 1 {
						t.Fatal("released Agent report was assigned an inferred transport/version")
					}
					evidence, err := db.SamplingConfigurationEvidence(ctx, node.ID)
					want := "acknowledgement_unsupported"
					if transport == "http" {
						want = "no_connection_evidence"
					}
					if err != nil || evidence.Status != want || evidence.AppliedRevision != nil {
						t.Fatal("released Agent welcome/hello was claimed applied")
					}
					t.Logf("pinned v0.2.1 binary accepted via %s/v1; configuration status=%s", transport, evidence.Status)
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatal("released Agent did not persist a report")
		})
	}
}
