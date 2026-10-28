package agentclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	check "github.com/zhengyifei200112-collab/myprobe/internal/protocol/networkcheck"
	v1 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"golang.org/x/text/encoding/simplifiedchinese"
)

func networkFixtureTask(kind, family, host string) check.Task {
	task := check.Task{Task: v1.Task{ID: "synthetic-network", TargetID: "synthetic-target", Kind: kind, Host: host, TimeoutMS: 3000, ExpiresAt: time.Now().Add(time.Minute)}, AddressFamily: family}
	if kind == v1.TaskKindPing {
		task.PacketCount = 5
	}
	return task
}

func TestNetworkProbeRealTCPAddressFamilies(t *testing.T) {
	for _, c := range []struct{ network, bind, family string }{{"tcp4", "127.0.0.1:0", check.FamilyIPv4}, {"tcp6", "[::1]:0", check.FamilyIPv6}} {
		t.Run(c.network, func(t *testing.T) {
			listener, err := net.Listen(c.network, c.bind)
			if err != nil {
				if c.network == "tcp6" && os.Getenv("MYPROBE_TEST_NETWORK_EVIDENCE_NATIVE") != "1" {
					t.Skipf("IPv6 unavailable: %v", err)
				}
				t.Fatal(err)
			}
			defer listener.Close()
			address := listener.Addr().(*net.TCPAddr)
			accepted := make(chan struct{})
			go func() {
				defer close(accepted)
				connection, err := listener.Accept()
				if err == nil {
					connection.Close()
				}
			}()
			task := networkFixtureTask(v1.TaskKindTCPing, c.family, address.IP.String())
			task.Port = address.Port
			result := defaultNetworkProbeExecutor().execute(context.Background(), task)
			if result.ValidateAgainst(task, time.Now()) != nil || result.Outcome != check.Success || result.AddressFamily != c.family || result.Packets != nil {
				t.Fatalf("actual TCP evidence = %+v", result)
			}
			<-accepted
		})
	}
}

