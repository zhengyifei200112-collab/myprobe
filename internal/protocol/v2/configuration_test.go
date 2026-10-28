package v2

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	v1 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

func TestConfigurationExtensionRequiresExplicitCapabilityAndValidPayload(t *testing.T) {
	if AllowsConfiguration(NegotiateCapabilities([]string{ConfigurationCapability}, nil)) {
		t.Fatal("missing server capability accepted")
	}
	if AllowsConfiguration(NegotiateCapabilities([]string{"unknown"}, []string{ConfigurationCapability})) {
		t.Fatal("unknown capability implied permission")
	}
	if !AllowsConfiguration(NegotiateCapabilities([]string{ConfigurationCapability}, []string{ConfigurationCapability})) {
		t.Fatal("common capability missing")
	}
	good := RuntimeConfiguration{Scope: SamplingConfigurationScope, Revision: 1, Nonce: strings.Repeat("a", 32), CollectionSeconds: 1, ReportSeconds: 5}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	envelope, err := NewEnvelope(TypeConfigurationApplied, 1, ConfigurationApplied(good))
	if err != nil || envelope.Validate(time.Now()) != nil {
		t.Fatal("valid v2 acknowledgement rejected")
	}
	legacy := v1.Envelope(envelope)
	legacy.Version = 1
	if legacy.Validate(time.Now()) == nil {
		t.Fatal("new extension accepted as v1")
	}
	raw, _ := json.Marshal(Welcome{Capabilities: []string{ConfigurationCapability}, RuntimeConfig: &good})
	var old Welcome
	if err = json.Unmarshal(raw, &old); err != nil || old.RuntimeConfig == nil {
		t.Fatal("welcome extension failed")
	}
	for _, mutate := range []func(*RuntimeConfiguration){func(c *RuntimeConfiguration) { c.Scope = "remote_command" }, func(c *RuntimeConfiguration) { c.Revision = 0 }, func(c *RuntimeConfiguration) { c.Nonce = "private arbitrary value" }, func(c *RuntimeConfiguration) { c.CollectionSeconds = 0 }, func(c *RuntimeConfiguration) { c.ReportSeconds = 3601 }} {
		bad := good
		mutate(&bad)
		if bad.Validate() == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
}
