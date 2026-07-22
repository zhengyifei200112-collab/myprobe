package agentgateway

import (
	"net/http/httptest"
	"testing"
)

func TestClientIPIgnoresForwardingHeadersFromUntrustedPeer(t *testing.T) {
	gateway := &Gateway{}
	request := httptest.NewRequest("POST", "/api/v1/agent/report", nil)
	request.RemoteAddr = "198.51.100.20:4242"
	request.Header.Set("X-Forwarded-For", "203.0.113.99")

	if got := gateway.clientIP(request); got != "198.51.100.20" {
		t.Fatalf("clientIP() = %q, want direct peer address", got)
	}
}

func TestClientIPWalksTrustedProxyChainFromRight(t *testing.T) {
	gateway := &Gateway{}
	gateway.SetTrustedProxies([]string{"10.0.0.0/8", "192.0.2.10"})
	request := httptest.NewRequest("POST", "/api/v1/agent/report", nil)
	request.RemoteAddr = "10.0.0.8:4242"
	request.Header.Set("X-Forwarded-For", "203.0.113.7, 192.0.2.10, 10.0.0.9")

	if got := gateway.clientIP(request); got != "203.0.113.7" {
		t.Fatalf("clientIP() = %q, want first untrusted address", got)
	}
}
