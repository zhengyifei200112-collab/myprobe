package agentclient

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/alerts"
	"github.com/zhengyifei200112-collab/myprobe/internal/collector"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

// One real main process, default evaluator/sender and Client.Run across a real
// restart. Only CPU samples and the isolated receiver's response are controlled.
func TestResourceIncidentThroughMainRestart(t *testing.T) {
	if os.Getenv("MYPROBE_TEST_RESOURCE_MAIN") != "1" {
		t.Skip("opt-in native Server/Agent and isolated notification receiver")
	}
	self, selfErr := os.Readlink("/proc/self/ns/net")
	initial, initialErr := os.Readlink("/proc/1/ns/net")
	if runtime.GOOS != "linux" || selfErr != nil || initialErr != nil || self == initial {
		t.Fatal("resource main acceptance requires a fresh Linux network namespace")
	}
	interfaces, err := net.Interfaces()
	if err != nil || len(interfaces) != 1 || interfaces[0].Name != "lo" {
		t.Fatal("resource main acceptance permits only the isolated loopback interface")
	}
	binary := os.Getenv("MYPROBE_TEST_RESOURCE_SERVER")
	if !filepath.IsAbs(binary) {
		t.Fatal("an absolute frozen Server executable is required")
	}
	initialBinary, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal("cannot bind initial Server executable")
	}
	initialSHA := sha256.Sum256(initialBinary)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	started := time.Now()
	root := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	var allowReply atomic.Bool
	var receiptMu sync.Mutex
	type receipt struct {
		state, incident, key string
		confirmed            bool
	}
	var receipts []receipt
	var timedOutReceipts atomic.Int64
	var timeoutMilliseconds atomic.Int64
	var expectedNode atomic.Value
	receiver := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var n store.DeliveryNotification
		body, bodyErr := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		if bodyErr != nil || json.Unmarshal(body, &n) != nil || n.Kind != "cpu" || n.NodeID != expectedNode.Load() || n.IncidentID == "" || r.Header.Get("Idempotency-Key") == "" {
			t.Error("unexpected isolated resource notification identity")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		confirmed := allowReply.Load()
		receiptMu.Lock()
		receipts = append(receipts, receipt{n.State, n.IncidentID, r.Header.Get("Idempotency-Key"), confirmed})
		receiptMu.Unlock()
		if confirmed {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		// Read the actual request, then withhold the response until the default
		// sender's timeout closes it. No dial substitution or shortened deadline.
		began := time.Now()
		select {
		case <-r.Context().Done():
			if ctx.Err() == nil {
				timeoutMilliseconds.Store(time.Since(began).Milliseconds())
				timedOutReceipts.Add(1)
			}
		case <-ctx.Done():
		}
	}))
	receiver.Listener.Close()
	listener, err := net.Listen("tcp", "192.0.2.1:0")
	if err != nil {
		t.Fatal("owned documentation-address receiver unavailable")
	}
	receiver.Listener = listener
	receiver.Start()
	defer func() { cancel(); receiver.Close() }()

	databasePath := filepath.Join(root, "resource.db")
	db, err := store.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	node, token, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "Synthetic CPU main acceptance", CollectionSeconds: 1, ReportSeconds: 1})
	if err != nil {
		t.Fatal(err)
	}
	expectedNode.Store(node.ID)
	key, password := rand.Text()+rand.Text(), rand.Text()
	seed := alerts.New(db, key, nil, logger)
	channel, err := seed.CreateChannel(ctx, "Isolated main receiver", "webhook", alerts.ChannelConfig{URL: receiver.URL})
	if err != nil {
		t.Fatal(err)
	}
	recoverySeconds := 30
	if _, err := seed.CreateRule(ctx, node.ID, channel.ID, "cpu", alerts.RuleConfig{ThresholdPercent: 80, DurationSeconds: 3, RecoverySeconds: &recoverySeconds, RepeatSeconds: 3600}, 300); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := reservation.Addr().String()
	reservation.Close()
	base := "http://" + address
	server := jointServer{binary: binary, database: databasePath, env: []string{
		"MYPROBE_LISTEN=" + address, "MYPROBE_DATABASE=" + databasePath,
		"MYPROBE_ADMIN_USERNAME=resource-admin", "MYPROBE_ADMIN_PASSWORD=" + password,
		"MYPROBE_ENCRYPTION_KEY=" + key, "MYPROBE_NOTIFICATION_ORIGIN=" + base,
		"MYPROBE_HTTP_PROBES_ENABLED=false", "MYPROBE_RELEASE_CHECKS_AUTO=false",
	}}
	server.start(t, ctx, base)
	defer server.stop()
	firstPID := server.pid
	jar, _ := cookiejar.New(nil)
	httpClient := &http.Client{Jar: jar, Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
	defer httpClient.CloseIdleConnections()
	loginBody, _ := json.Marshal(map[string]string{"username": "resource-admin", "password": password})
	login, err := httpClient.Post(base+"/api/v1/auth/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		t.Fatal("resource fixture authentication transport failed")
	}
	login.Body.Close()
	if login.StatusCode != http.StatusOK {
		t.Fatal("resource fixture authentication rejected")
	}
	read := func(suffix string, destination any) {
		t.Helper()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+suffix, nil)
		if err != nil {
			t.Fatal("invalid fixture request")
		}
		response, err := httpClient.Do(request)
		if err != nil {
			t.Fatal("resource fixture read transport failed")
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(destination) != nil {
			t.Fatalf("resource fixture read failed with status %d", response.StatusCode)
		}
	}
	var health struct{ Server struct{ Version string } }
	read("/api/v1/admin/system/health", &health)
	if health.Server.Version != "resource-main-acceptance" {
		t.Fatal("unexpected main executable version; build with the documented fixture ldflag")
	}
	type phase struct {
		Name           string
		ElapsedSeconds float64
	}
	var phases []phase
	await := func(name string, condition func() bool) {
		t.Helper()
		for {
			if condition() {
				phases = append(phases, phase{name, time.Since(started).Seconds()})
				t.Logf("resource main phase=%s elapsed=%s", name, time.Since(started).Round(time.Millisecond))
				return
			}
			select {
			case <-ctx.Done():
				t.Fatalf("resource main deadline in phase %s", name)
			case <-time.After(200 * time.Millisecond):
			}
		}
	}
	incidents := func() []store.Incident {
		var page struct{ Incidents []store.Incident }
		read("/api/v1/admin/incidents?node_id="+node.ID, &page)
		if len(page.Incidents) > 1 {
			t.Fatal("resource dip or restart manufactured an extra incident")
		}
		return page.Incidents
	}
	var episode store.Incident
	var incidentID string
	observeIncident := func() bool {
		items := incidents()
		if len(items) == 0 {
			return false
		}
		episode = items[0]
		if episode.Kind != "cpu" || (incidentID != "" && episode.ID != incidentID) {
			t.Fatal("resource episode identity changed")
		}
		return true
	}
	deliveries := func() []store.NotificationDelivery {
		var page struct{ Deliveries []store.NotificationDelivery }
		read("/api/v1/admin/incidents/"+incidentID+"/deliveries", &page)
		return page.Deliveries
	}
	attempts := func(id string) []store.DeliveryAttempt {
		var page store.DeliveryAttemptPage
		read("/api/v1/admin/notification-deliveries/"+id+"/attempts", &page)
		return page.Attempts
	}
	source := &resourceMainMetrics{}
	source.cpu.Store(95)
	client, err := New(Config{ServerURL: base, Token: token, AgentVersion: "resource-main-synthetic-source", CollectionPeriod: time.Second, ReportPeriod: time.Second}, collector.New(collector.Config{}), logger)
	if err != nil {
		t.Fatal(err)
	}
	client.collector = source
	agentCtx, stopAgent := context.WithCancel(ctx)
	agentDone := make(chan struct{})
	go func() { defer close(agentDone); _ = client.Run(agentCtx) }()
	defer func() {
		stopAgent()
		client.clearConnection(nil)
		select {
		case <-agentDone:
		case <-time.After(10 * time.Second):
			t.Error("resource fixture Agent did not stop")
		}
	}()
	observeCapture := func(cpu float64, since time.Time) {
		await("captured-resource-phase", func() bool {
			var detail struct{ Node store.PublicNode }
			read("/api/v1/admin/nodes/"+node.ID, &detail)
			return detail.Node.Report != nil && !detail.Node.Report.CapturedAt.Before(since) && detail.Node.Report.CPU.UsagePercent == cpu && detail.Node.Online && !detail.Node.Stale
		})
	}
	observeCapture(95, started)
	await("pending", func() bool { return observeIncident() && episode.State == "pending" })
	incidentID = episode.ID
	await("firing", func() bool { return observeIncident() && episode.State == "firing" && episode.FiredAt != nil })
	if episode.FiredAt.Sub(episode.StartedAt) < 3*time.Second {
		t.Fatal("resource trigger duration was bypassed")
	}
	var firing store.NotificationDelivery
	await("real-timeout-persisted", func() bool {
		jobs := deliveries()
		if len(jobs) != 1 || jobs[0].NotificationType != "firing" || jobs[0].Status != "pending" {
			return false
		}
		firing = jobs[0]
		for _, attempt := range attempts(firing.ID) {
			if attempt.Outcome == "unknown" && attempt.ErrorClass == "transport_error" && timedOutReceipts.Load() > 0 {
				return true
			}
		}
		return false
	})
	if timeoutMilliseconds.Load() < 9000 {
		t.Fatal("notification response was not held through the default sender timeout")
	}
	changed := time.Now().UTC()
	source.cpu.Store(10)
	observeCapture(10, changed)
	await("brief-recovery-observed", func() bool { return observeIncident() && episode.State == "firing" && episode.RecoverySince != nil })
	briefSince := *episode.RecoverySince
	changed = time.Now().UTC()
	source.cpu.Store(95)
	observeCapture(95, changed)
	if time.Since(briefSince) >= 30*time.Second {
		t.Fatal("fixture did not return high CPU before the recovery condition elapsed")
	}
	await("recovery-reset-without-resolving", func() bool {
		return observeIncident() && episode.State == "firing" && episode.RecoverySince == nil && episode.ObservedAt != nil && !episode.ObservedAt.Before(changed)
	})
	beforeRestart := deliveries()
	if len(beforeRestart) != 1 || beforeRestart[0].ID != firing.ID || beforeRestart[0].Status != "pending" || beforeRestart[0].AttemptCount != 1 || !beforeRestart[0].AvailableAt.After(time.Now()) {
		t.Fatal("the original timed-out intent must still be pending before restart")
	}
	server.stop()
	allowReply.Store(true)
	server.start(t, ctx, base)
	if server.pid == firstPID || server.pid <= 0 {
		t.Fatal("fixture did not restart the actual Server process")
	}
	await("same-episode-after-main-restart", func() bool { return observeIncident() && episode.State == "firing" })
	await("pending-delivery-survived-and-confirmed", func() bool {
		jobs := deliveries()
		return len(jobs) == 1 && jobs[0].ID == firing.ID && jobs[0].Status == "delivered" && jobs[0].AttemptCount == 2
	})
	changed = time.Now().UTC()
	source.cpu.Store(10)
	observeCapture(10, changed)
	await("sustained-recovery-observed", func() bool { return observeIncident() && episode.State == "firing" && episode.RecoverySince != nil })
	finalRecoverySince := *episode.RecoverySince
	await("same-episode-resolved", func() bool {
		return observeIncident() && episode.State == "resolved" && episode.ResolutionReason == "recovered"
	})
	if episode.ResolvedAt == nil || episode.ResolvedAt.Sub(finalRecoverySince) < 30*time.Second {
		t.Fatal("resource recovery duration was bypassed")
	}
	await("confirmed-recovery-delivery", func() bool {
		jobs := deliveries()
		return len(jobs) == 2 && jobs[0].NotificationType == "resolved" && jobs[0].Status == "delivered" && jobs[0].AttemptCount == 1 && jobs[1].ID == firing.ID && jobs[1].Status == "delivered"
	})
	receiptMu.Lock()
	seen := append([]receipt(nil), receipts...)
	receiptMu.Unlock()
	if len(seen) != 3 || seen[0].confirmed || !seen[1].confirmed || !seen[2].confirmed || seen[0].state != "firing" || seen[1].state != "firing" || seen[2].state != "resolved" || seen[0].key != seen[1].key || seen[1].key == seen[2].key {
		t.Fatal("receiver receipts do not reconcile with unknown/retried firing and one recovery")
	}
	for _, item := range seen {
		if item.incident != incidentID {
			t.Fatal("receiver receipt belongs to another episode")
		}
	}
	finalJobs := deliveries()
	firingAttempts, recoveryAttempts := attempts(firing.ID), attempts(finalJobs[0].ID)
	if len(firingAttempts) != 2 || firingAttempts[0].Outcome != "delivered" || firingAttempts[1].Outcome != "unknown" || len(recoveryAttempts) != 1 || recoveryAttempts[0].Outcome != "delivered" {
		t.Fatal("durable attempt outcomes disagree with actual receiver receipts")
	}
	rawBinary, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal("cannot bind tested Server executable")
	}
	binarySHA := sha256.Sum256(rawBinary)
	if binarySHA != initialSHA {
		t.Fatal("Server executable changed during resource acceptance")
	}
	evidence := map[string]any{"format": "myprobe.resource-main-acceptance.v1", "passed": !t.Failed(), "server_sha256": hex.EncodeToString(binarySHA[:]), "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "elapsed_seconds": time.Since(started).Seconds(), "phases": phases, "incident_count": 1, "delivery_count": len(finalJobs), "firing_attempts": len(firingAttempts), "recovery_attempts": len(recoveryAttempts), "receiver_requests": len(seen), "receiver_timeout_ms": timeoutMilliseconds.Load(), "same_incident_after_restart": true, "same_pending_delivery_after_restart": true, "original_login_session_survived": true, "stable_firing_idempotency_key": true, "scope": "real main process restart, Client.Run, default evaluator/sender and SQLite; synthetic CPU and isolated documentation-address receiver only; no browser, production data, Provider/OAuth, capacity or 24-hour claim"}
	raw, _ := json.Marshal(evidence)
	t.Log("MYPROBE_RESOURCE_MAIN_RESULT=" + string(raw))
	if output := os.Getenv("MYPROBE_TEST_RESOURCE_OUTPUT"); output != "" {
		if !filepath.IsAbs(output) {
			t.Fatal("resource evidence output must be absolute")
		}
		file, err := os.OpenFile(filepath.Join(output, "resource-main-result.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			t.Fatal("cannot exclusively create resource evidence")
		}
		_, writeErr := file.Write(append(raw, '\n'))
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			t.Fatal("cannot persist resource evidence")
		}
	}
}

type resourceMainMetrics struct {
	cpu   atomic.Int64
	calls atomic.Uint64
}

func (*resourceMainMetrics) Hello(_ context.Context, version string, collection, report int) (protocol.Hello, error) {
	return protocol.Hello{AgentVersion: version, Hostname: "synthetic-resource-main", OS: "linux", Platform: "synthetic", Architecture: runtime.GOARCH, CollectionSeconds: collection, ReportSeconds: report}, nil
}
func (*resourceMainMetrics) UpdateConfig(collector.Config) {}
func (s *resourceMainMetrics) Collect(context.Context) (protocol.Report, error) {
	n := s.calls.Add(1)
	return protocol.Report{CapturedAt: time.Now().UTC(), CPU: protocol.CPUMetric{UsagePercent: float64(s.cpu.Load()), LogicalCores: 2}, Memory: protocol.MemoryMetric{TotalBytes: 2 << 30, UsedBytes: 300 << 20}, Networks: []protocol.NetworkMetric{{Interface: "synthetic0", RXTotalBytes: n * 1000, TXTotalBytes: n * 2000}}, Uptime: n}, nil
}
