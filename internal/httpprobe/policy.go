// Package httpprobe executes bounded service checks under Agent-local policy.
package httpprobe

import (
	"errors"
	"net/netip"
)

var ErrPolicyDenied = errors.New("HTTP target denied by local policy")

var privateRanges = prefixes("10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7")
var excludedRanges = prefixes(
	"0.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
	"192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "198.18.0.0/15",
	"198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4",
	"2001::/23", "2001:db8::/32", "2002::/16",
	"fd00:ec2::254/128", "168.63.129.16/32",
)

func prefixes(values ...string) []netip.Prefix {
	result := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		result = append(result, netip.MustParsePrefix(value))
	}
	return result
}

// Policy's zero value allows ports 80/443 and ordinary public unicast addresses.
// Additional ranges are restricted to RFC1918/ULA; even a broad administrator
// allowlist cannot enable loopback, link-local or metadata endpoints.
type Policy struct {
	private []netip.Prefix
	ports   map[uint16]bool
}

func NewPolicy(privateCIDRs []string, additionalPorts []int) (Policy, error) {
	if len(privateCIDRs) > 64 || len(additionalPorts) > 64 {
		return Policy{}, errors.New("HTTP policy has too many entries")
	}
	result := Policy{ports: make(map[uint16]bool)}
	for _, raw := range privateCIDRs {
		prefix, err := netip.ParsePrefix(raw)
		if err != nil || prefix.Addr().Is4In6() {
			return Policy{}, errors.New("invalid private HTTP range")
		}
		prefix = prefix.Masked()
		valid := false
		for _, parent := range privateRanges {
			if parent.Contains(prefix.Addr()) && prefix.Bits() >= parent.Bits() {
				valid = true
			}
		}
		if !valid {
			return Policy{}, errors.New("HTTP private allowance must stay within private address space")
		}
		result.private = append(result.private, prefix)
	}
	for _, port := range additionalPorts {
		if port < 1 || port > 65535 {
			return Policy{}, errors.New("invalid additional HTTP port")
		}
		result.ports[uint16(port)] = true
	}
	return result, nil
}

func (p Policy) Allows(address netip.Addr, port uint16) bool {
	if port != 80 && port != 443 && !p.ports[port] {
		return false
	}
	if !address.IsValid() || address.Zone() != "" {
		return false
	}
	address = address.Unmap()
	if !address.IsGlobalUnicast() || address.IsLoopback() || address.IsLinkLocalUnicast() {
		return false
	}
	for _, prefix := range excludedRanges {
		if prefix.Contains(address) {
			return false
		}
	}
	if address.IsPrivate() {
		for _, prefix := range p.private {
			if prefix.Contains(address) {
				return true
			}
		}
		return false
	}
	// Limit IPv6 to global unicast allocation space, excluding transition and
	// special-use ranges above. This intentionally fails closed for other ranges.
	return address.Is4() || netip.MustParsePrefix("2000::/3").Contains(address)
}
