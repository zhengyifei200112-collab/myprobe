package agentclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

func TestHTTPFallbackConfigurationDoesNotDeadlockSampling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var envelope protocol.Envelope
		if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
			t.Error(err)
			return
		}
		_ = json.NewEncoder(w).Encode(protocol.Acknowledgement{Sequence: envelope.Sequence, Config: &protocol.Config{CollectionSeconds: 2, ReportSeconds: 3}})
	}))
	defer server.Close()
	client := configClient(t, server.URL, &fakeMetrics{})
	client.reportNS.Store(int64(5 * time.Millisecond))
	client.sample = &collectedSample{id: 1, report: protocol.Report{CapturedAt: time.Now().UTC()}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); client.reportLoop(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("report worker remained locked after cancellation")
		}
	}()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		if client.collectionNS.Load() == int64(2*time.Second) && client.reportNS.Load() == int64(3*time.Second) {
			if _, ok := client.currentSample(); ok {
				t.Fatal("new configuration kept an old-generation sample")
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("HTTP acknowledgement configuration was lost or deadlocked")
}
