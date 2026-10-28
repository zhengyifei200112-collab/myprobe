package v2

import (
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/networkcheck"
	v1 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

func TestNetworkEvidenceRequiresExplicitMutualVersionedCapability(t *testing.T) {
	for _, pair := range []struct {
		agent, server []string
		allowed       bool
	}{
		{nil, []string{networkcheck.Capability}, false},
		{[]string{networkcheck.Capability}, nil, false},
		{[]string{"network_probe.v2"}, []string{networkcheck.Capability}, false},
		{[]string{networkcheck.Capability, networkcheck.Capability}, []string{networkcheck.Capability}, true},
	} {
		got := NegotiateCapabilities(pair.agent, pair.server)
		if AllowsNetwork(got) != pair.allowed || len(got) > 1 {
			t.Fatalf("capability negotiation = %v", got)
		}
	}
	for _, kind := range []string{TypeNetworkTask, TypeNetworkResult} {
		envelope, err := NewEnvelope(kind, 3, map[string]string{"synthetic": "payload still requires separate validation"})
		if err != nil || envelope.Validate(time.Now()) != nil {
			t.Fatal("v2 outer envelope rejected", err)
		}
		envelope.Version = v1.Version
		if v1.Envelope(envelope).Validate(time.Now()) == nil {
			t.Fatal("new message leaked into v1")
		}
	}
}
