package main

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/zhengyifei200112-collab/myprobe/internal/httpprobe"
)

func TestHTTPPolicyConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, cidrs, ports string
		invalid            bool
	}{
		{"defaults", "", "", false},
		{"explicit", " 10.2.0.0/16 , fd12::/64 ", " 8080,65535 ", false},
		{"empty range", "10.2.0.0/16,", "", true},
		{"empty port", "", "80,,443", true},
		{"zero", "", "0", true},
		{"overflow", "", "65536", true},
		{"negative", "", "-1", true},
		{"signed", "", "+80", true},
		{"fraction", "", "80.5", true},
		{"range syntax", "", "8000-9000", true},
		{"too many", "", strings.Repeat("80,", 64) + "80", true},
		{"public allowance", "0.0.0.0/0", "", true},
		{"loopback", "127.0.0.0/8", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cidrs, ports, err := parseHTTPPolicy(tc.cidrs, tc.ports)
			var policy httpprobe.Policy
			if err == nil {
				policy, err = httpprobe.NewPolicy(cidrs, ports)
			}
			if (err != nil) != tc.invalid {
				t.Fatalf("error = %v, want invalid %v", err, tc.invalid)
			}
			if tc.invalid {
				return
			}
			if !policy.Allows(netip.MustParseAddr("8.8.8.8"), 443) {
				t.Fatal("default port lost")
			}
			if policy.Allows(netip.MustParseAddr("10.2.0.1"), 8080) != (tc.name == "explicit") {
				t.Fatal("local allowances not applied")
			}
			if policy.Allows(netip.MustParseAddr("169.254.169.254"), 80) {
				t.Fatal("metadata target allowed")
			}
		})
	}
}