func TestNetworkProbeResolutionAndFailureClassification(t *testing.T) {
	for _, test := range []struct {
		name, family, wantOutcome, wantClass string
		addresses                            []netip.Addr
		lookupErr, dialErr                   error
	}{
		{"DNS failure", check.FamilyIPv6, check.Unobserved, "dns", nil, errors.New("synthetic DNS error"), nil},
		{"IPv6 unavailable", check.FamilyIPv6, check.Unobserved, "no_address", []netip.Addr{netip.MustParseAddr("192.0.2.53")}, nil, nil},
		{"socket family unsupported", check.FamilyIPv6, check.Unobserved, "unsupported", []netip.Addr{netip.MustParseAddr("2001:db8::53")}, nil, syscall.EAFNOSUPPORT},
		{"refused TCP", check.FamilyIPv4, check.Failure, "refused", []netip.Addr{netip.MustParseAddr("192.0.2.53")}, nil, fmt.Errorf("synthetic: %w", syscall.ECONNREFUSED)},
		{"network failure", check.FamilyIPv4, check.Failure, "unreachable", []netip.Addr{netip.MustParseAddr("192.0.2.53")}, nil, syscall.ENETUNREACH},
	} {
		t.Run(test.name, func(t *testing.T) {
			executor := defaultNetworkProbeExecutor()
			executor.lookup = func(ctx context.Context, network, host string) ([]netip.Addr, error) {
				if network != "ip4" && network != "ip6" {
					t.Fatal("unconstrained resolver")
				}
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("resolution lacks total deadline")
				}
				return test.addresses, test.lookupErr
			}
			executor.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
				if strings.Contains(address, "example.test") {
					t.Fatal("hostname was resolved again by dial")
				}
				return nil, test.dialErr
			}
			task := networkFixtureTask(v1.TaskKindTCPing, test.family, "example.test")
			task.Port = 443
			result := executor.execute(context.Background(), task)
			if result.Outcome != test.wantOutcome || result.ErrorClass != test.wantClass || result.ValidateAgainst(task, time.Now()) != nil {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}

const linuxPartial = "64 bytes from 192.0.2.53: icmp_seq=1 ttl=64 time=12.0 ms\n64 bytes from 192.0.2.53: icmp_seq=3 ttl=64 time=14.0 ms\n5 packets transmitted, 2 received, 60% packet loss, time 1000ms\nrtt min/avg/max/mdev = 12.000/13.000/14.000/1.000 ms\n"

func TestNetworkProbePingEvidenceNeverInventsPacketsOrRTT(t *testing.T) {
	for _, test := range []struct {
		name, output, outcome, class string
		runErr                       error
		packets                      bool
	}{
		{"partial loss", linuxPartial, check.Success, "", nil, true},
		{"settled total loss", "5 packets transmitted, 0 received, 100% packet loss, time 1000ms\n", check.Failure, "timeout", errors.New("tool nonzero exit"), true},
		{"no summary does not imply sent", "", check.Unobserved, "internal", context.DeadlineExceeded, false},
		{"no invented elapsed RTT", "64 bytes from 192.0.2.53: icmp_seq=1 ttl=64 time=12 ms\n", check.Unobserved, "internal", nil, false},
		{"permission unavailable", "ping: socket: Operation not permitted\n", check.Unobserved, "unsupported", errors.New("tool failed"), false},
		{"missing binary", "", check.Unobserved, "unsupported", &exec.Error{Name: "ping", Err: exec.ErrNotFound}, false},
		{"output cap", "", check.Unobserved, "internal", errEvidenceOutputLimit, false},
		{"zero sent", "0 packets transmitted, 0 received, 100% packet loss\n", check.Unobserved, "no_packets_sent", errors.New("tool failed"), false},
		{"contradictory summary", strings.Replace(linuxPartial, "5 packets transmitted, 2 received", "0 packets transmitted, 0 received", 1), check.Unobserved, "internal", nil, false},
		{"local sends failed despite attempted count", "ping: sendmsg: Network is unreachable\n5 packets transmitted, 0 received, 100% packet loss\n", check.Unobserved, "send_error", errors.New("tool failed"), false},
		{"local error in partial success invalidates sent count", "ping: sendmsg: Network is unreachable\n" + linuxPartial, check.Success, "", errors.New("partial send failed"), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			executor := defaultNetworkProbeExecutor()
			executor.goos = "linux"
			executor.run = func(ctx context.Context, program string, args []string) ([]byte, error) {
				if program != "ping" || args[0] != "-4" || args[len(args)-1] != "192.0.2.53" {
					t.Fatalf("unbounded/nonliteral command %s %v", program, args)
				}
				return []byte(test.output), test.runErr
			}
			task := networkFixtureTask(v1.TaskKindPing, check.FamilyIPv4, "192.0.2.53")
			result := executor.execute(context.Background(), task)
			if result.Outcome != test.outcome || result.ErrorClass != test.class || (result.Packets != nil) != test.packets || result.ValidateAgainst(task, time.Now()) != nil {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}

func TestNetworkProbeWindowsPacketSummaryRequiresEchoReplies(t *testing.T) {
	for _, text := range []string{
		"Reply from 192.0.2.53: bytes=32 time<1ms TTL=64\nPackets: Sent = 1, Received = 1, Lost = 0 (0% loss),\nMinimum = 0ms, Maximum = 0ms, Average = 0ms\n",
		"来自 192.0.2.53 的回复: 字节=32 时间<1ms TTL=64\n数据包: 已发送 = 1，已接收 = 1，丢失 = 0 (0% 丢失)，\n最短 = 0ms，最长 = 0ms，平均 = 0ms\n",
	} {
		stats := parsePingEvidence(text, "windows", 1)
		if stats.packets == nil || stats.packets.Sent != 1 || stats.packets.Received != 1 || stats.rtt == nil || *stats.rtt != 0 {
			t.Fatalf("real tool zero-quantized RTT/counters lost: %+v", stats)
		}
		gbk, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(text))
		if err != nil {
			t.Fatal(err)
		}
		if parsePingEvidence(networkToolText(gbk, "windows"), "windows", 1).packets == nil {
			t.Fatal("GBK packet summary was not recognized")
		}
	}
	stats := parsePingEvidence("Reply from 192.0.2.1: Destination host unreachable.\nPackets: Sent = 1, Received = 1, Lost = 0 (0% loss),\n", "windows", 1)
	if stats.packets != nil || stats.rtt != nil || stats.transmitted != 1 {
		t.Fatal("unreachable response became an ICMP Echo Reply")
	}
	for _, text := range []string{"PING: transmit failed. General failure.", "PING: 传输失败。常规故障。"} {
		if !localPingSendError(text) {
			t.Fatal("local Windows failure could become exact packet loss")
		}
	}
	for _, text := range []string{linuxPartial + linuxPartial, strings.Replace(linuxPartial, "2 received", "6 received", 1)} {
		if parsePingEvidence(text, "linux", 5).packets != nil {
			t.Fatal("duplicate/invalid tool summary trusted")
		}
	}
}

func TestNetworkProbeTotalDeadlineCancellationAndOutputBound(t *testing.T) {
	executor := defaultNetworkProbeExecutor()
	executor.lookup = func(ctx context.Context, network, host string) ([]netip.Addr, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	task := networkFixtureTask(v1.TaskKindTCPing, check.FamilyIPv6, "example.test")
	task.Port = 443
	task.TimeoutMS = 100
	started := time.Now()
	result := executor.execute(context.Background(), task)
	if result.Outcome != check.Unobserved || result.ErrorClass != "dns" || time.Since(started) > time.Second {
		t.Fatalf("DNS exceeded shared task deadline: %+v", result)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := executor.execute(ctx, task); got.ErrorClass != "cancelled" || got.AddressFamily != check.FamilyUnknown {
		t.Fatal("cancellation became failure")
	}
	buffer := new(evidenceOutput)
	for i := 0; i < 10; i++ {
		if n, err := buffer.Write(bytes.Repeat([]byte("x"), maxEvidenceOutput)); n != maxEvidenceOutput || err != nil {
			t.Fatal("output drain blocked")
		}
	}
	if len(buffer.data) != maxEvidenceOutput || !buffer.overflow {
		t.Fatal("tool output unbounded")
	}
}

func TestNetworkProbeCommandFamilyIsExplicitAndNoShell(t *testing.T) {
	for _, goos := range []string{"linux", "windows", "darwin"} {
		for _, address := range []string{"192.0.2.53", "2001:db8::53"} {
			program, args, err := evidencePingCommand(goos, netip.MustParseAddr(address), 2, 3*time.Second)
			if err != nil || args[len(args)-1] != address || program != "ping" && program != "ping6" {
				t.Fatalf("unexpected command %s %v", program, args)
			}
			if goos != "darwin" && args[0] != map[bool]string{true: "-4", false: "-6"}[netip.MustParseAddr(address).Is4()] {
				t.Fatal("family flag missing")
			}
			if goos == "linux" {
				for _, value := range args {
					if value == "-w" {
						t.Fatal("iputils deadline can send past the requested count when replies are absent")
					}
				}
			}
		}
	}
	if _, _, err := evidencePingCommand("unknown", netip.MustParseAddr("192.0.2.53"), 1, time.Second); err == nil {
		t.Fatal("unknown platform silently fell back")
	}
}

func TestNetworkProbeNativeICMPEvidence(t *testing.T) {
	if os.Getenv("MYPROBE_TEST_NETWORK_EVIDENCE_NATIVE") != "1" {
		t.Skip("opt in to owned-loopback ICMP/tool tests")
	}
	for _, c := range []struct{ host, family string }{{"127.0.0.1", check.FamilyIPv4}, {"::1", check.FamilyIPv6}} {
		t.Run(c.family, func(t *testing.T) {
			task := networkFixtureTask(v1.TaskKindPing, c.family, c.host)
			task.PacketCount = 2
			task.TimeoutMS = 5000
			result := defaultNetworkProbeExecutor().execute(context.Background(), task)
			if result.ValidateAgainst(task, time.Now()) != nil || result.Outcome != check.Success || result.Packets == nil || result.Packets.Sent != 2 || result.Packets.Received != 2 || result.AddressFamily != c.family {
				t.Fatalf("%s actual ICMP proof = %+v", runtime.GOOS, result)
			}
			t.Logf("native=%s family=%s sent=%d received=%d rtt_ms=%f", runtime.GOOS, result.AddressFamily, result.Packets.Sent, result.Packets.Received, *result.RTTMS)
		})
	}
}

func TestNetworkProbeNativeSilentTarget(t *testing.T) {
	if os.Getenv("MYPROBE_TEST_NETWORK_EVIDENCE_LOSS") != "1" {
		t.Skip("requires owned namespace with echo replies disabled")
	}
	if runtime.GOOS != "linux" {
		t.Fatal("silent-target isolation requires Linux")
	}
	task := networkFixtureTask(v1.TaskKindPing, check.FamilyIPv4, "192.0.2.53")
	task.PacketCount = 3
	result := defaultNetworkProbeExecutor().execute(context.Background(), task)
	if result.ValidateAgainst(task, time.Now()) != nil || result.Outcome != check.Failure || result.ErrorClass != "timeout" || result.Packets == nil || result.Packets.Sent != 3 || result.Packets.Received != 0 || result.AddressFamily != check.FamilyIPv4 {
		t.Fatalf("silent-target evidence = %+v", result)
	}
	t.Logf("native=%s family=%s sent=%d received=%d outcome=%s", runtime.GOOS, result.AddressFamily, result.Packets.Sent, result.Packets.Received, result.Outcome)
}

func TestNetworkProbeNativeNoRoute(t *testing.T) {
	if os.Getenv("MYPROBE_TEST_NETWORK_EVIDENCE_LOSS") != "1" {
		t.Skip("requires owned namespace with no route to the second fixture address")
	}
	task := networkFixtureTask(v1.TaskKindPing, check.FamilyIPv4, "192.0.2.54")
	task.PacketCount = 2
	result := defaultNetworkProbeExecutor().execute(context.Background(), task)
	if result.ValidateAgainst(task, time.Now()) != nil || result.Outcome != check.Unobserved || result.ErrorClass != "send_error" || result.Packets != nil || result.AddressFamily != check.FamilyUnknown {
		t.Fatalf("no-route must not claim transmitted packets or target failure: %+v", result)
	}
	t.Logf("native=%s no_route_outcome=%s class=%s packet_evidence=unavailable", runtime.GOOS, result.Outcome, result.ErrorClass)
}
