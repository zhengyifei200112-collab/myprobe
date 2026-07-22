package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
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
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	source := collector.New(collector.Config{Interfaces: split(*interfaces), Mounts: split(*mounts)})
	client, err := agentclient.New(agentclient.Config{
		ServerURL: *serverURL, Token: *token, CollectionPeriod: *collection,
		ReportPeriod: *report, AgentVersion: version,
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
