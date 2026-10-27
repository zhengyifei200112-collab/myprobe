package agentclient

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/alerts"
	"github.com/zhengyifei200112-collab/myprobe/internal/auth"
	"github.com/zhengyifei200112-collab/myprobe/internal/collector"
	"github.com/zhengyifei200112-collab/myprobe/internal/config"
	"github.com/zhengyifei200112-collab/myprobe/internal/httpapi"
	"github.com/zhengyifei200112-collab/myprobe/internal/httpprobe"
	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
	"github.com/zhengyifei200112-collab/myprobe/internal/scheduler"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestCertificateReminderThroughRealTLSAgent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(30 * 24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	var selected atomic.Pointer[tls.Certificate]
	var fixtureAddress netip.Addr
	makeLeaf := func(serial int64, remaining time.Duration) (*tls.Certificate, string) {
		t.Helper()
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		leaf := &x509.Certificate{SerialNumber: big.NewInt(serial), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(remaining), IPAddresses: []net.IP{net.IP(fixtureAddress.AsSlice())}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
		raw, err := x509.CreateCertificate(rand.Reader, leaf, ca, &key.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		fingerprint := sha256.Sum256(raw)
		return &tls.Certificate{Certificate: [][]byte{raw, der}, PrivateKey: key}, hex.EncodeToString(fingerprint[:])
	}
	var firstSHA string
	agentConfig, targetURL := startPrivateTargetTLS(t, true, func(address netip.Addr) *tls.Config {
		fixtureAddress = address
		initial, sha := makeLeaf(2, 6*24*time.Hour)
		firstSHA = sha
		selected.Store(initial)
		return &tls.Config{MinVersion: tls.VersionTLS12, GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
			return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{*selected.Load()}}, nil
		}}
	})
	db, err := store.Open(ctx, filepath.Join(httpFixtureDir(t), "certificate-lifecycle.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	password, key := rand.Text(), rand.Text()+rand.Text()
	authentication := auth.New(db, time.Hour)
	if _, err := authentication.Bootstrap(ctx, "service-admin", password); err != nil {
		t.Fatal(err)
	}
	node, token, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "Certificate observer"})
	if err != nil {
		t.Fatal(err)
	}
	notices := make(chan alerts.Notification, 10)
	var sent atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error("fixture notification read failed")
			w.WriteHeader(500)
			return
		}
		if strings.Contains(string(raw), targetURL) {
			t.Error("private target leaked in certificate notification")
		}
		var n alerts.Notification
		if json.Unmarshal(raw, &n) != nil {
			t.Error("invalid fixture notice")
			w.WriteHeader(500)
			return
		}
		sent.Add(1)
		notices <- n
		w.WriteHeader(204)
	}))
	defer receiver.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewUnstartedServer(nil)
	origin := "http://" + server.Listener.Addr().String()
	hub := agentgateway.NewHub()
	gateway := agentgateway.New(db, hub)
	gateway.EnableHTTPProbes(true)
	svc := alerts.New(db, key, alerts.NewHTTPSender(receiver.Client()), logger, origin)
	server.Config.Handler = httpapi.NewWithAlertService(config.Config{EncryptionKey: key, SessionTTL: time.Hour, HTTPProbesEnabled: true}, db, authentication, gateway, hub, svc).Handler()
	server.Start()
	defer server.Close()
	channel, err := svc.CreateChannel(ctx, "Certificate fixture channel", "webhook", alerts.ChannelConfig{URL: receiver.URL})
	if err != nil {
		t.Fatal(err)
	}
	service, err := db.SaveHTTPService(ctx, store.HTTPService{Name: "TLS lifecycle website", Enabled: true, IntervalSeconds: 30, NodeIDs: []string{node.ID}, Spec: httpcheck.Spec{URL: targetURL, Method: "GET", StatusCodes: []int{200}, TimeoutMS: 2000, MaxBodyBytes: 1024}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveServiceAlertSettings(ctx, store.ServiceAlertSettings{ServiceID: service.ID, ServiceRevision: service.Revision, ChannelID: channel.ID, CertificateRemindersEnabled: true, CooldownSeconds: 300}); err != nil {
		t.Fatal(err)
	}
	agentConfig.ServerURL, agentConfig.Token = server.URL, token
	agentConfig.CollectionPeriod, agentConfig.ReportPeriod = time.Second, time.Second
	client, err := New(agentConfig, collector.New(collector.Config{}), logger)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := httpprobe.NewPolicy(agentConfig.HTTPPrivateCIDRs, agentConfig.HTTPAdditionalPorts)
	if err != nil {
		t.Fatal(err)
	}
	// Only this fixture trusts its generated CA. All real TLS and address-policy
	// validation runs through the production executor, without dial substitution.
	client.httpExecutor = httpprobe.NewWithRootCAs(policy, roots)
	agentDone, schedulerDone, alertsDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() { defer close(agentDone); _ = client.Run(ctx) }()
	go func() { defer close(schedulerDone); scheduler.NewHTTP(db, gateway, logger).Run(ctx) }()
	go func() { defer close(alertsDone); svc.Run(ctx) }()
	defer func() {
		cancel()
		client.clearConnection(nil)
		for _, done := range []chan struct{}{agentDone, schedulerDone, alertsDone} {
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Error("certificate runtime did not stop")
			}
		}
	}()
	waitNotice := func(level string) alerts.Notification {
		t.Helper()
		select {
		case n := <-notices:
			if n.Kind != "service_certificate" || n.ServiceID != service.ID || n.State != "firing" || !strings.Contains(n.Message, level+" 天提醒档") || !strings.Contains(n.IncidentPath, n.IncidentID) {
				t.Fatal("certificate notification identity or threshold missing")
			}
			return n
		case <-ctx.Done():
			t.Fatal("real TLS reminder did not arrive")
			return alerts.Notification{}
		}
	}
	first := waitNotice("7")
	next, secondSHA := makeLeaf(3, 12*time.Hour)
	selected.Store(next)
	second := waitNotice("1")
	if first.IncidentID == second.IncidentID || firstSHA == secondSHA {
		t.Fatal("certificate rotation reused old subject")
	}
	for {
		records, err := db.ListCertificateReminders(ctx, service.ID, 0, 25)
		if err != nil {
			t.Fatal(err)
		}
		delivered := 0
		for _, r := range records {
			if r.DeliveryStatus == "delivered" {
				delivered++
			}
		}
		if delivered == 2 {
			if len(records) != 5 {
				t.Fatal("missed thresholds replayed or ledger missing")
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("certificate receipts were not persisted")
		case <-time.After(10 * time.Millisecond):
		}
	}
	old, err := db.Incident(ctx, first.IncidentID)
	if err != nil || old.State != "resolved" || old.ResolutionReason != "certificate_no_longer_observed" || old.Severity != "warning" {
		t.Fatal("verified rotation did not close old warning")
	}
	if os.Getenv("MYPROBE_TEST_HTTP_BROWSER") == "1" {
		cmd := exec.CommandContext(ctx, "node", "../../scripts/certificate-live-ui.cjs")
		cmd.Env = append(os.Environ(), "SERVICE_INCIDENT_URL="+second.IncidentPath, "SERVICE_INCIDENT_PASSWORD="+password)
		output, err := cmd.CombinedOutput()
		t.Log(string(output))
		if err != nil {
			t.Fatal(err)
		}
	}
	// A new periodic TLS result and repeated ticks must keep the same two intents.
	snapshots, err := db.HTTPHealthSnapshots(ctx, []string{service.ID}, time.Now().UTC())
	if err != nil || len(snapshots) != 1 || len(snapshots[0].Observers) != 1 || snapshots[0].Observers[0].Observation == nil {
		t.Fatal("TLS observation missing")
	}
	previousTask := snapshots[0].Observers[0].Observation.Result.TaskID
	for {
		snapshots, err = db.HTTPHealthSnapshots(ctx, []string{service.ID}, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		o := snapshots[0].Observers[0].Observation
		if o != nil && o.Result.TaskID != previousTask && o.Result.Outcome == "success" && len(o.Result.Certificates) == 1 && o.Result.Certificates[0].SHA256 == secondSHA {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("repeated TLS evidence did not arrive")
		case <-time.After(20 * time.Millisecond):
		}
	}
	if err := svc.Tick(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if sent.Load() != 2 {
		t.Fatal("duplicate reminder or technical recovery sent")
	}
	events, err := db.ListIncidents(ctx, store.IncidentFilter{ServiceID: service.ID, Limit: 25})
	if err != nil || len(events) != 2 {
		t.Fatal("certificate-only service manufactured technical faults")
	}
}
