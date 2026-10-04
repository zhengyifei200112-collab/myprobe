package v2

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
	v1 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

func TestVersionIsolationAndEnvelopeRoundTrip(t *testing.T) {
	e, err := NewEnvelope(TypeHTTPTask, 7, map[string]string{"id": "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Validate(time.Now()); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(v1.Envelope(e).Validate(time.Now()), v1.ErrUnsupportedVersion) {
		t.Fatal("v1 accepted v2")
	}
	old, _ := v1.NewEnvelope(v1.TypeHeartbeat, 1, struct{}{})
	if !errors.Is(Envelope(old).Validate(time.Now()), v1.ErrUnsupportedVersion) {
		t.Fatal("v2 silently accepted v1")
	}
	if err := old.Validate(time.Now()); err != nil {
		t.Fatal("v1 changed", err)
	}
	encoded, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Envelope
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	payload, err := DecodePayload[map[string]string](decoded)
	if err != nil || payload["id"] != "fixture" || decoded.Sequence != 7 {
		t.Fatal("roundtrip failed")
	}
	e.Type = "unknown"
	if e.Validate(time.Now()) == nil {
		t.Fatal("unknown extension accepted")
	}
}

func TestHTTPRequiresMutualCapability(t *testing.T) {
	for _, test := range []struct {
		agent, server []string
		want          bool
	}{
		{nil, []string{httpcheck.Capability}, false},
		{[]string{httpcheck.Capability}, nil, false},
		{[]string{"http_probe.v2"}, []string{httpcheck.Capability}, false},
		{[]string{httpcheck.Capability, httpcheck.Capability}, []string{httpcheck.Capability}, true},
	} {
		got := NegotiateCapabilities(test.agent, test.server)
		if AllowsHTTP(got) != test.want || len(got) > 1 {
			t.Fatalf("negotiated %v", got)
		}
	}
}
