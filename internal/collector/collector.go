package collector

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	gopsutilnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/sensors"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

type Config struct {
	Interfaces []string
	Mounts     []string
	HostRoot   string
}

type networkSnapshot struct {
	rx uint64
	tx uint64
	at time.Time
}

type Collector struct {
	mu       sync.Mutex
	config   Config
	previous map[string]networkSnapshot
}

func New(config Config) *Collector {
	return &Collector{config: normalizedConfig(config), previous: make(map[string]networkSnapshot)}
}

func (c *Collector) UpdateConfig(config Config) {
	c.mu.Lock()
	hostRoot := c.config.HostRoot
	c.config = normalizedConfig(config)
	c.config.HostRoot = hostRoot
	c.mu.Unlock()
}

func (c *Collector) Hello(ctx context.Context, version string, collectionSeconds, reportSeconds int) (protocol.Hello, error) {
	info, err := host.InfoWithContext(ctx)
	if err != nil {
		return protocol.Hello{}, err
	}
	return protocol.Hello{
		AgentVersion: version, Hostname: info.Hostname, MachineID: info.HostID,
		OS: runtime.GOOS, Platform: info.Platform, PlatformVersion: info.PlatformVersion,
		KernelVersion: info.KernelVersion, Architecture: runtime.GOARCH,
		Capabilities:      []string{"metrics.v1", "ping.v1", "tcping.v1"},
		CollectionSeconds: collectionSeconds, ReportSeconds: reportSeconds,
	}, nil
}

func (c *Collector) Collect(ctx context.Context) (protocol.Report, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now().UTC()
	report := protocol.Report{CapturedAt: now}

	info, err := host.InfoWithContext(ctx)
	if err != nil {
		return protocol.Report{}, err
	}
	report.Uptime = info.Uptime
	report.Processes = int(info.Procs)

	percent, err := cpu.PercentWithContext(ctx, 0, false)
	if err != nil || len(percent) == 0 {
		return protocol.Report{}, errors.New("collect CPU usage")
	}
	logical, _ := cpu.CountsWithContext(ctx, true)
	model := ""
	if details, detailErr := cpu.InfoWithContext(ctx); detailErr == nil && len(details) > 0 {
		model = strings.TrimSpace(details[0].ModelName)
	}
	report.CPU = protocol.CPUMetric{Model: model, LogicalCores: logical, Architecture: runtime.GOARCH, UsagePercent: clampPercent(percent[0])}

	memory, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return protocol.Report{}, err
	}
	report.Memory = protocol.MemoryMetric{TotalBytes: memory.Total, UsedBytes: memory.Used, UsagePercent: clampPercent(memory.UsedPercent)}
	if swap, swapErr := mem.SwapMemoryWithContext(ctx); swapErr == nil {
		report.Swap = protocol.MemoryMetric{TotalBytes: swap.Total, UsedBytes: swap.Used, UsagePercent: clampPercent(swap.UsedPercent)}
	}

	if average, loadErr := load.AvgWithContext(ctx); loadErr == nil {
		report.Load = protocol.LoadMetric{One: average.Load1, Five: average.Load5, Fifteen: average.Load15}
	}

	report.Disks = c.collectDisks(ctx)
	report.Networks = c.collectNetworks(ctx, now)
	if temperatures, temperatureErr := sensors.SensorsTemperatures(); temperatureErr == nil {
		for _, sensor := range temperatures {
			if sensor.Temperature > -100 && sensor.Temperature < 250 {
				report.Temperatures = append(report.Temperatures, protocol.TemperatureMetric{Sensor: sensor.SensorKey, Celsius: sensor.Temperature})
			}
		}
	}
	return report, report.Validate()
}

func (c *Collector) collectDisks(ctx context.Context) []protocol.DiskMetric {
	mounts := append([]string(nil), c.config.Mounts...)
	if len(mounts) == 0 {
		if partitions, err := disk.PartitionsWithContext(ctx, false); err == nil {
			seen := make(map[string]bool)
			for _, partition := range partitions {
				if partition.Mountpoint != "" && !seen[partition.Mountpoint] {
					seen[partition.Mountpoint] = true
					mounts = append(mounts, partition.Mountpoint)
				}
			}
		}
	}
	metrics := make([]protocol.DiskMetric, 0, len(mounts))
	for _, mount := range mounts {
		usage, err := disk.UsageWithContext(ctx, diskUsagePath(c.config.HostRoot, mount))
		if err != nil || usage.Total == 0 {
			continue
		}
		metrics = append(metrics, protocol.DiskMetric{
			Mount: mount, Filesystem: usage.Fstype, TotalBytes: usage.Total,
			UsedBytes: usage.Used, UsagePercent: clampPercent(usage.UsedPercent),
		})
	}
	sort.Slice(metrics, func(i, j int) bool { return metrics[i].Mount < metrics[j].Mount })
	return metrics
}

func (c *Collector) collectNetworks(ctx context.Context, now time.Time) []protocol.NetworkMetric {
	counters, err := gopsutilnet.IOCountersWithContext(ctx, true)
	if err != nil {
		return nil
	}
	allowed := make(map[string]bool, len(c.config.Interfaces))
	for _, name := range c.config.Interfaces {
		allowed[name] = true
	}
	metrics := make([]protocol.NetworkMetric, 0, len(counters))
	for _, counter := range counters {
		if counter.Name == "lo" || (len(allowed) > 0 && !allowed[counter.Name]) {
			continue
		}
		metric := protocol.NetworkMetric{Interface: counter.Name, RXTotalBytes: counter.BytesRecv, TXTotalBytes: counter.BytesSent}
		if previous, ok := c.previous[counter.Name]; ok {
			elapsed := now.Sub(previous.at).Seconds()
			if elapsed > 0 {
				if counter.BytesRecv >= previous.rx {
					metric.RXBytesPerS = float64(counter.BytesRecv-previous.rx) / elapsed
				}
				if counter.BytesSent >= previous.tx {
					metric.TXBytesPerS = float64(counter.BytesSent-previous.tx) / elapsed
				}
			}
		}
		c.previous[counter.Name] = networkSnapshot{rx: counter.BytesRecv, tx: counter.BytesSent, at: now}
		metrics = append(metrics, metric)
	}
	sort.Slice(metrics, func(i, j int) bool { return metrics[i].Interface < metrics[j].Interface })
	return metrics
}

func normalizedConfig(config Config) Config {
	hostRoot := strings.TrimSpace(config.HostRoot)
	if hostRoot != "" {
		hostRoot = filepath.Clean(hostRoot)
	}
	return Config{Interfaces: cleanUnique(config.Interfaces), Mounts: cleanUnique(config.Mounts), HostRoot: hostRoot}
}

func diskUsagePath(hostRoot, mount string) string {
	if !filepath.IsAbs(hostRoot) || !filepath.IsAbs(mount) {
		return mount
	}
	volume := filepath.VolumeName(mount)
	relative := strings.TrimLeft(mount[len(volume):], `/\\`)
	return filepath.Join(hostRoot, relative)
}

func cleanUnique(values []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}
