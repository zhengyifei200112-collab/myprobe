package httpprobe

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
)

type fakeResolver struct {
	addresses []netip.Addr
	calls     int
}

func (r *fakeResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	r.calls++
	return r.addresses, nil
}

func TestGuardedDialUsesValidatedLiteral(t *testing.T) {
	r := &fakeResolver{addresses: []netip.Addr{netip.MustParseAddr("8.8.8.8")}}
	var target string
	left, right := net.Pipe()
	defer right.Close()
	d := guardedDialer{resolver: r, dial: func(_ context.Context, network, address string) (net.Conn, error) { target = address; return left, nil }}
	connection, err := d.DialContext(context.Background(), "tcp", "service.example:443")
	if err != nil {
		t.Fatal(err)
	}
	connection.Close()
	if target != "8.8.8.8:443" || r.calls != 1 {
		t.Fatalf("unvalidated dial %q lookups %d", target, r.calls)
	}
}

func TestGuardedDialRejectsMixedOrReboundAnswers(t *testing.T) {
	r := &fakeResolver{addresses: []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("127.0.0.1")}}
	d := guardedDialer{resolver: r, dial: func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("forbidden answer dialed")
		return nil, nil
	}}
	if _, err := d.DialContext(context.Background(), "tcp", "service.example:80"); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("mixed answers: %v", err)
	}
	r.addresses = []netip.Addr{netip.MustParseAddr("169.254.169.254")}
	if _, err := d.DialContext(context.Background(), "tcp", "service.example:80"); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("rebound answer: %v", err)
	}
	for _, address := range []string{"[::ffff:127.0.0.1]:80", "8.8.8.8:8080", "8.8.8.8:0", "invalid"} {
		if _, err := d.DialContext(context.Background(), "tcp", address); !errors.Is(err, ErrPolicyDenied) {
			t.Fatalf("accepted %s", address)
		}
	}
}
