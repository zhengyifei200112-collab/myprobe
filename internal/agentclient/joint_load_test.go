package agentclient

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/zhengyifei200112-collab/myprobe/internal/alerts"
	"github.com/zhengyifei200112-collab/myprobe/internal/collector"
	"github.com/zhengyifei200112-collab/myprobe/internal/diagnostics"
	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

// This is an opt-in joint runtime fixture, not a microbenchmark. Client.Run,
// both task executors and the separately built cmd/server are production code.
// Only the host metrics source is synthetic. A short run cannot certify a soak.
func TestJointRuntimeLoad(t *testing.T) {
	if os.Getenv("MYPROBE_TEST_JOINT_LOAD") != "1" {
		t.Skip("opt-in disposable real Server, Agents, targets and subscriptions")
	}
	nodes := jointInt(t, "MYPROBE_TEST_LOAD_NODES", 5, 1, 100)
	deniedReceiver := os.Getenv("MYPROBE_TEST_LOAD_DENIED_RECEIVER") == "1"
	if !deniedReceiver {
		self, err := os.Readlink("/proc/self/ns/net")
		initial, initialErr := os.Readlink("/proc/1/ns/net")
		if runtime.GOOS != "linux" || os.Getenv("MYPROBE_TEST_LOAD_NETWORK_NAMESPACE") != "1" || err != nil || initialErr != nil || self == initial {
			t.Fatal("full notification load requires scripts/run-joint-load.sh in an isolated Linux network namespace; denied-receiver mode is a separate limited case")
		}
	}
	duration := 90 * time.Second
	if raw := os.Getenv("MYPROBE_TEST_LOAD_DURATION"); raw != "" {
		var err error
		duration, err = time.ParseDuration(raw)
		if err != nil || duration < 90*time.Second || duration > 48*time.Hour {
			t.Fatal("load duration must be 90s to 48h")
		}
	}
	withBackup := os.Getenv("MYPROBE_TEST_LOAD_BACKUP") == "1"
	if withBackup && (duration < 3*time.Minute || duration > 15*time.Minute || os.Getenv("MYPROBE_TEST_LOAD_HISTORY") != "year" || os.Getenv("MYPROBE_TEST_LOAD_RECONCILE") != "1") {
		t.Fatal("live backup requires 3..15 minutes, dense year history and report reconciliation")
	}
	setupBudget := 5 * time.Minute
	withHistory := false
	if mode := os.Getenv("MYPROBE_TEST_LOAD_HISTORY"); mode != "" {
		if mode != "year" {
			t.Fatal("unsupported joint history mode")
		}
		setupBudget = 2 * time.Hour
		withHistory = true
	}
	backlogHours, err := parseJointBacklogHours(os.Getenv("MYPROBE_TEST_LOAD_BACKLOG_HOURS"))
	if err != nil {
		t.Fatal(err)
	}
	if backlogHours > 0 && !withHistory {
		t.Fatal("history backlog requires the dense year fixture")
	}
	budget := newJointLoadBudget(setupBudget, duration, 3*time.Minute)
	defer budget.stop()
	ctx := budget.Context
	root := jointFixtureRoot(t)
	databasePath := filepath.Join(root, "joint.db")
	binary := os.Getenv("MYPROBE_TEST_LOAD_SERVER_BINARY")
	if binary == "" {
		binary = filepath.Join(root, "joint-server")
		if runtime.GOOS == "windows" {
			binary += ".exe"
		}
		build := exec.CommandContext(ctx, "go", "build", "-o", binary, "../../cmd/server")
		if output, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build Server: %s %v", output, err)
		}
	} else {
		var err error
		binary, err = filepath.Abs(binary)
		if err != nil {
			t.Fatal(err)
		}
	}
	if os.Getenv("MYPROBE_TEST_LOAD_PRIVATE_FIXTURE_DIR") != "" {
		if err := recordJointFixturePlan(root, binary, nodes, backlogHours, withHistory); err != nil {
			t.Fatal("cannot record private synthetic fixture plan")
		}
	}
	address := jointAddress(t)
	base := "http://" + address
	key, password := rand.Text()+rand.Text(), rand.Text()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	var targetStatus atomic.Int32
	targetStatus.Store(503)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(int(targetStatus.Load())) }))
	defer target.Close()
	targetURL := target.URL
	_, targetPort, _ := net.SplitHostPort(strings.TrimPrefix(targetURL, "http://"))
	port, _ := strconv.Atoi(targetPort)
	// HTTP policy deliberately forbids loopback. Reuse the real private-interface
	// fixture and allow only its owned address/port instead of weakening policy.
	t.Setenv("MYPROBE_TEST_PRIVATE_HTTP", "1")
	httpConfig, privateTargetURL := startPrivateTarget(t, false, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(int(targetStatus.Load())) }))
	var allowDelivery atomic.Bool
	var failedNotices, deliveredNotices atomic.Int64
	receiver := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, 1<<20))
		if !allowDelivery.Load() {
			failedNotices.Add(1)
			w.WriteHeader(503)
			return
		}
		deliveredNotices.Add(1)
		w.WriteHeader(204)
	}))
	if !deniedReceiver {
		// RFC 5737 address exists only on this test's private loopback interface.
		// Production validation/dialing is unchanged; no public traffic is sent.
		receiver.Listener.Close()
		listener, err := net.Listen("tcp", "192.0.2.1:0")
		if err != nil {
			t.Fatal("isolated documentation-address receiver unavailable")
		}
		receiver.Listener = listener
	}
	receiver.Start()
	defer receiver.Close()
	db, err := store.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	type nodeFixture struct {
		id, token string
		source    *jointMetrics
		client    *Client
	}
	fixtures := make([]nodeFixture, nodes)
	ids := make([]string, nodes)
	for i := range fixtures {
		node, token, err := db.CreateNode(ctx, store.CreateNodeParams{Name: fmt.Sprintf("Synthetic load %03d", i), CollectionSeconds: 15, ReportSeconds: 15})
		if err != nil {
			t.Fatal(err)
		}
		_, err = db.UpdateNode(ctx, node.ID, store.UpdateNodeParams{Name: node.Name, CollectionSeconds: 15, ReportSeconds: 15, LatencyMode: protocol.TaskKindTCPing})
		if err != nil {
			t.Fatal(err)
		}
		fixtures[i] = nodeFixture{id: node.ID, token: token, source: &jointMetrics{reports: make(map[string][32]byte)}}
		ids[i] = node.ID
	}
	for i := range 3 {
		target, err := db.CreateTarget(ctx, store.CreateTargetParams{Name: fmt.Sprintf("Owned TCP %d", i), Kind: protocol.TaskKindTCPing, Host: "127.0.0.1", Port: &port, IntervalSeconds: 60, TimeoutMS: 1000})
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			if err := db.AssignTarget(ctx, id, target.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	svc := alerts.New(db, key, nil, logger)
	channel, err := svc.CreateChannel(ctx, "Owned receiver", "webhook", alerts.ChannelConfig{URL: receiver.URL})
	if err != nil {
		t.Fatal(err)
	}
	service, err := db.SaveHTTPService(ctx, store.HTTPService{Name: "Owned HTTP target", Enabled: true, IntervalSeconds: 30, NodeIDs: ids, Spec: httpcheck.Spec{URL: privateTargetURL, Method: "GET", StatusCodes: []int{200}, TimeoutMS: 1000, MaxBodyBytes: 1024}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.SaveServiceAlertSettings(ctx, store.ServiceAlertSettings{ServiceID: service.ID, ServiceRevision: service.Revision, Enabled: true, ChannelID: channel.ID, CooldownSeconds: 30}); err != nil {
		t.Fatal(err)
	}
	var history jointHistorySeed
	if withHistory {
		history = seedJointHistory(t, ctx, databasePath, db, ids, backlogHours)
		if os.Getenv("MYPROBE_TEST_LOAD_PRIVATE_FIXTURE_DIR") != "" {
			if err := recordJointHistorySeed(root, history); err != nil {
				t.Fatal("cannot record private history fixture metadata")
			}
		}
		for i := range fixtures {
			fixtures[i].source.counterOffset = history.CounterOffset
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	server := jointServer{binary: binary, database: databasePath, env: []string{"MYPROBE_LISTEN=" + address, "MYPROBE_DATABASE=" + databasePath, "MYPROBE_ADMIN_USERNAME=load-admin", "MYPROBE_ADMIN_PASSWORD=" + password, "MYPROBE_ENCRYPTION_KEY=" + key, "MYPROBE_HTTP_PROBES_ENABLED=true", "MYPROBE_SESSION_HOURS=48", "MYPROBE_NOTIFICATION_ORIGIN=" + base}}
	if withBackup {
		env, err := prepareJointBackup(root)
		if err != nil {
			t.Fatal(err)
		}
		server.env = append(server.env, env)
	}
	server.start(t, ctx, base)
	defer server.stop()
	jar, _ := cookiejar.New(nil)
	httpClient := &http.Client{Timeout: 10 * time.Second, Jar: jar}
	loginBody, _ := json.Marshal(map[string]string{"username": "load-admin", "password": password})
	login, err := httpClient.Post(base+"/api/v1/auth/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		t.Fatal(err)
	}
	var session struct {
		CSRFToken string `json:"csrf_token"`
	}
	loginErr := json.NewDecoder(io.LimitReader(login.Body, 64<<10)).Decode(&session)
	login.Body.Close()
	if login.StatusCode != 200 || loginErr != nil || session.CSRFToken == "" {
		t.Fatalf("fixture login status %d", login.StatusCode)
	}
	var liveBackup *jointBackupController
	if withBackup {
		liveBackup, err = newJointBackupController(ctx, httpClient, base, session.CSRFToken, root, channel.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if history.Mode != "" {
		checkJointHistoryNodes(t, httpClient, base, ids, history.Anchor)
	}
	runCtx, stopLoad := context.WithCancel(ctx)
	var reportObserver *jointReportObserver
	agentBase := base
	if os.Getenv("MYPROBE_TEST_LOAD_RECONCILE") == "1" {
		reportObserver = newJointReportObserver(base)
		defer reportObserver.close()
		agentBase = reportObserver.server.URL
	}
	var workers sync.WaitGroup
	defer func() { stopLoad(); workers.Wait() }()
	for i := range fixtures {
		f := &fixtures[i]
		if reportObserver != nil {
			reportObserver.register(f.token, f.id)
		}
		client, err := New(Config{ServerURL: agentBase, Token: f.token, AgentVersion: "joint-load-synthetic-source", CollectionPeriod: 15 * time.Second, ReportPeriod: 15 * time.Second, HTTPPrivateCIDRs: httpConfig.HTTPPrivateCIDRs, HTTPAdditionalPorts: httpConfig.HTTPAdditionalPorts}, collector.New(collector.Config{}), logger)
		if err != nil {
			t.Fatal(err)
		}
		client.collector, f.client = f.source, client
		workers.Add(1)
		go func() { defer workers.Done(); _ = client.Run(runCtx) }()
	}
	var snapshots, browserEvents atomic.Int64
	var browsers *jointBrowser
	if os.Getenv("MYPROBE_TEST_LOAD_BROWSER") == "1" {
		browsers = startJointBrowser(t, ctx, base, root, duration)
	} else {
		for range 5 {
			workers.Add(1)
			go func() { defer workers.Done(); jointSubscription(runCtx, base, &snapshots, &browserEvents) }()
		}
	}
	result := jointLoadResult{Format: "myprobe.joint-load.v1", StartedAt: time.Now().UTC(), Nodes: nodes, DurationRequestedSeconds: duration.Seconds(), ReportSeconds: 15, TargetsPerNode: 3, TargetSeconds: 60, HTTPServices: 1, SubscriptionClients: 5, OS: runtime.GOOS, GoVersion: runtime.Version(), LogicalCPUs: runtime.NumCPU(), HistoryFixture: "current-run-only", ResourceLimits: server.limits,
		Limitations: []string{"synthetic host metrics; actual Client.Run and TCP/HTTP executors", "five Go WebSocket consumers are not rendered browser sessions", "current-run history does not establish dense 30-day/year query capacity", "collected-minus-persisted includes latest-cache skips and unacknowledged sends; not a packet-loss proof", "one-second resource/WAL sampling can miss instantaneous peaks", "shared host/WSL limits do not establish a dedicated 2-vCPU SSD deployment", "short runs do not establish 24-hour stability"}}
	result.SubscriptionMode = "go-websocket-consumers"
	result.DenseHistory = history
	if history.Mode != "" {
		result.HistoryFixture = history.Mode
		result.Limitations[2] = "SQL-generated dense retained year, with real startup retention and live workload; setup is not a live soak"
	}
	if backlogHours > 0 {
		result.Limitations = append(result.Limitations, fmt.Sprintf("history anchor shifted by %d synthetic backlog hours; data age is not measured runtime", backlogHours))
	}
	if reportObserver != nil {
		result.Limitations[3] = "test-only observer hop outside Server limits reconciles attempted envelopes, Server ACK and stored reports"
	}
	result.SourceSHA256 = make(map[string]string)
	for _, path := range []string{"internal/agentclient/joint_load_process_log_test.go", "internal/agentclient/joint_load_process_readiness_test.go", "internal/agentclient/joint_load_startup_validation_test.go", "internal/agentclient/joint_load_backlog_test.go", "internal/agentclient/joint_load_backlog_validation_test.go"} {
		raw, err := os.ReadFile(filepath.Join("../..", path))
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(raw)
		result.SourceSHA256[path] = hex.EncodeToString(hash[:])
	}
	for _, path := range []string{"cmd/server/main.go", "internal/agentclient/client.go", "internal/agentclient/sampling.go", "internal/agentgateway/gateway.go", "internal/store/store.go", "internal/store/agent_metadata.go", "internal/agentclient/joint_load_test.go", "internal/agentclient/joint_load_budget_test.go", "internal/agentclient/joint_load_budget_validation_test.go", "internal/agentclient/joint_load_backup_test.go", "internal/agentclient/joint_load_history_test.go", "internal/agentclient/joint_load_history_validation_test.go", "internal/agentclient/joint_load_process_test.go", "internal/agentclient/joint_load_resource_test.go", "internal/agentclient/joint_load_resource_validation_test.go", "internal/agentclient/joint_load_browser_test.go", "internal/agentclient/joint_load_reconciliation_test.go", "internal/agentclient/joint_load_reconciliation_validation_test.go", "internal/backup/repository.go", "internal/backup/scheduler.go", "internal/backup/archive.go", "scripts/joint-load-browser.cjs", "scripts/run-joint-load.sh"} {
		raw, err := os.ReadFile(filepath.Join("../..", filepath.FromSlash(path)))
		if err != nil {
			t.Fatal("load fixture source unavailable for provenance")
		}
		hash := sha256.Sum256(raw)
		result.SourceSHA256[path] = hex.EncodeToString(hash[:])
	}
	serverFile, err := os.Open(binary)
	if err != nil {
		t.Fatal(err)
	}
	binaryHash := sha256.New()
	_, hashErr := io.Copy(binaryHash, serverFile)
	serverFile.Close()
	if hashErr != nil {
		t.Fatal(hashErr)
	}
	result.ServerBinarySHA256 = hex.EncodeToString(binaryHash.Sum(nil))
	result.NotificationMode = "real-default-sender-isolated-rfc5737-receiver"
	if deniedReceiver {
		result.NotificationMode = "loopback-policy-denied"
		result.Limitations = append(result.Limitations, "denied-receiver mode verifies refusal only, not provider outage/recovery")
	}
	result.RSSObservation, result.CPUObservation = "unavailable", "unavailable"
	if runtime.GOOS == "linux" {
		result.RSSObservation = "proc-vmrss-sampled"
	}
	if server.cgroup != "" {
		result.CPUObservation = "cgroup-usage-usec"
	}
	if browsers != nil {
		result.SubscriptionMode = "playwright-rendered"
		result.Limitations[1] = "five real rendered pages run outside the Server resource group"
	}
	measurements := make(map[string][]float64)
	var requestErrors int
	recordRequestFailure := func(endpoint, kind string, status int, before time.Time, cause error) {
		requestErrors++
		if len(result.APIFailures) >= 32 {
			return
		}
		class := ""
		if cause != nil {
			class = "request_error"
			var networkError net.Error
			switch {
			case errors.Is(cause, context.DeadlineExceeded):
				class = "deadline_exceeded"
			case errors.Is(cause, context.Canceled):
				class = "context_canceled"
			case errors.As(cause, &networkError) && networkError.Timeout():
				class = "network_timeout"
			case errors.Is(cause, io.EOF), errors.Is(cause, io.ErrUnexpectedEOF):
				class = "unexpected_eof"
			}
		}
		failure := jointAPIFailure{Endpoint: endpoint, Kind: kind, Status: status, ErrorClass: class, ObservedAt: time.Now().UTC(), ElapsedMilliseconds: float64(time.Since(before)) / float64(time.Millisecond)}
		result.APIFailures = append(result.APIFailures, failure)
		// Endpoint names are fixed above; never retain response bodies, URLs,
		// cookies, node identifiers or the transport error's raw message.
		t.Logf("joint API request failure: %+v", failure)
	}
	var cpuMicros uint64
	restarted, disconnected := false, false
	started, err := budget.startMeasurement()
	if err != nil {
		t.Fatal(err)
	}
	result.StartedAt = started.UTC()
	result.PreparationStartedAt = budget.preparationStarted.UTC()
	result.PreparationSeconds = started.Sub(budget.preparationStarted).Seconds()
	result.PreparationBudgetSeconds = setupBudget.Seconds()
	result.MeasurementDeadlineAt = started.Add(duration + 3*time.Minute).UTC()
	resourceTarget := &jointResourceTarget{}
	resourceTarget.set(&server, 0)
	var publishResources func(jointResourceObservation) error
	if output := os.Getenv("MYPROBE_TEST_LOAD_OUTPUT"); output != "" {
		if !filepath.IsAbs(output) {
			t.Fatal("resource output must be absolute")
		}
		publishResources = func(observation jointResourceObservation) error {
			return writeJointResourceProgress(output, nodes, result.SubscriptionMode, started, observation)
		}
	}
	resourceSampler := startJointResourceSampler(ctx, started, time.Second, resourceTarget.read, publishResources)
	defer resourceSampler.stop()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for time.Since(started) < duration {
		select {
		case <-ctx.Done():
			t.Fatal("joint load timed out:", context.Cause(ctx))
		case <-ticker.C:
		}
		elapsed := time.Since(started)
		if liveBackup != nil {
			if err := liveBackup.tick(ctx, elapsed, func(ctx context.Context) error {
				for i := range fixtures {
					report, err := fixtures[i].source.Collect(ctx)
					if err != nil {
						return err
					}
					if err = fixtures[i].client.sendReport(ctx, report); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}
		if failedNotices.Load() > 0 && elapsed > 20*time.Second {
			allowDelivery.Store(true)
		}
		if !disconnected && elapsed > duration/3 && (deniedReceiver || failedNotices.Load() > 0) {
			for i := range fixtures {
				fixtures[i].client.clearConnection(nil)
			}
			disconnected = true
			result.Faults = append(result.Faults, "closed all Agent sockets; normal reconnect/backoff remained active")
		}
		if !restarted && elapsed > duration/2 && (deniedReceiver || deliveredNotices.Load() > 0) && liveBackup.readyForRestart() {
			cpuMicros += server.cpuMicros()
			resourceTarget.set(nil, cpuMicros)
			server.stop()
			server.start(t, ctx, base)
			resourceTarget.set(&server, cpuMicros)
			restarted = true
			if liveBackup != nil {
				liveBackup.restarted = true
			}
			result.Faults = append(result.Faults, "restarted the same real Server/database; normal Agent and subscription reconnection")
		}
		if elapsed > duration*2/3 {
			targetStatus.Store(200)
		}
		endpoints := []struct{ name, path string }{{"overview", "/api/v1/public/nodes"}, {"history_current_30d_range", "/api/v1/public/nodes/" + ids[int(elapsed.Seconds())%nodes] + "/history?range=30d"}, {"health", "/api/v1/admin/system/health"}}
		if history.Mode != "" {
			endpoints[1].name = "history_dense_30d"
			endpoints = append(endpoints, struct{ name, path string }{"history_dense_1y", "/api/v1/public/nodes/" + ids[int(elapsed.Seconds())%nodes] + "/history?range=1y"})
		}
		for _, endpoint := range endpoints {
			before := time.Now()
			response, err := httpClient.Get(base + endpoint.path)
			if err != nil {
				recordRequestFailure(endpoint.name, "transport", 0, before, err)
				continue
			}
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 16<<20))
			response.Body.Close()
			if response.StatusCode != 200 || readErr != nil {
				kind := "http_status"
				if readErr != nil {
					kind = "response_read"
				}
				recordRequestFailure(endpoint.name, kind, response.StatusCode, before, readErr)
				continue
			}
			measurements[endpoint.name] = append(measurements[endpoint.name], float64(time.Since(before))/float64(time.Millisecond))
			if strings.HasPrefix(endpoint.name, "history_dense_") {
				if err := validateJointHistory(body, strings.TrimPrefix(endpoint.name, "history_dense_"), history.Anchor, before); err != nil {
					t.Errorf("dense history during live workload: %v", err)
				}
			}
			if endpoint.name == "health" {
				var current jointHealth
				if err := json.Unmarshal(body, &current); err != nil {
					recordRequestFailure(endpoint.name, "invalid_json", response.StatusCode, before, nil)
				} else {
					result.FinalHealth = current
				}
			}
		}
		if int(elapsed.Seconds())%30 == 0 {
			t.Logf("joint load elapsed=%s nodes=%d snapshots=%d notices=%d/%d", elapsed.Round(time.Second), nodes, snapshots.Load(), failedNotices.Load(), deliveredNotices.Load())
			t.Logf("engine=%s delivery=%s evaluation=%s error=%s connections=%d queue=%s", result.FinalHealth.Engine.Status, result.FinalHealth.Engine.DeliveryConfiguration, result.FinalHealth.Engine.Evaluation.State, result.FinalHealth.Engine.Evaluation.LastErrorCode, result.FinalHealth.Transport.Connections, result.FinalHealth.Queue.Status)
			response, err := httpClient.Get(base + "/api/v1/admin/service-monitors/" + service.ID + "/health")
			if err == nil {
				var health struct {
					Health struct {
						State                     string
						Failure, Unknown, Success int
					}
					Observers []struct{ Reason, ErrorClass string }
				}
				_ = json.NewDecoder(response.Body).Decode(&health)
				response.Body.Close()
				t.Logf("service=%+v observers=%+v", health.Health, health.Observers)
			}
		}
	}
	result.MeasurementSeconds = time.Since(started).Seconds()
	// Keep the actual clients/pages alive during a separate bounded settling
	// phase. A successful send is not evidence that the durable backlog drained.
	drainStarted := time.Now()
	for !deniedReceiver && time.Since(drainStarted) < 60*time.Second {
		response, err := httpClient.Get(base + "/api/v1/admin/system/health")
		if err != nil {
			t.Error("recovery health request failed")
			break
		}
		var current jointHealth
		err = json.NewDecoder(response.Body).Decode(&current)
		response.Body.Close()
		if response.StatusCode != 200 || err != nil {
			t.Error("recovery health unavailable")
			break
		}
		result.FinalHealth = current
		queue := current.Queue
		if queue.Status == "available" && queue.Deliveries != nil && queue.Aggregates != nil && queue.System != nil && queue.Deliveries.Pending+queue.Deliveries.Inflight+queue.Aggregates.Pending+queue.Aggregates.Inflight+queue.System.Pending+queue.System.Inflight == 0 {
			result.RecoveryDrained = true
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("recovery drain timed out")
		case <-time.After(time.Second):
		}
	}
	result.RecoveryDrainSeconds = time.Since(drainStarted).Seconds()
	result.ElapsedSeconds = time.Since(started).Seconds()
	result.EndedAt = time.Now().UTC()
	result.ResourceObservation = resourceSampler.stop()
	if result.ResourceObservation.ProgressWriteErrors != 0 {
		t.Error("resource progress could not be persisted")
	}
	result.APIErrors, result.SampledPeakRSSBytes, result.SampledPeakWALBytes = requestErrors, result.ResourceObservation.PeakRSSBytes, result.ResourceObservation.PeakWALBytes
	result.ServerCPUMicroseconds = cpuMicros + server.cpuMicros()
	result.ResourceSampleSeconds = 1
	result.ResourceTimeline = result.ResourceObservation.Timeline
	if liveBackup != nil {
		minimum, err := minimumRetainedJointSeedReports(history, nodes, liveBackup.evidence.ArchiveCompletedAt, store.DefaultRetentionPolicy().Raw)
		if err != nil {
			t.Error(err)
		}
		evidence, err := liveBackup.finish(reportObserver, nodes, minimum)
		result.LiveBackup = &evidence
		if err != nil {
			t.Error(err)
		}
	}
	result.APILatency = make(map[string]jointLatency)
	for name, values := range measurements {
		result.APILatency[name] = jointDistribution(values)
	}
	result.Snapshots, result.SubscriptionEvents = snapshots.Load(), browserEvents.Load()
	result.NotificationsFailed, result.NotificationsDelivered = failedNotices.Load(), deliveredNotices.Load()
	stopLoad()
	workers.Wait()
	if reportObserver != nil {
		expected := make(map[string]float64)
		for i, f := range fixtures {
			report, _ := f.source.Collect(ctx)
			report.CPU.UsagePercent = float64(i%90+1) + 0.125
			raw, _ := json.Marshal(report)
			f.source.mu.Lock()
			f.source.reports[report.CapturedAt.UTC().Format(time.RFC3339Nano)] = sha256.Sum256(raw)
			f.source.mu.Unlock()
			if err := f.client.sendReport(ctx, report); err != nil {
				t.Fatalf("final real HTTP report failed: %v", err)
			}
			expected[fmt.Sprintf("Synthetic load %03d", i)] = report.CPU.UsagePercent
		}
		if browsers != nil {
			browsers.expect(t, expected)
		}
	}
	if browsers != nil {
		stats := browsers.finish(t)
		result.Snapshots, result.SubscriptionEvents, result.BrowserPageErrors = stats.Snapshots, stats.Events, stats.PageErrors
		result.BrowserConvergedNodes, result.BrowserConvergedPages = stats.ConvergedNodes, stats.ConvergedPages
		if reportObserver != nil && (stats.ConvergedNodes != nodes || stats.ConvergedPages != 5) {
			t.Error("all rendered node states did not converge")
		}
	}
	if reportObserver != nil {
		reportObserver.close()
	}
	server.stop()
	readDB, err := sql.Open("sqlite", "file:"+filepath.ToSlash(databasePath)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer readDB.Close()
	readDB.SetMaxOpenConns(1)
	storedReports := make(map[string]map[string][32]byte)
	duplicateReports := 0
	if err := readDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM delivery_attempts").Scan(&result.DeliveryAttempts); err != nil {
		t.Fatal(err)
	}
	result.HTTPOutcomes = make(map[string]int)
	outcomes, err := readDB.QueryContext(ctx, "SELECT json_extract(result_json,'$.outcome'), COALESCE(json_extract(result_json,'$.error_class'),''), COUNT(*) FROM http_tasks WHERE result_json IS NOT NULL GROUP BY 1,2")
	if err != nil {
		t.Fatal(err)
	}
	for outcomes.Next() {
		var outcome, class string
		var n int
		if err := outcomes.Scan(&outcome, &class, &n); err != nil {
			t.Fatal(err)
		}
		result.HTTPOutcomes[outcome+":"+class] = n
	}
	if err := outcomes.Err(); err != nil {
		t.Fatal(err)
	}
	outcomes.Close()
	for _, f := range fixtures {
		storedReports[f.id] = make(map[string][32]byte)
		rows, err := readDB.QueryContext(ctx, "SELECT captured_at,report_json FROM metric_samples WHERE node_id=? AND id>? ORDER BY id", f.id, history.LastMetricID)
		if err != nil {
			t.Fatal(err)
		}
		seen := make(map[string]bool)
		count := 0
		for rows.Next() {
			var captured, raw string
			if err := rows.Scan(&captured, &raw); err != nil {
				t.Fatal(err)
			}
			f.source.mu.Lock()
			hash, exists := f.source.reports[captured]
			f.source.mu.Unlock()
			// Gateway adds its observed peer IP; compare the exact original source
			// metrics after removing that Server-owned enrichment.
			var report protocol.Report
			if err := json.Unmarshal([]byte(raw), &report); err != nil {
				t.Fatal(err)
			}
			report.PublicIP = ""
			original, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			if !exists || hash != sha256.Sum256(original) || seen[captured] {
				t.Error("stored report unknown, changed or duplicated")
			}
			if seen[captured] {
				duplicateReports++
			}
			storedReports[f.id][captured] = sha256.Sum256(original)
			seen[captured], count = true, count+1
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		if count < 3 {
			t.Errorf("node has only %d persisted reports", count)
		}
		var latencyCount, httpCount int
		if err := readDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM latency_samples WHERE node_id=? AND success=1 AND id>?", f.id, history.LastLatencyID).Scan(&latencyCount); err != nil {
			t.Fatal(err)
		}
		if err := readDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM http_tasks WHERE node_id=? AND result_json IS NOT NULL", f.id).Scan(&httpCount); err != nil {
			t.Fatal(err)
		}
		if latencyCount < 3 || httpCount < 1 {
			t.Errorf("node tasks missing: tcp=%d http=%d", latencyCount, httpCount)
		}
		var confirmationCount int
		if err := readDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM node_configuration_evidence e JOIN nodes n ON n.id=e.node_id WHERE e.node_id=? AND e.applied_revision=n.agent_config_revision AND e.applied_collection_seconds=15 AND e.applied_report_seconds=15 AND e.acknowledged_at IS NOT NULL", f.id).Scan(&confirmationCount); err != nil {
			t.Fatal(err)
		}
		if confirmationCount != 1 {
			t.Error("latest actual Agent configuration was not confirmed")
		}
		result.ConfigurationsConfirmed += confirmationCount
		result.CollectedReports += f.source.calls.Load()
		result.PersistedReports += int64(count)
		result.TCPSuccesses += int64(latencyCount)
		result.HTTPResults += int64(httpCount)
	}
	if reportObserver != nil {
		result.ReportAccounting = reportObserver.reconcile(storedReports, duplicateReports)
		generated := make(map[string]uint64)
		for _, f := range fixtures {
			generated[f.id] = f.client.sequence.Load()
		}
		reportObserver.sequenceAccounting(&result.ReportAccounting, generated)
		a := result.ReportAccounting
		if a.UnexplainedNotStored+a.ACKWithoutStored+a.UnknownStored+a.HashMismatch+a.DuplicateStored+a.UnobservedGeneratedEnvelopes != 0 {
			t.Errorf("wire/ACK/storage reconciliation failed: %+v", a)
		}
	}
	notificationsOK := result.NotificationsFailed > 0 && result.NotificationsDelivered > 0
	if deniedReceiver {
		notificationsOK = result.NotificationsFailed == 0 && result.NotificationsDelivered == 0 && result.DeliveryAttempts > 0
	}
	if !deniedReceiver && !result.RecoveryDrained {
		t.Error("notification backlog did not drain within 60 seconds")
	}
	if result.APIErrors > 0 || result.Snapshots < 5 || result.SubscriptionEvents == 0 || !notificationsOK || !disconnected || !restarted || result.BrowserPageErrors > 0 {
		t.Error("joint runtime did not complete all request, subscription and notification paths")
	}
	if runtime.GOOS == "linux" && result.SampledPeakRSSBytes <= 0 {
		t.Error("Server RSS observation unavailable")
	}
	if server.cgroup != "" && result.ServerCPUMicroseconds == 0 {
		t.Error("limited Server CPU observation unavailable")
	}
	if result.FinalHealth.Engine.Evaluation.FailedRuns > 0 || result.FinalHealth.Retention.Job.FailedRuns > 0 {
		t.Error("real runtime workers reported failures")
	}
	result.Passed = !t.Failed()
	raw, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("MYPROBE_JOINT_LOAD_RESULT=" + string(raw))
	if output := os.Getenv("MYPROBE_TEST_LOAD_OUTPUT"); output != "" {
		if !filepath.IsAbs(output) {
			t.Fatal("load output must be an absolute existing directory")
		}
		if err := os.WriteFile(filepath.Join(output, fmt.Sprintf("joint-load-%d-%s-%s.json", nodes, runtime.GOOS, result.SubscriptionMode)), append(raw, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

type jointMetrics struct {
	calls         atomic.Int64
	mu            sync.Mutex
	reports       map[string][32]byte
	counterOffset uint64
}

func (s *jointMetrics) Hello(_ context.Context, version string, collection, report int) (protocol.Hello, error) {
	return protocol.Hello{AgentVersion: version, Hostname: "synthetic-load-host", OS: "linux", Platform: "synthetic", Architecture: "amd64", CollectionSeconds: collection, ReportSeconds: report, Capabilities: []string{"tcping"}}, nil
}
func (*jointMetrics) UpdateConfig(collector.Config) {}
func (s *jointMetrics) Collect(context.Context) (protocol.Report, error) {
	n := uint64(s.calls.Add(1)) + s.counterOffset
	report := protocol.Report{CapturedAt: time.Now().UTC(), CPU: protocol.CPUMetric{UsagePercent: 17, LogicalCores: 2}, Memory: protocol.MemoryMetric{TotalBytes: 2 << 30, UsedBytes: 300 << 20, UsagePercent: 14.65}, Networks: []protocol.NetworkMetric{{Interface: "synthetic0", RXTotalBytes: n * 1000, TXTotalBytes: n * 2000}}, Uptime: n}
	raw, _ := json.Marshal(report)
	s.mu.Lock()
	s.reports[report.CapturedAt.Format(time.RFC3339Nano)] = sha256.Sum256(raw)
	s.mu.Unlock()
	return report, nil
}

func jointSubscription(ctx context.Context, base string, snapshots, events *atomic.Int64) {
	for ctx.Err() == nil {
		conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+"/api/v1/public/ws", nil)
		if err == nil {
			conn.SetReadLimit(16 << 20)
			for ctx.Err() == nil {
				var message struct {
					Type string `json:"type"`
				}
				if wsjson.Read(ctx, conn, &message) != nil {
					break
				}
				if message.Type == "snapshot" {
					snapshots.Add(1)
				} else {
					events.Add(1)
				}
			}
			conn.CloseNow()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}

type jointLatency struct {
	Count         int `json:"count"`
	P50, P95, Max float64
}

func jointDistribution(values []float64) jointLatency {
	if len(values) == 0 {
		return jointLatency{}
	}
	sort.Float64s(values)
	return jointLatency{len(values), values[(len(values)-1)/2], values[(95*len(values)+99)/100-1], values[len(values)-1]}
}

type jointLoadResult struct {
	PreparationStartedAt, MeasurementDeadlineAt                                                                                               time.Time
	PreparationSeconds, PreparationBudgetSeconds                                                                                              float64
	LiveBackup                                                                                                                                *jointBackupEvidence `json:",omitempty"`
	ResourceObservation                                                                                                                       jointResourceObservation
	Format                                                                                                                                    string `json:"format"`
	StartedAt, EndedAt                                                                                                                        time.Time
	Nodes, ReportSeconds, TargetsPerNode, TargetSeconds, HTTPServices, SubscriptionClients                                                    int
	DurationRequestedSeconds, ElapsedSeconds                                                                                                  float64
	MeasurementSeconds, RecoveryDrainSeconds                                                                                                  float64
	RecoveryDrained                                                                                                                           bool
	OS, GoVersion                                                                                                                             string
	LogicalCPUs                                                                                                                               int
	HistoryFixture                                                                                                                            string
	DenseHistory                                                                                                                              jointHistorySeed
	SourceSHA256                                                                                                                              map[string]string
	ServerBinarySHA256                                                                                                                        string
	ResourceTimeline                                                                                                                          []jointResourcePoint
	SubscriptionMode, RSSObservation, CPUObservation                                                                                          string
	BrowserPageErrors                                                                                                                         int
	BrowserConvergedNodes, BrowserConvergedPages                                                                                              int
	NotificationMode                                                                                                                          string
	DeliveryAttempts                                                                                                                          int
	ConfigurationsConfirmed                                                                                                                   int
	FinalHealth                                                                                                                               jointHealth
	HTTPOutcomes                                                                                                                              map[string]int
	ReportAccounting                                                                                                                          jointReportAccounting
	ResourceLimits                                                                                                                            map[string]string
	ResourceSampleSeconds                                                                                                                     int
	SampledPeakRSSBytes, SampledPeakWALBytes                                                                                                  int64
	ServerCPUMicroseconds                                                                                                                     uint64
	APILatency                                                                                                                                map[string]jointLatency
	APIErrors                                                                                                                                 int
	APIFailures                                                                                                                               []jointAPIFailure
	CollectedReports, PersistedReports, TCPSuccesses, HTTPResults, Snapshots, SubscriptionEvents, NotificationsFailed, NotificationsDelivered int64
	Faults, Limitations                                                                                                                       []string
	Passed                                                                                                                                    bool
}

type jointAPIFailure struct {
	Endpoint, Kind, ErrorClass string
	Status                     int
	ObservedAt                 time.Time
	ElapsedMilliseconds        float64
}

type jointResourcePoint struct {
	ElapsedSeconds      float64
	RSSBytes            int64
	CPUMicroseconds     uint64
	SampledPeakWALBytes int64
}

type jointHealth struct {
	Engine    alerts.EngineDiagnostics           `json:"notification_engine"`
	Queue     store.NotificationQueueDiagnostics `json:"notification_queue"`
	Transport struct {
		Connections        int  `json:"agent_connections"`
		PendingResults     int  `json:"pending_results"`
		HTTPPendingResults *int `json:"http_pending_results"`
		HTTPExpiredResults *int `json:"http_expired_results"`
	} `json:"transport"`
	BrowserSubscriptions int `json:"browser_subscriptions"`
	Retention            struct {
		Job     diagnostics.JobSnapshot `json:"job"`
		HTTPJob diagnostics.JobSnapshot `json:"http_catchup_job"`
	} `json:"retention"`
}

func jointInt(t *testing.T, name string, fallback, lower, upper int) int {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < lower || value > upper {
		t.Fatalf("%s must be %d to %d", name, lower, upper)
	}
	return value
}
func jointAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	return address
}
