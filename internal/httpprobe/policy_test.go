package httpprobe

import (
	"net/netip"
	"testing"
)

func TestDefaultHTTPPolicy(t *testing.T) {
	p := Policy{}
	for _, raw := range []string{"127.0.0.1", "::1", "::ffff:127.0.0.1", "169.254.169.254", "100.100.100.200", "10.0.0.1", "172.16.1.2", "192.168.1.1", "fc00::1", "fe80::1", "224.0.0.1", "0.0.0.0", "::", "192.0.2.1", "2001:db8::1", "64:ff9b::7f00:1", "2002:7f00:1::"} {
		if p.Allows(netip.MustParseAddr(raw), 443) {
			t.Errorf("allowed denied address %s", raw)
		}
	}
	for _, raw := range []string{"8.8.8.8", "2606:4700:4700::1111", "::ffff:8.8.8.8"} {
		if !p.Allows(netip.MustParseAddr(raw), 443) || p.Allows(netip.MustParseAddr(raw), 8080) {
			t.Errorf("public/port policy mismatch %s", raw)
		}
	}
}

func TestLocalPrivatePolicyCannotRelaxForbiddenRanges(t *testing.T) {
	wide, err := NewPolicy([]string{"fc00::/7"}, nil)
	if err != nil || wide.Allows(netip.MustParseAddr("fd00:ec2::254"), 80) {
		t.Fatal("metadata exception overridden")
	}
	p, err := NewPolicy([]string{"10.2.0.0/16", "fd12::/32"}, []int{8080})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Allows(netip.MustParseAddr("10.2.1.1"), 8080) || p.Allows(netip.MustParseAddr("10.3.1.1"), 8080) {
		t.Fatal("CIDR boundary incorrect")
	}
	if !p.Allows(netip.MustParseAddr("::ffff:10.2.1.1"), 8080) {
		t.Fatal("mapped private address not normalized")
	}
	for _, raw := range []string{"0.0.0.0/0", "127.0.0.0/8", "169.254.0.0/16", "::/0", "::ffff:10.0.0.0/104", "10.0.0.0/7"} {
		if _, err := NewPolicy([]string{raw}, nil); err == nil {
			t.Errorf("unsafe allowance %s", raw)
		}
	}
	if _, err := NewPolicy(nil, []int{65536}); err == nil {
		t.Fatal("invalid port accepted")
	}
}
