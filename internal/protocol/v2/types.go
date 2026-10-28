// Package v2 defines explicitly negotiated optional wire extensions. Transport
// must negotiate this version separately; v1 messages remain version 1.
package v2

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/networkcheck"
	v1 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

const (
	Version           = 2
	MaxMessageBytes   = v1.MaxMessageBytes
	TypeHTTPTask      = "http_task"
	TypeHTTPResult    = "http_result"
	TypeNetworkTask   = "network_task"
	TypeNetworkResult = "network_result"
)

type Envelope v1.Envelope

func NewEnvelope(kind string, sequence uint64, payload any) (Envelope, error) {
	base, err := v1.NewEnvelope(kind, sequence, payload)
	base.Version = Version
	return Envelope(base), err
}

// Validate checks only the outer envelope. Decode and validate the corresponding
// payload, authenticated node and negotiated capabilities before processing it.
func (e Envelope) Validate(now time.Time) error {
	if e.Version != Version {
		return v1.ErrUnsupportedVersion
	}
	base := v1.Envelope(e)
	base.Version = v1.Version
	if e.Type == TypeHTTPTask || e.Type == TypeHTTPResult || e.Type == TypeConfigurationApplied || e.Type == TypeNetworkTask || e.Type == TypeNetworkResult {
		base.Type = v1.TypeTask
	}
	if err := base.Validate(now); err != nil {
		return err
	}
	if len(e.Payload) > MaxMessageBytes || !json.Valid(e.Payload) {
		return errors.New("invalid v2 payload")
	}
	return nil
}

func DecodePayload[T any](e Envelope) (T, error) { return v1.DecodePayload[T](v1.Envelope(e)) }

type Welcome struct {
	v1.Welcome
	Capabilities  []string              `json:"capabilities"`
	RuntimeConfig *RuntimeConfiguration `json:"runtime_config,omitempty"`
}

// NegotiateCapabilities returns only explicitly supported common extensions.
// Unknown future capabilities do not imply permission to send new message kinds.
func NegotiateCapabilities(agent, server []string) []string {
	result := make([]string, 0, 3)
	contains := func(values []string, want string) bool {
		for _, value := range values {
			if value == want {
				return true
			}
		}
		return false
	}
	if contains(agent, httpcheck.Capability) && contains(server, httpcheck.Capability) {
		result = append(result, httpcheck.Capability)
	}
	if contains(agent, ConfigurationCapability) && contains(server, ConfigurationCapability) {
		result = append(result, ConfigurationCapability)
	}
	if contains(agent, networkcheck.Capability) && contains(server, networkcheck.Capability) {
		result = append(result, networkcheck.Capability)
	}
	return result
}

func AllowsNetwork(capabilities []string) bool {
	for _, value := range capabilities {
		if value == networkcheck.Capability {
			return true
		}
	}
	return false
}

func AllowsHTTP(capabilities []string) bool {
	for _, value := range capabilities {
		if value == httpcheck.Capability {
			return true
		}
	}
	return false
}
