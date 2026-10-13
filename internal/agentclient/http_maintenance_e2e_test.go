package agentclient

import (
	"context"
	"crypto/rand"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/auth"
	"github.com/zhengyifei200112-collab/myprobe/internal/collector"
	"github.com/zhengyifei200112-collab/myprobe/internal/config"
	"github.com/zhengyifei200112-collab/myprobe/internal/httpapi"
	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
	"github.com/zhengyifei200112-collab/myprobe/internal/scheduler"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestHTTPMaintenanceStatisticsThroughRealAgent(t *testing.T) {
	agentConfig, targetURL := startPrivateHTTPTarget(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "maintenance-real-clock.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	password := rand.Text()
	authentication := auth.New(db, time.Hour)
	if _, err := authentication.Bootstrap(ctx, "fixture-admin", password); err != nil {
		t.Fatal(err)
	}
	node, token, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "Maintenance observer"})
	if err != nil {
		t.Fatal(err)
	}
	hub := agentgateway.NewHub()
	gateway := agentgateway.New(db, hub)
	gateway.EnableHTTPProbes(true)
	server := httptest.NewServer(httpapi.New(config.Config{SessionTTL: time.Hour, EncryptionKey: password, HTTPProbesEnabled: true}, db, authentication, gateway, hub).Handler())
	defer server.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	agentConfig.ServerURL, agentConfig.Token = server.URL, token
	agentConfig.CollectionPeriod, agentConfig.ReportPeriod = time.Second, time.Second
	client, err := New(agentConfig, collector.New(collector.Config{}), logger)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); _ = client.Run(ctx) }()
	defer func() {
		cancel()
		client.clearConnection(nil)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("maintenance fixture Agent did not stop")
		}
	}()
	for {
		nodes, err := db.ListNodes(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(nodes) == 1 && nodes[0].LastSeenAt != nil && gateway.SupportsHTTP(node.ID) {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("fixture Agent not ready")
		case <-time.After(10 * time.Millisecond):
		}
	}
	recorded := time.Now().UTC()
	create := func(duration time.Duration) store.MaintenanceWindow {
		t.Helper()
		w, err := db.CreateMaintenanceWindow(ctx, store.MaintenanceWindow{Reason: "fixture private reason", Timezone: "Asia/Shanghai", CreatedBy: "fixture-admin", NodeIDs: []string{node.ID}, StartsAt: recorded.Add(-time.Second), EndsAt: recorded.Add(duration)})
		if err != nil {
			t.Fatal(err)
		}
		return w
	}
	first, second := create(time.Minute), create(3*time.Minute)
	service, err := db.SaveHTTPService(ctx, store.HTTPService{Name: "Maintenance statistics website", Enabled: true, IntervalSeconds: 30, NodeIDs: []string{node.ID}, Spec: httpcheck.Spec{URL: targetURL, Method: "GET", StatusCodes: []int{200}, TimeoutMS: 1000, MaxBodyBytes: 1024}})
	if err != nil {
		t.Fatal(err)
	}
	schedulerDone := make(chan struct{})
	go func() { defer close(schedulerDone); scheduler.NewHTTP(db, gateway, logger).Run(ctx) }()
	defer func() {
		cancel()
		select {
		case <-schedulerDone:
		case <-time.After(10 * time.Second):
			t.Error("maintenance scheduler did not stop")
		}
	}()
	for {
		items, err := db.RecentHTTPResults(ctx, service.ID, 25)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) > 0 && items[0].Result != nil && items[0].Result.Outcome == "success" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("actual check did not succeed")
		case <-time.After(10 * time.Millisecond):
		}
	}
	// Cancel one overlapping window after real execution. The other still covers
	// its planned slot, and historical results continue to exist unchanged.
	if err := db.CancelMaintenanceWindow(ctx, first.ID, first.Revision, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var mature store.HTTPStatistics
	for {
		now := time.Now().UTC()
		mature, err = db.HTTPServiceStatistics(ctx, service.ID, node.ID, service.CreatedAt, now, now, true)
		if err != nil {
			t.Fatal(err)
		}
		if mature.Raw.Expected > 0 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("real clock did not reach HTTP maturity")
		case <-time.After(100 * time.Millisecond):
		}
	}
	if mature.Raw.Success != mature.Raw.Expected || mature.Raw.Missing != 0 || mature.MaintenanceExcludedCounts.Expected != mature.Raw.Expected || mature.Expected != 0 || mature.Coverage != nil || mature.SuccessRate != nil {
		t.Fatal("actual overlapping-window statistics do not match successful raw checks")
	}
	t.Log("Actual Agent result reached 126-second maturity; raw success retained, overlap excluded once, canceling one window preserved the other.")
	if os.Getenv("MYPROBE_TEST_HTTP_BROWSER") == "1" {
		cmd := exec.CommandContext(ctx, "node", "../../scripts/http-maintenance-live-ui.cjs")
		cmd.Env = append(os.Environ(), "UI_BASE_URL="+server.URL, "HTTP_MAINTENANCE_PASSWORD="+password)
		output, err := cmd.CombinedOutput()
		t.Log(string(output))
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := db.CancelMaintenanceWindow(ctx, second.ID, second.Revision, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	// Freeze the effective range; advancing maturity must not add later slots to
	// a comparison that is only checking cancellation's historical effect.
	after, err := db.HTTPServiceStatistics(ctx, service.ID, node.ID, mature.Start, mature.End, time.Now().UTC(), true)
	if err != nil || after.Raw.Expected != mature.Raw.Expected || after.MaintenanceExcludedCounts.Expected != mature.MaintenanceExcludedCounts.Expected {
		t.Fatal("later cancellation erased earlier maintenance history")
	}
}
