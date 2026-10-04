package httpprobe

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strconv"
)

type resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

type guardedDialer struct {
	policy   Policy
	resolver resolver
	dial     func(context.Context, string, string) (net.Conn, error)
}

func (d guardedDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if network != "tcp" {
		return nil, ErrPolicyDenied
	}
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return nil, ErrPolicyDenied
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 {
		return nil, ErrPolicyDenied
	}
	var addresses []netip.Addr
	if literal, err := netip.ParseAddr(host); err == nil {
		addresses = []netip.Addr{literal}
	} else {
		r := d.resolver
		if r == nil {
			r = net.DefaultResolver
		}
		addresses, err = r.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
	}
	if len(addresses) == 0 {
		return nil, &net.DNSError{Err: "no target addresses", IsNotFound: true}
	}
	if len(addresses) > 64 {
		return nil, ErrPolicyDenied
	}
	// Reject mixed allowed/forbidden answers before attempting any connection.
	for _, address := range addresses {
		if !d.policy.Allows(address, uint16(port)) {
			return nil, ErrPolicyDenied
		}
	}
	dial := d.dial
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	var lastError error
	for _, address := range addresses {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		connection, err := dial(ctx, "tcp", net.JoinHostPort(address.Unmap().String(), portText))
		if err == nil {
			return connection, nil
		}
		lastError = err
	}
	if lastError == nil {
		lastError = errors.New("HTTP target connection failed")
	}
	return nil, lastError
}
