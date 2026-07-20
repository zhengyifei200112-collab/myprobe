package v1

import (
	"errors"
	"testing"
	"time"
)

func TestEnvelopeValidation(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name string
		item Envelope
		want error
	}{
		{name: "valid", item: Envelope{Version: Version, Type: TypeReport, SentAt: now}},
		{name: "version", item: Envelope{Version: 99, Type: TypeReport, SentAt: now}, want: ErrUnsupportedVersion},
		{name: "type", item: Envelope{Version: Version, Type: "shell", SentAt: now}, want: ErrInvalidMessageType},
		{name: "future", item: Envelope{Version: Version, Type: TypeReport, SentAt: now.Add(6 * time.Minute)}, want: ErrInvalidTimestamp},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.item.Validate(now)
			if !errors.Is(err, test.want) {
				t.Fatalf("Validate() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestReportRejectsInvalidAbsoluteValues(t *testing.T) {
	report := Report{
		CapturedAt: time.Now().UTC(),
		CPU:        CPUMetric{UsagePercent: 20},
		Memory:     MemoryMetric{TotalBytes: 100, UsedBytes: 101, UsagePercent: 50},
	}
	if err := report.Validate(); err == nil {
		t.Fatal("Validate() accepted memory used above total")
	}
}

func TestEnvelopePayloadRoundTrip(t *testing.T) {
	want := Acknowledgement{Sequence: 17}
	envelope, err := NewEnvelope(TypeAcknowledged, 17, want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodePayload[Acknowledgement](envelope)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("payload = %#v, want %#v", got, want)
	}
}
