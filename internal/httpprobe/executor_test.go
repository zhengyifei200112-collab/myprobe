package httpprobe

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
)

func TestExecutorLocalHTTPChecks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "service.example" {
			t.Errorf("host changed: %s", r.Host)
		}
		switch r.URL.Path {
		case "/error":
			w.WriteHeader(500)
		case "/large":
			w.Write([]byte(strings.Repeat("x", 2048)))
		case "/redirect":
			http.Redirect(w, r, "/ok", 302)
		case "/loop":
			http.Redirect(w, r, "/loop", 302)
		case "/private":
			http.Redirect(w, r, "http://127.0.0.1/", 302)
		case "/slow":
			<-r.Context().Done()
		default:
			w.Write([]byte("ready"))
		}
	}))
	defer server.Close()
	e := New(Policy{})
	e.dialer.resolver = &fakeResolver{addresses: []netip.Addr{netip.MustParseAddr("8.8.8.8")}}
	e.dialer.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "8.8.8.8:80" {
			t.Errorf("unvalidated dial %s", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	for _, test := range []struct{ path, method, outcome, class string }{
		{"/ok", "GET", "success", ""}, {"/ok", "HEAD", "success", ""},
		{"/error", "GET", "failure", "status_mismatch"}, {"/large", "GET", "failure", "response_too_large"},
		{"/redirect", "GET", "success", ""}, {"/loop", "GET", "failure", "redirect_limit"},
		{"/private", "GET", "unobserved", "policy_denied"}, {"/slow", "GET", "failure", "timeout"},
	} {
		t.Run(test.path+test.method, func(t *testing.T) {
			now := time.Now().UTC()
			task := httpcheck.Task{ID: "task", ServiceID: "service", Revision: 1, ScheduledAt: now, ExpiresAt: now.Add(time.Minute), Spec: httpcheck.Spec{URL: "http://service.example" + test.path, Method: test.method, StatusCodes: []int{200}, TimeoutMS: 100, MaxRedirects: 3, MaxBodyBytes: 1024}}
			result := e.Execute(context.Background(), task)
			if result.Outcome != test.outcome || result.ErrorClass != test.class {
				t.Fatalf("result: %+v", result)
			}
			if err := result.ValidateFor(task, time.Now()); err != nil {
				t.Fatalf("executor emitted invalid result: %v %+v", err, result)
			}
		})
	}
}
