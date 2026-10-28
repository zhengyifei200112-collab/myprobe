package agentclient

import (
	"net/netip"
	"reflect"
	"testing"
	"time"
)

// Runs on every OS: the dangerous ping6 -W option must never be confused with
// Darwin IPv4 ping's millisecond response wait, even without a local Mac.
func TestNetworkProbeDarwinEchoCommand(t *testing.T) {
	program, args, err := evidencePingCommand("darwin", netip.MustParseAddr("::1"), 3, 2*time.Second)
	if err != nil || program != "ping6" || !reflect.DeepEqual(args, []string{"-n", "-c", "3", "-i", "0.25", "::1"}) {
		t.Fatalf("IPv6 must remain bounded Echo Requests, got %s %q %v", program, args, err)
	}
	program, args, err = evidencePingCommand("darwin", netip.MustParseAddr("127.0.0.1"), 3, 2*time.Second)
	if err != nil || program != "ping" || !reflect.DeepEqual(args, []string{"-n", "-c", "3", "-i", "0.25", "-W", "1400", "127.0.0.1"}) {
		t.Fatalf("IPv4 response budget: %s %q %v", program, args, err)
	}
}
