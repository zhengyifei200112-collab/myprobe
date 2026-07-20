package v1

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"
)

var (
	ErrUnsupportedVersion = errors.New("unsupported protocol version")
	ErrInvalidMessageType = errors.New("invalid message type")
	ErrInvalidTimestamp   = errors.New("invalid timestamp")
)

var hostnamePattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)

func (e Envelope) Validate(now time.Time) error {
	if e.Version != Version {
		return ErrUnsupportedVersion
	}
	if !isKnownType(e.Type) {
		return ErrInvalidMessageType
	}
	if e.SentAt.IsZero() || e.SentAt.After(now.Add(5*time.Minute)) {
		return ErrInvalidTimestamp
	}
	return nil
}

func (r Report) Validate() error {
	if r.CapturedAt.IsZero() {
		return fmt.Errorf("captured_at: %w", ErrInvalidTimestamp)
	}
	if err := validPercent("cpu.usage_percent", r.CPU.UsagePercent); err != nil {
		return err
	}
	if err := validateMemory("memory", r.Memory); err != nil {
		return err
	}
	if err := validateMemory("swap", r.Swap); err != nil {
		return err
	}
	for index, disk := range r.Disks {
		if disk.Mount == "" {
			return fmt.Errorf("disks[%d].mount is required", index)
		}
		if disk.UsedBytes > disk.TotalBytes {
			return fmt.Errorf("disks[%d].used_bytes exceeds total_bytes", index)
		}
		if err := validPercent(fmt.Sprintf("disks[%d].usage_percent", index), disk.UsagePercent); err != nil {
			return err
		}
	}
	for index, network := range r.Networks {
		if network.Interface == "" {
			return fmt.Errorf("networks[%d].interface is required", index)
		}
		if network.RXBytesPerS < 0 || network.TXBytesPerS < 0 {
			return fmt.Errorf("networks[%d] rate must not be negative", index)
		}
	}
	return nil
}

func (t Task) Validate(now time.Time) error {
	if strings.TrimSpace(t.ID) == "" || len(t.ID) > 128 || strings.TrimSpace(t.TargetID) == "" || len(t.TargetID) > 128 {
		return errors.New("task id and target id are required")
	}
	if t.Kind != TaskKindPing && t.Kind != TaskKindTCPing {
		return errors.New("task kind must be ping or tcping")
	}
	if !validProbeHost(t.Host) {
		return errors.New("task host is invalid")
	}
	if t.Kind == TaskKindTCPing && (t.Port < 1 || t.Port > 65535) {
		return errors.New("tcping port must be between 1 and 65535")
	}
	if t.TimeoutMS < 100 || t.TimeoutMS > 60000 {
		return errors.New("task timeout must be between 100 and 60000 milliseconds")
	}
	if t.ExpiresAt.IsZero() || t.ExpiresAt.Before(now) || t.ExpiresAt.After(now.Add(10*time.Minute)) {
		return errors.New("task expiry is invalid")
	}
	return nil
}

func (r LatencyResult) Validate(now time.Time) error {
	if strings.TrimSpace(r.TaskID) == "" || len(r.TaskID) > 128 || strings.TrimSpace(r.TargetID) == "" || len(r.TargetID) > 128 {
		return errors.New("task id and target id are required")
	}
	if r.CompletedAt.IsZero() || r.CompletedAt.Before(now.Add(-10*time.Minute)) || r.CompletedAt.After(now.Add(time.Minute)) {
		return errors.New("completion time is invalid")
	}
	if r.Success && (r.LatencyMS < 0 || r.LatencyMS > 60000 || r.ErrorClass != "") {
		return errors.New("successful latency result is invalid")
	}
	if !r.Success && strings.TrimSpace(r.ErrorClass) == "" {
		return errors.New("failed latency result requires an error class")
	}
	if !r.Success && !validProbeErrorClass(r.ErrorClass) {
		return errors.New("failed latency result has an invalid error class")
	}
	return nil
}

func validProbeErrorClass(value string) bool {
	switch value {
	case "timeout", "dns", "unsupported", "refused", "unreachable", "invalid_task", "busy":
		return true
	default:
		return false
	}
}

func validProbeHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" || len(host) > 253 {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	return hostnamePattern.MatchString(host) && !strings.Contains(host, "..")
}

func validateMemory(name string, metric MemoryMetric) error {
	if metric.UsedBytes > metric.TotalBytes {
		return fmt.Errorf("%s.used_bytes exceeds total_bytes", name)
	}
	return validPercent(name+".usage_percent", metric.UsagePercent)
}

func validPercent(name string, value float64) error {
	if value < 0 || value > 100 {
		return fmt.Errorf("%s must be between 0 and 100", name)
	}
	return nil
}

func isKnownType(messageType string) bool {
	switch messageType {
	case TypeHello, TypeReport, TypePingResult, TypeTCPingResult, TypeHeartbeat,
		TypeWelcome, TypeAcknowledged, TypeConfiguration, TypeTask, TypeError:
		return true
	default:
		return false
	}
}
