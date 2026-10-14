package agentclient

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/collector"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestActualAgentAppliesWelcomeLiveRevisionAndRecordsIngress(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "runtime.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	node, token, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "synthetic runtime node", CollectionSeconds: 2, ReportSeconds: 3})
	if err != nil {
		t.Fatal(err)
	}
	gateway := agentgateway.New(db, agentgateway.NewHub())
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/agent/ws", gateway.WebSocketV2)
	mux.HandleFunc("/api/v1/agent/report", gateway.HTTPReport)
	server := httptest.NewServer(mux)
	defer server.Close()
	client, err := New(Config{ServerURL: server.URL, Token: token, AgentVersion: "runtime-fixture", CollectionPeriod: time.Second, ReportPeriod: time.Second}, collector.New(collector.Config{}), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	agentCtx, stopAgent := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { client.Run(agentCtx); close(done) }()
	stopped := false
	defer func() {
		if !stopped {
			stopAgent()
			<-done
		}
	}()
	waitEvidence := func(revision int64) store.SamplingConfigurationEvidence {
		t.Helper()
		for ctx.Err() == nil {
			evidence, err := db.SamplingConfigurationEvidence(ctx, node.ID)
			if err != nil {
				t.Fatal(err)
			}
			if evidence.Status == "agent_confirmed" && evidence.AppliedRevision != nil && *evidence.AppliedRevision == revision {
				return evidence
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("actual Agent did not confirm configuration")
		return store.SamplingConfigurationEvidence{}
	}
	first := waitEvidence(1)
	if first.CollectionSeconds != 2 || first.ReportSeconds != 3 || client.collectionNS.Load() != int64(2*time.Second) || client.reportNS.Load() != int64(3*time.Second) {
		t.Fatal("welcome was acknowledged without applying both periods")
	}
	// Periods are actually written through the normal management Store path.
	if _, err = db.UpdateNode(ctx, node.ID, store.UpdateNodeParams{Name: node.Name, CollectionSeconds: 1, ReportSeconds: 2, LatencyMode: protocol.TaskKindPing}); err != nil {
		t.Fatal(err)
	}
	second := waitEvidence(2)
	if second.CollectionSeconds != 1 || second.ReportSeconds != 2 || client.collectionNS.Load() != int64(time.Second) || client.reportNS.Load() != int64(2*time.Second) {
		t.Fatal("live revision was acknowledged without application")
	}
	for ctx.Err() == nil {
		ingress, err := db.LastReportIngress(ctx, node.ID)
		if err != nil {
			t.Fatal(err)
		}
		if ingress.Status == "observed" && ingress.Transport == "websocket" && ingress.WireVersion != nil && *ingress.WireVersion == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ctx.Err() != nil {
		t.Fatal("actual sampler report not persisted through v2 socket")
	}
	stopAgent()
	<-done
	stopped = true
	report := protocol.Report{CapturedAt: time.Now().UTC(), CPU: protocol.CPUMetric{UsagePercent: 17}}
	if err = client.sendReport(ctx, report); err != nil {
		t.Fatal("actual HTTP fallback report failed")
	}
	ingress, err := db.LastReportIngress(ctx, node.ID)
	if err != nil || ingress.Transport != "http" || ingress.WireVersion == nil || *ingress.WireVersion != 1 {
		t.Fatal("HTTP fallback inherited old socket transport")
	}
}
