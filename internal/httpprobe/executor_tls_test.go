package httpprobe

import (
	"compress/gzip"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
)

func executorTask(url string) httpcheck.Task {
	now := time.Now().UTC()
	return httpcheck.Task{ID: "task", ServiceID: "service", Revision: 1, ScheduledAt: now, ExpiresAt: now.Add(time.Minute), Spec: httpcheck.Spec{URL: url, Method: "GET", StatusCodes: []int{200}, TimeoutMS: 5000, MaxRedirects: 3, MaxBodyBytes: 1024}}
}

type constantResolver struct{}

func (constantResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
}

func localExecutor(server *httptest.Server) *Executor {
	e := New(Policy{})
	e.dialer.resolver = constantResolver{}
	e.dialer.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	return e
}

func TestTLSVerificationAndEvidence(t *testing.T) {
	for _, expired := range []bool{false, true} {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		leaf := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"service.example"}, NotBefore: now.Add(-2 * time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
		if expired {
			leaf.NotAfter = now.Add(-time.Hour)
		}
		der, err := x509.CreateCertificate(rand.Reader, leaf, leaf, &key.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		certificate, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/downgrade" {
				http.Redirect(w, r, "http://service.example/", 302)
				return
			}
			w.Write([]byte("ready"))
		}))
		server.Config.ErrorLog = log.New(io.Discard, "", 0)
		server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
		server.StartTLS()
		func() {
			defer server.Close()
			e := localExecutor(server)
			task := executorTask("https://service.example/")
			result := e.Execute(context.Background(), task)
			if result.Outcome != "failure" || (result.ErrorClass != "tls_invalid" && result.ErrorClass != "certificate_expired") {
				t.Fatalf("untrusted TLS accepted: %+v", result)
			}
			e.rootCAs = x509.NewCertPool()
			e.rootCAs.AddCert(certificate)
			result = e.Execute(context.Background(), task)
			if expired {
				if result.ErrorClass != "certificate_expired" {
					t.Fatalf("expiry not classified: %+v", result)
				}
				return
			}
			if result.Outcome != "success" || len(result.Certificates) != 1 || !result.Certificates[0].Verified {
				t.Fatalf("TLS evidence: %+v", result)
			}
			if err := result.ValidateFor(task, time.Now()); err != nil {
				t.Fatal(err)
			}
			task.Spec.URL = "https://wrong.example/"
			if result = e.Execute(context.Background(), task); result.ErrorClass != "tls_invalid" {
				t.Fatal("hostname mismatch accepted")
			}
			task.Spec.URL = "https://service.example/downgrade"
			if result = e.Execute(context.Background(), task); result.ErrorClass != "policy_denied" {
				t.Fatal("TLS downgrade accepted")
			}
		}()
	}
}

func TestDecodedBodyLimitAndCancellation(t *testing.T) {
	started := make(chan struct{}, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/wait" {
			started <- struct{}{}
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		writer := gzip.NewWriter(w)
		writer.Write([]byte(strings.Repeat("x", 2048)))
		writer.Close()
	}))
	defer server.Close()
	e := localExecutor(server)
	if result := e.Execute(context.Background(), executorTask("http://service.example/")); result.ErrorClass != "response_too_large" {
		t.Fatalf("decoded limit bypassed: %+v", result)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan httpcheck.Result, 4)
	for range 4 {
		go func() { done <- e.Execute(ctx, executorTask("http://service.example/wait")) }()
	}
	for range 4 {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("slots did not start")
		}
	}
	if result := e.Execute(context.Background(), executorTask("http://service.example/")); result.Outcome != "unobserved" || result.ErrorClass != "busy" {
		t.Fatal("concurrency not bounded")
	}
	cancel()
	for range 4 {
		select {
		case result := <-done:
			if result.ErrorClass != "cancelled" {
				t.Fatalf("cancel: %+v", result)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("cancellation failed")
		}
	}
	if len(e.slots) != 0 {
		t.Fatal("execution slot leaked")
	}
}
