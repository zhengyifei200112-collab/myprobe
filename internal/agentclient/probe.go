package agentclient

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

var pingLatencyPattern = regexp.MustCompile(`(?i)(?:time|时间)\s*[=<]\s*([0-9]+(?:\.[0-9]+)?)\s*ms`)

func executeTask(parent context.Context, task protocol.Task) protocol.LatencyResult {
	result := protocol.LatencyResult{TaskID: task.ID, TargetID: task.TargetID}
	if err := task.Validate(time.Now().UTC()); err != nil {
		result.ErrorClass = "invalid_task"
		result.CompletedAt = time.Now().UTC()
		return result
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(task.TimeoutMS)*time.Millisecond)
	defer cancel()

	started := time.Now()
	var latency float64
	var err error
	if task.Kind == protocol.TaskKindTCPing {
		latency, err = probeTCP(ctx, task.Host, task.Port)
	} else {
		latency, err = probePing(ctx, task.Host, task.TimeoutMS, started)
	}
	result.CompletedAt = time.Now().UTC()
	if err != nil {
		result.ErrorClass = classifyProbeError(ctx, err)
		return result
	}
	result.Success = true
	result.LatencyMS = latency
	return result
}

func probeTCP(ctx context.Context, host string, port int) (float64, error) {
	started := time.Now()
	connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return 0, err
	}
	_ = connection.Close()
	return float64(time.Since(started).Microseconds()) / 1000, nil
}

func probePing(ctx context.Context, host string, timeoutMS int, started time.Time) (float64, error) {
	program, arguments := pingCommand(host, timeoutMS)
	output, err := exec.CommandContext(ctx, program, arguments...).CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("ping: %w", err)
	}
	if match := pingLatencyPattern.FindStringSubmatch(string(output)); len(match) == 2 {
		if value, parseErr := strconv.ParseFloat(match[1], 64); parseErr == nil {
			return value, nil
		}
	}
	return float64(time.Since(started).Microseconds()) / 1000, nil
}

func pingCommand(host string, timeoutMS int) (string, []string) {
	return pingCommandForOS(runtime.GOOS, host, timeoutMS)
}

func pingCommandForOS(goos, host string, timeoutMS int) (string, []string) {
	if goos == "windows" {
		return "ping", []string{"-n", "1", "-w", strconv.Itoa(timeoutMS), host}
	}
	if goos == "darwin" {
		return "ping", []string{"-c", "1", "-W", strconv.Itoa(timeoutMS), host}
	}
	seconds := max(1, (timeoutMS+999)/1000)
	return "ping", []string{"-c", "1", "-W", strconv.Itoa(seconds), host}
}

func classifyProbeError(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var dnsError *net.DNSError
	if errors.As(err, &dnsError) {
		return "dns"
	}
	var executableError *exec.Error
	if errors.As(err, &executableError) {
		return "unsupported"
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "refused") {
		return "refused"
	}
	return "unreachable"
}
