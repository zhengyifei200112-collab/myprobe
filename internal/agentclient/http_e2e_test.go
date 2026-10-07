package agentclient

import (
	"context"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/collector"
	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
	"github.com/zhengyifei200112-collab/myprobe/internal/scheduler"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestHTTPPeriodicCheckThroughRealAgentAndGateway(t *testing.T) {
	runHTTPPeriodicCheck(t, Config{}, httpcheck.Spec{URL: "http://127.0.0.1/", Method: "GET", StatusCodes: []int{200}, TimeoutMS: 1000, MaxBodyBytes: 1024}, "unobserved", "policy_denied")
}

// Opt-in because this opens a temporary listener on a private interface. It uses
// the unmodified production executor and address policy, with no dial injection.
func TestHTTPPeriodicCheckWithPrivateTarget(t *testing.T) {
	config, targetURL := startPrivateHTTPTarget(t)
	for _, tc := range []struct{ name, path, method, expected, outcome, class string }{
		{"success", "/", "GET", "healthy", "success", ""},
		{"head", "/", "HEAD", "", "success", ""},
		{"status", "/failure", "GET", "", "failure", "status_mismatch"},
		{"content", "/", "GET", "absent", "failure", "content_mismatch"},
		{"redirect", "/redirect", "GET", "", "failure", "redirect_limit"},
		{"oversized", "/oversized", "GET", "", "failure", "response_too_large"},
		{"timeout", "/slow", "GET", "", "failure", "timeout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := httpcheck.Spec{URL: targetURL + tc.path, Method: tc.method, StatusCodes: []int{200}, TimeoutMS: 2000, MaxBodyBytes: 1024}
			if tc.expected != "" {
				spec.Assertion = &httpcheck.Assertion{Kind: "text_contains", Text: tc.expected}
			}
			runHTTPPeriodicCheck(t, config, spec, tc.outcome, tc.class)
		})
	}
}

func TestHTTPSPeriodicCheckRejectsUntrustedCertificate(t *testing.T) {
	config, targetURL := startPrivateTarget(t, true)
	runHTTPPeriodicCheck(t, config, httpcheck.Spec{URL: targetURL + "/", Method: "GET", StatusCodes: []int{200}, TimeoutMS: 2000, MaxBodyBytes: 1024}, "failure", "tls_invalid")
}

func startPrivateHTTPTarget(t *testing.T) (Config, string) {
	t.Helper()
	return startPrivateTarget(t, false)
}

func startPrivateTarget(t *testing.T, useTLS bool) (Config, string) {
	t.Helper()
	if os.Getenv("MYPROBE_TEST_PRIVATE_HTTP") != "1" {
		t.Skip("set MYPROBE_TEST_PRIVATE_HTTP=1 in an isolated environment")
	}
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal("cannot enumerate fixture interfaces")
	}
	var listener net.Listener
	var address netip.Addr
	for _, candidate := range addresses {
		prefix, err := netip.ParsePrefix(candidate.String())
		if err != nil || !prefix.Addr().Is4() || !prefix.Addr().IsPrivate() {
			continue
		}
		address = prefix.Addr()
		listener, err = net.Listen("tcp4", net.JoinHostPort(address.String(), "0"))
		if err == nil {
			break
		}
	}
	if listener == nil {
		t.Fatal("private fixture listener unavailable")
	}
	target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/failure":
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		case "/redirect":
			http.Redirect(w, r, "/redirect", http.StatusFound)
			return
		case "/oversized":
			_, _ = io.WriteString(w, strings.Repeat("x", 2048))
			return
		case "/slow":
			<-r.Context().Done()
			return
		}
		_, _ = io.WriteString(w, "fixture healthy")
	}))
	target.Listener.Close()
	target.Listener = listener
	// Expected TLS handshake errors must not print fixture interface addresses.
	target.Config.ErrorLog = log.New(io.Discard, "", 0)
	scheme := "http://"
	if useTLS {
		target.StartTLS()
		scheme = "https://"
	} else {
		target.Start()
	}
	t.Cleanup(target.Close)
	port := listener.Addr().(*net.TCPAddr).Port
	config := Config{HTTPPrivateCIDRs: []string{address.String() + "/32"}, HTTPAdditionalPorts: []int{port}}
	return config, scheme + net.JoinHostPort(address.String(), strconv.Itoa(port))
}

func runHTTPPeriodicCheck(t *testing.T, agentConfig Config, spec httpcheck.Spec, outcome, errorClass string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "http-e2e.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	node, token, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	gateway := agentgateway.New(db, agentgateway.NewHub())
	gateway.EnableHTTPProbes(true)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/agent/ws", gateway.WebSocketV2)
	server := httptest.NewServer(mux)
	defer server.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	agentConfig.ServerURL, agentConfig.Token, agentConfig.AgentVersion = server.URL, token, "fixture"
	agentConfig.CollectionPeriod, agentConfig.ReportPeriod = time.Second, time.Second
	client, err := New(agentConfig, collector.New(collector.Config{}), logger)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); client.connectAndRead(ctx) }()
	defer func() {
		cancel()
		client.clearConnection(nil)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("agent failed to stop")
		}
	}()
	for client.currentConnection() == nil && ctx.Err() == nil {
		time.Sleep(time.Millisecond)
	}
	if ctx.Err() != nil {
		t.Fatal("handshake timed out")
	}
	service, err := db.SaveHTTPService(ctx, store.HTTPService{Name: "HTTP fixture", Enabled: true, IntervalSeconds: 30, NodeIDs: []string{node.ID}, Spec: spec})
	if err != nil {
		t.Fatal(err)
	}
	scheduled := make(chan struct{})
	go func() { defer close(scheduled); scheduler.NewHTTP(db, gateway, logger).Run(ctx) }()
	defer func() {
		cancel()
		select {
		case <-scheduled:
		case <-time.After(3 * time.Second):
			t.Error("scheduler failed to stop")
		}
	}()
	for ctx.Err() == nil {
		observations, err := db.RecentHTTPResults(ctx, service.ID, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(observations) > 0 && observations[0].Result != nil {
			if len(observations) != 1 || observations[0].Result.Outcome != outcome || observations[0].Result.ErrorClass != errorClass {
				t.Fatalf("unexpected result: %+v", observations)
			}
			if errorClass == "tls_invalid" && (observations[0].Result.StatusCode != 0 || len(observations[0].Result.Certificates) != 0) {
				t.Fatal("failed TLS handshake produced HTTP status or verified certificate evidence")
			}
			stats, err := db.HTTPServiceStatistics(ctx, service.ID, node.ID, service.UpdatedAt, service.UpdatedAt.Add(30*time.Second), service.UpdatedAt.Add(5*time.Minute))
			if err != nil || stats.Expected != 1 {
				t.Fatalf("unexpected expected count: %+v %v", stats, err)
			}
			if (outcome == "success" && (stats.Success != 1 || stats.Failure != 0 || stats.Missing != 0)) ||
				(outcome == "failure" && (stats.Success != 0 || stats.Failure != 1 || stats.Missing != 0)) ||
				(outcome == "unobserved" && (stats.Success != 0 || stats.Failure != 0 || stats.Missing != 1 || stats.Unobserved != 1)) {
				t.Fatalf("incorrect outcome accounting: %+v", stats)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("periodic HTTP result was not persisted")
}
