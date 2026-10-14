package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentclient"
	"github.com/zhengyifei200112-collab/myprobe/internal/collector"
)

var version = "dev"

func main() {
	serverURL := flag.String("server", env("MYPROBE_SERVER", ""), "MyProbe server URL")
	token := flag.String("token", env("MYPROBE_TOKEN", ""), "agent authentication token")
	collection := flag.Duration("collection-interval", 5*time.Second, "host metric collection interval")
	report := flag.Duration("report-interval", 5*time.Second, "metric report interval")
	interfaces := flag.String("interfaces", "", "comma-separated network interfaces; empty selects all non-loopback interfaces")
	mounts := flag.String("mounts", "", "comma-separated disk mount points; empty discovers physical mounts")
	hostRoot := flag.String("host-root", env("MYPROBE_HOST_ROOT", ""), "optional host filesystem prefix used by containerized agents")
	httpPrivate := flag.String("http-private-cidrs", env("MYPROBE_HTTP_PRIVATE_CIDRS", ""), "comma-separated private CIDRs allowed for HTTP probes")
	httpPorts := flag.String("http-additional-ports", env("MYPROBE_HTTP_ADDITIONAL_PORTS", ""), "comma-separated HTTP probe ports in addition to 80 and 443")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	privateCIDRs, ports, err := parseHTTPPolicy(*httpPrivate, *httpPorts)
	if err != nil {
		logger.Error("invalid agent configuration", "error", err)
		os.Exit(2)
	}
	source := collector.New(collector.Config{Interfaces: split(*interfaces), Mounts: split(*mounts), HostRoot: *hostRoot})
	client, err := agentclient.New(agentclient.Config{
		ServerURL: *serverURL, Token: *token, CollectionPeriod: *collection,
		ReportPeriod: *report, AgentVersion: version,
		HTTPPrivateCIDRs: privateCIDRs, HTTPAdditionalPorts: ports,
	}, source, logger)
	if err != nil {
		logger.Error("invalid agent configuration", "error", err)
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	logger.Info("MyProbe agent started", "version", version, "server", *serverURL)
	if err := client.Run(ctx); err != nil {
		logger.Error("agent stopped", "error", err)
		os.Exit(1)
	}
}

// Keep malformed list entries visible to validation instead of silently dropping
// them. Errors deliberately exclude deployment-specific addresses and values.
func parseHTTPPolicy(cidrs, rawPorts string) ([]string, []int, error) {
	parseList := func(raw string) ([]string, error) {
		if strings.TrimSpace(raw) == "" {
			return nil, nil
		}
		values := strings.Split(raw, ",")
		if len(values) > 64 {
			return nil, errors.New("HTTP policy has too many entries")
		}
		for i := range values {
			values[i] = strings.TrimSpace(values[i])
			if values[i] == "" {
				return nil, errors.New("HTTP policy contains an empty entry")
			}
		}
		return values, nil
	}
	private, err := parseList(cidrs)
	if err != nil {
		return nil, nil, err
	}
	values, err := parseList(rawPorts)
	if err != nil {
		return nil, nil, err
	}
	var ports []int
	for _, value := range values {
		port, err := strconv.ParseUint(value, 10, 16)
		if err != nil || port == 0 {
			return nil, nil, errors.New("invalid additional HTTP port")
		}
		ports = append(ports, int(port))
	}
	return private, ports, nil
}

func split(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
