package agentclient

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

type jointServer struct {
	binary, database string
	env              []string
	cmd              *exec.Cmd
	done             chan error
	stopCommand      context.CancelFunc
	unit, cgroup     string
	pid              int
	limits           map[string]string
}

func (s *jointServer) start(t *testing.T, ctx context.Context, base string) {
	t.Helper()
	childCtx, stop := context.WithCancel(ctx)
	s.stopCommand = stop
	s.limits = map[string]string{"mode": "unrestricted"}
	if os.Getenv("MYPROBE_TEST_LOAD_SYSTEMD") == "1" {
		if runtime.GOOS != "linux" {
			t.Fatal("systemd resource control requires Linux")
		}
		s.unit = "myprobe-joint-load-" + strings.ToLower(rand.Text())
		// A transient unit contains only this Server. Agents, receiver, sampler and
		// subscriptions stay outside it. No global service configuration is changed.
		args := []string{"--quiet", "--unit=" + s.unit, "--property=MemoryMax=2G", "--property=MemorySwapMax=0", "--property=CPUQuota=200%", "--property=AllowedCPUs=0,1", "--wait", "--collect", "--pipe"}
		if os.Getenv("MYPROBE_TEST_LOAD_NETWORK_NAMESPACE") == "1" {
			args = append(args, fmt.Sprintf("--property=NetworkNamespacePath=/proc/%d/ns/net", os.Getpid()))
		}
		for _, env := range s.env {
			args = append(args, "--setenv="+env)
		}
		args = append(args, s.binary)
		s.cmd = exec.CommandContext(childCtx, "systemd-run", args...)
	} else {
		s.cmd = exec.CommandContext(childCtx, s.binary)
	}
	// Do not inherit the operator's MyProbe production configuration into a fixture.
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "MYPROBE_") {
			s.cmd.Env = append(s.cmd.Env, env)
		}
	}
	s.cmd.Env = append(s.cmd.Env, s.env...)
	s.cmd.Stdout, s.cmd.Stderr = io.Discard, io.Discard
	if err := s.cmd.Start(); err != nil {
		stop()
		t.Fatalf("start disposable Server: %v", err)
	}
	s.done = make(chan error, 1)
	go func(cmd *exec.Cmd, done chan error) { done <- cmd.Wait() }(s.cmd, s.done)
	s.pid = s.cmd.Process.Pid
	client := &http.Client{Timeout: time.Second}
	ready := false
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline) && ctx.Err() == nil; {
		response, err := client.Get(base + "/healthz")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == 200 {
				ready = true
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ready {
		s.stop()
		t.Fatal("disposable Server did not become ready")
	}
	if s.unit != "" {
		pid, err := jointSystemctl(ctx, "show", s.unit, "--property=MainPID", "--value")
		if err != nil {
			s.stop()
			t.Fatal("cannot observe limited Server PID")
		}
		s.pid, err = strconv.Atoi(strings.TrimSpace(pid))
		if err != nil || s.pid <= 0 {
			s.stop()
			t.Fatal("limited Server PID missing")
		}
		group, err := jointSystemctl(ctx, "show", s.unit, "--property=ControlGroup", "--value")
		if err != nil {
			s.stop()
			t.Fatal("cannot observe Server cgroup")
		}
		s.cgroup = filepath.Join("/sys/fs/cgroup", strings.TrimSpace(group))
		s.limits["mode"] = "systemd-cgroup-v2"
		for _, name := range []string{"cpu.max", "memory.max", "memory.swap.max", "cpuset.cpus.effective"} {
			raw, err := os.ReadFile(filepath.Join(s.cgroup, name))
			if err != nil {
				s.stop()
				t.Fatalf("resource control %s missing", name)
			}
			s.limits[name] = strings.TrimSpace(string(raw))
		}
		if s.limits["cpu.max"] != "200000 100000" || s.limits["memory.max"] != "2147483648" || s.limits["memory.swap.max"] != "0" || s.limits["cpuset.cpus.effective"] != "0-1" {
			s.stop()
			t.Fatal("requested CPU/memory/swap/cpuset controls did not take effect")
		}
	}
}

func (s *jointServer) stop() {
	if s.cmd == nil {
		return
	}
	if s.unit != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		_, _ = jointSystemctl(ctx, "stop", s.unit)
		cancel()
	} else if runtime.GOOS == "windows" {
		_ = s.cmd.Process.Kill()
	} else {
		_ = s.cmd.Process.Signal(syscall.SIGTERM)
	}
	select {
	case <-s.done:
	case <-time.After(20 * time.Second):
		s.stopCommand()
		_ = s.cmd.Process.Kill()
		<-s.done
	}
	s.stopCommand()
	s.cmd = nil
}

func jointSystemctl(ctx context.Context, args ...string) (string, error) {
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	raw, err := exec.CommandContext(bounded, "systemctl", args...).Output()
	return string(raw), err
}

func (s *jointServer) rssBytes() int64 {
	if runtime.GOOS != "linux" || s.pid <= 0 {
		return 0
	}
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", s.pid))
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == "VmRSS:" {
			value, _ := strconv.ParseInt(fields[1], 10, 64)
			return value * 1024
		}
	}
	return 0
}
func (s *jointServer) cpuMicros() uint64 {
	if s.cgroup == "" {
		return 0
	}
	raw, err := os.ReadFile(filepath.Join(s.cgroup, "cpu.stat"))
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "usage_usec" {
			value, _ := strconv.ParseUint(fields[1], 10, 64)
			return value
		}
	}
	return 0
}
