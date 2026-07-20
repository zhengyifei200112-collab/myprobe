package v1

import (
	"errors"
	"fmt"
	"time"
)

var (
	ErrUnsupportedVersion = errors.New("unsupported protocol version")
	ErrInvalidMessageType = errors.New("invalid message type")
	ErrInvalidTimestamp   = errors.New("invalid timestamp")
)

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
