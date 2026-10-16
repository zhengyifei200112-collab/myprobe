package agentclient

import (
	"context"
	"errors"
	"math"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	check "github.com/zhengyifei200112-collab/myprobe/internal/protocol/networkcheck"
	v1 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

const maxEvidenceOutput = 32 << 10

var errEvidenceOutputLimit = errors.New("network tool output limit")
var errEvidenceUnsupported = errors.New("network tool unsupported")

type networkProbeExecutor struct {
	goos   string
	lookup func(context.Context, string, string) ([]netip.Addr, error)
	dial   func(context.Context, string, string) (net.Conn, error)
	run    func(context.Context, string, []string) ([]byte, error)
}

func defaultNetworkProbeExecutor() networkProbeExecutor {
	return networkProbeExecutor{goos: runtime.GOOS, lookup: net.DefaultResolver.LookupNetIP,
		dial: (&net.Dialer{}).DialContext, run: runNetworkTool}
}

func (e networkProbeExecutor) execute(parent context.Context, task check.Task) (result check.Result) {
	result = check.Result{TaskID: task.ID, TargetID: task.TargetID, Kind: task.Kind, Outcome: check.Unobserved, ErrorClass: "internal", AddressFamily: check.FamilyUnknown}
	defer func() { result.CompletedAt = time.Now().UTC() }()
	if task.Validate(time.Now().UTC()) != nil {
		result.ErrorClass = "invalid_task"
		return
	}
	deadline := time.Now().Add(time.Duration(task.TimeoutMS) * time.Millisecond)
	if task.ExpiresAt.Before(deadline) {
		deadline = task.ExpiresAt
	}
	ctx, cancel := context.WithDeadline(parent, deadline)
	defer cancel()
	if parent.Err() != nil {
		result.ErrorClass = "cancelled"
		return
	}
	address, err := e.address(ctx, task)
	if err != nil {
		if parent.Err() != nil {
			result.ErrorClass = "cancelled"
		} else if errors.Is(err, errEvidenceUnsupported) {
			result.ErrorClass = "no_address"
		} else {
			result.ErrorClass = "dns"
		}
		return
	}
	if ctx.Err() != nil {
		if parent.Err() != nil {
			result.ErrorClass = "cancelled"
		}
		return
	}
	family := check.FamilyIPv6
	if address.Is4() {
		family = check.FamilyIPv4
	}
	if task.Kind == v1.TaskKindTCPing {
		started := time.Now()
		network := "tcp6"
		if address.Is4() {
			network = "tcp4"
		}
		connection, dialErr := e.dial(ctx, network, net.JoinHostPort(address.String(), strconv.Itoa(task.Port)))
		if dialErr != nil {
			if parent.Err() != nil {
				result.ErrorClass = "cancelled"
				return
			}
			if probeUnsupported(dialErr) {
				result.ErrorClass = "unsupported"
				return
			}
			result.Outcome, result.AddressFamily = check.Failure, family
			result.ErrorClass = "unreachable"
			var timeout net.Error
			if errors.Is(ctx.Err(), context.DeadlineExceeded) || (errors.As(dialErr, &timeout) && timeout.Timeout()) {
				result.ErrorClass = "timeout"
			}
			if errors.Is(dialErr, syscall.ECONNREFUSED) {
				result.ErrorClass = "refused"
			}
			return
		}
		defer connection.Close()
		remote, ok := connection.RemoteAddr().(*net.TCPAddr)
		if !ok || !remote.AddrPort().Addr().IsValid() || remote.AddrPort().Addr().Unmap() != address {
			return
		}
		rtt := float64(time.Since(started).Microseconds()) / 1000
		result.Outcome, result.ErrorClass, result.AddressFamily, result.RTTMS = check.Success, "", family, &rtt
		return
	}
	program, arguments, err := evidencePingCommand(e.goos, address, task.PacketCount, time.Until(deadline))
	if err != nil {
		result.ErrorClass = "unsupported"
		return
	}
	output, runErr := e.run(ctx, program, arguments)
	if parent.Err() != nil {
		result.ErrorClass = "cancelled"
		return
	}
	if errors.Is(runErr, errEvidenceOutputLimit) {
		return
	}
	text := networkToolText(output, e.goos)
	if probeUnsupported(runErr) || unsupportedPingText(text) {
		result.ErrorClass = "unsupported"
		return
	}
	stats := parsePingEvidence(text, e.goos, task.PacketCount)
	if stats.echoReplies > 0 && (stats.rtt == nil || stats.packets != nil && stats.packets.Received == 0) {
		// An echo without a trustworthy RTT, or contradictory summary, is
		// unavailable evidence; it cannot be turned into a target failure.
		return
	}
	if stats.rtt != nil && stats.echoReplies > 0 {
		result.Outcome, result.ErrorClass, result.AddressFamily, result.RTTMS = check.Success, "", family, stats.rtt
		result.Packets = stats.packets
		return
	}
	if stats.transmitted > 0 {
		result.Outcome, result.ErrorClass, result.AddressFamily = check.Failure, "unreachable", family
		if stats.packets != nil && stats.packets.Received == 0 {
			result.Packets = stats.packets
			result.ErrorClass = "timeout"
		}
		return
	}
	if stats.transmitted == 0 {
		result.ErrorClass = "no_packets_sent"
	}
	return
}

func (e networkProbeExecutor) address(ctx context.Context, task check.Task) (netip.Addr, error) {
	values := []netip.Addr{}
	if literal, err := netip.ParseAddr(task.Host); err == nil {
		values = append(values, literal)
	} else {
		network := "ip"
		if task.AddressFamily == check.FamilyIPv4 {
			network = "ip4"
		}
		if task.AddressFamily == check.FamilyIPv6 {
			network = "ip6"
		}
		var lookupErr error
		values, lookupErr = e.lookup(ctx, network, task.Host)
		if lookupErr != nil {
			return netip.Addr{}, lookupErr
		}
	}
	for _, raw := range values {
		address := raw.Unmap()
		if !address.IsValid() || address.IsUnspecified() || address.IsMulticast() || address.String() == "255.255.255.255" {
			continue
		}
		if task.AddressFamily == check.FamilyIPv4 && !address.Is4() || task.AddressFamily == check.FamilyIPv6 && !address.Is6() {
			continue
		}
		return address, nil
	}
	return netip.Addr{}, errEvidenceUnsupported
}

func probeUnsupported(err error) bool {
	return err != nil && (errors.Is(err, errEvidenceUnsupported) || errors.Is(err, exec.ErrNotFound) || os.IsPermission(err) || errors.Is(err, syscall.EAFNOSUPPORT))
}

func evidencePingCommand(goos string, address netip.Addr, count int, remaining time.Duration) (string, []string, error) {
	if !address.IsValid() || count < 1 || count > check.MaxPackets || remaining <= 0 {
		return "", nil, errEvidenceUnsupported
	}
	family := "-6"
	if address.Is4() {
		family = "-4"
	}
	ms := max(1, int(remaining.Milliseconds()))
	if goos == "windows" {
		return "ping", []string{family, "-n", strconv.Itoa(count), "-w", strconv.Itoa(ms), address.String()}, nil
	}
	if goos == "darwin" {
		program := "ping"
		if address.Is6() {
			program = "ping6"
		}
		return program, []string{"-n", "-c", strconv.Itoa(count), "-i", "0.25", "-W", strconv.Itoa(ms), address.String()}, nil
	}
	if goos != "linux" {
		return "", nil, errEvidenceUnsupported
	}
	args := []string{family, "-n", "-c", strconv.Itoa(count), "-i", "0.25", "-W", strconv.FormatFloat(float64(max(1, ms-100))/1000, 'f', 3, 64)}
	// Leave time for the tool to write its settled packet summary before the
	// total context deadline. Shorter budgets remain bounded by CommandContext.
	if seconds := (ms - 100) / 1000; seconds >= 1 {
		args = append(args, "-w", strconv.Itoa(seconds))
	}
	return "ping", append(args, address.String()), nil
}

type evidenceOutput struct {
	mu       sync.Mutex
	data     []byte
	overflow bool
}

func (b *evidenceOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := maxEvidenceOutput - len(b.data)
	b.data = append(b.data, p[:min(len(p), remaining)]...)
	if len(p) > remaining {
		b.overflow = true
	}
	return len(p), nil
}
func runNetworkTool(ctx context.Context, program string, args []string) ([]byte, error) {
	command := exec.CommandContext(ctx, program, args...)
	command.WaitDelay = time.Second
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "LC_ALL=") && !strings.HasPrefix(env, "LANG=") && !strings.HasPrefix(env, "LANGUAGE=") && !strings.HasPrefix(env, "IPUTILS_PING_PTR_LOOKUP=") {
			command.Env = append(command.Env, env)
		}
	}
	command.Env = append(command.Env, "LC_ALL=C", "LANG=C", "IPUTILS_PING_PTR_LOOKUP=0")
	output := new(evidenceOutput)
	command.Stdout, command.Stderr = output, output
	err := command.Run()
	if output.overflow {
		return nil, errEvidenceOutputLimit
	}
	return output.data, err
}

func finiteRTT(value string) *float64 {
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || number < 0 || number > 60000 {
		return nil
	}
	return &number
}
