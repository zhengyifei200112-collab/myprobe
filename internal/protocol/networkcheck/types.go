// Package networkcheck defines bounded, mutually negotiated network probes.
// Addresses remain private task inputs; results contain only a family enum.
package networkcheck

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/netip"
	"strings"
	"time"
	"unicode"

	v1 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

const (
	Capability       = "network_probe.v1"
	FamilyAuto       = "auto"
	FamilyIPv4       = "ipv4"
	FamilyIPv6       = "ipv6"
	FamilyUnknown    = "unknown"
	Success          = "success"
	Failure          = "failure"
	Unobserved       = "unobserved"
	MaxPackets       = 5
	PacketIntervalMS = 250
)

var ErrInvalid = errors.New("invalid network probe evidence")

// PacketCount is explicit for ping (1..5), absent/zero for TCP. TimeoutMS bounds
// the whole task, including the packet train; it is not a per-packet allowance.
type Task struct {
	v1.Task
	AddressFamily string `json:"address_family"`
	PacketCount   int    `json:"packet_count,omitempty"`
}

func (t Task) Validate(now time.Time) error {
	if t.Task.Validate(now) != nil || !identifier(t.ID) || !identifier(t.TargetID) || strings.TrimSpace(t.Host) != t.Host {
		return ErrInvalid
	}
	if t.AddressFamily != FamilyAuto && t.AddressFamily != FamilyIPv4 && t.AddressFamily != FamilyIPv6 {
		return ErrInvalid
	}
	if ip, err := netip.ParseAddr(t.Host); err == nil {
		family := FamilyIPv6
		if ip.Unmap().Is4() {
			family = FamilyIPv4
		}
		if t.AddressFamily != FamilyAuto && t.AddressFamily != family {
			return ErrInvalid
		}
	}
	if t.Kind == v1.TaskKindPing {
		if t.Port != 0 || t.PacketCount < 1 || t.PacketCount > MaxPackets || (t.PacketCount-1)*PacketIntervalMS >= t.TimeoutMS {
			return ErrInvalid
		}
	} else if t.PacketCount != 0 {
		return ErrInvalid
	}
	return nil
}

// Counts are optional as a whole. A missing, interrupted or unreliable tool
// summary is nil, never reconstructed from an outcome, error class or hostname.
type PacketCounts struct {
	Sent     int `json:"sent"`
	Received int `json:"received"`
}

func (p *PacketCounts) UnmarshalJSON(raw []byte) error {
	var fields struct {
		Sent     *int `json:"sent"`
		Received *int `json:"received"`
	}
	if decode(raw, &fields) != nil || fields.Sent == nil || fields.Received == nil {
		return ErrInvalid
	}
	p.Sent, p.Received = *fields.Sent, *fields.Received
	return nil
}

type Result struct {
	TaskID        string        `json:"task_id"`
	TargetID      string        `json:"target_id"`
	Kind          string        `json:"kind"`
	Outcome       string        `json:"outcome"`
	ErrorClass    string        `json:"error_class,omitempty"`
	AddressFamily string        `json:"address_family"`
	RTTMS         *float64      `json:"rtt_ms"`
	Packets       *PacketCounts `json:"packets"`
	CompletedAt   time.Time     `json:"completed_at"`
}

func (r Result) Validate(now time.Time) error {
	if !identifier(r.TaskID) || !identifier(r.TargetID) || (r.Kind != v1.TaskKindPing && r.Kind != v1.TaskKindTCPing) ||
		r.CompletedAt.IsZero() || r.CompletedAt.Before(now.Add(-10*time.Minute)) || r.CompletedAt.After(now.Add(time.Minute)) {
		return ErrInvalid
	}
	if r.AddressFamily != FamilyUnknown && r.AddressFamily != FamilyIPv4 && r.AddressFamily != FamilyIPv6 {
		return ErrInvalid
	}
	if r.RTTMS != nil && (math.IsNaN(*r.RTTMS) || math.IsInf(*r.RTTMS, 0) || *r.RTTMS < 0 || *r.RTTMS > 60000) {
		return ErrInvalid
	}
	if r.Packets != nil {
		if r.Kind != v1.TaskKindPing || r.Packets.Sent < 0 || r.Packets.Sent > MaxPackets || r.Packets.Received < 0 || r.Packets.Received > r.Packets.Sent {
			return ErrInvalid
		}
	}
	switch r.Outcome {
	case Success:
		if r.RTTMS == nil || r.ErrorClass != "" || r.AddressFamily == FamilyUnknown || (r.Packets != nil && r.Packets.Received < 1) {
			return ErrInvalid
		}
	case Failure:
		if r.RTTMS != nil || r.AddressFamily == FamilyUnknown || (r.ErrorClass != "timeout" && r.ErrorClass != "refused" && r.ErrorClass != "unreachable") ||
			(r.Packets != nil && (r.Packets.Sent < 1 || r.Packets.Received != 0)) {
			return ErrInvalid
		}
	case Unobserved:
		if r.RTTMS != nil || r.Packets != nil || r.AddressFamily != FamilyUnknown {
			return ErrInvalid
		}
		switch r.ErrorClass {
		case "unsupported", "busy", "dns", "invalid_task", "cancelled", "no_address", "no_packets_sent", "send_error", "internal":
		default:
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

// The receiver must additionally bind this result to the authenticated node's
// durable task. This function cannot authorize a task or validate its lease.
func (r Result) ValidateAgainst(t Task, now time.Time) error {
	// A dispatched task may already have expired when its result is decoded;
	// check its definition at expiry, leaving receive deadlines to the ledger.
	if t.Validate(t.ExpiresAt) != nil || r.Validate(now) != nil || r.TaskID != t.ID || r.TargetID != t.TargetID || r.Kind != t.Kind {
		return ErrInvalid
	}
	if r.AddressFamily != FamilyUnknown && t.AddressFamily != FamilyAuto && r.AddressFamily != t.AddressFamily {
		return ErrInvalid
	}
	if r.Packets != nil && r.Packets.Sent > t.PacketCount {
		return ErrInvalid
	}
	return nil
}

func DecodeTask(raw []byte, now time.Time) (Task, error) {
	var value Task
	if decode(raw, &value) != nil || value.Validate(now) != nil {
		return Task{}, ErrInvalid
	}
	return value, nil
}
func DecodeResult(raw []byte, now time.Time) (Result, error) {
	var value Result
	if decode(raw, &value) != nil || value.Validate(now) != nil {
		return Result{}, ErrInvalid
	}
	return value, nil
}
func decode(raw []byte, value any) error {
	if len(raw) == 0 || len(raw) > v1.MaxMessageBytes {
		return ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return ErrInvalid
	}
	if decoder.Decode(new(any)) != io.EOF {
		return ErrInvalid
	}
	return nil
}
func identifier(value string) bool {
	return value != "" && len(value) <= 128 && strings.TrimSpace(value) == value && strings.IndexFunc(value, unicode.IsControl) < 0
}
