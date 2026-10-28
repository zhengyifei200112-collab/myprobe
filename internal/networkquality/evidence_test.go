package networkquality

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestSegmentLegacyNQS1EvidenceRemainsUnknown(t *testing.T) {
	// Frozen output of cb84e4a's NQS1 writer, not the current encoder.
	raw, err := hex.DecodeString("4e51530102010100000001000000000000000000000000000000002440000000000000244000000000000024400000000000000000000a0100000000000024400000000000000000094e5148010101804e01c78c3312")
	if err != nil {
		t.Fatal(err)
	}
	s, err := DecodeSegment("synthetic-legacy-codec", 1000, 10, raw)
	if err != nil {
		t.Fatal(err)
	}
	if s.Totals.Success != 1 || s.Totals.Failure != 1 || s.Totals.Evidence != (EvidenceTotals{}) {
		t.Fatal("legacy history acquired proof", s.Totals)
	}
	if err = s.AddEvidence(1020, Success, 30, "", Evidence{Family: 6, PacketsKnown: true, Sent: 3, Received: 2}); err != nil {
		t.Fatal(err)
	}
	encoded, err := s.MarshalBinary()
	if err != nil || !bytes.Equal(encoded[:4], []byte{'N', 'Q', 'S', 2}) {
		t.Fatal("new evidence not versioned", err)
	}
	got, err := DecodeSegment(s.Epoch, s.Anchor, s.Interval, encoded)
	if err != nil || got.Totals.Evidence != (EvidenceTotals{IPv6: 1, PacketSuccess: 1, Sent: 3, Received: 2}) || got.Totals.Success != 2 || got.Totals.JitterPairs != 0 {
		t.Fatal("mixed legacy/new population changed", got, err)
	}
	for i := range encoded {
		bad := bytes.Clone(encoded)
		bad[i] ^= 1
		if _, err := DecodeSegment(s.Epoch, s.Anchor, s.Interval, bad); err == nil {
			t.Fatalf("corruption accepted at %d", i)
		}
	}
}

func TestSegmentEvidenceWeightedMergeAndReasons(t *testing.T) {
	a, _ := NewSegment("proof", 1000, 10)
	b, _ := NewSegment("proof", 1000, 10)
	if err := a.AddEvidence(1000, Success, 10, "", Evidence{Family: 4, PacketsKnown: true, Sent: 5, Received: 3}); err != nil {
		t.Fatal(err)
	}
	if err := b.AddEvidence(1010, Failure, 0, "timeout", Evidence{Family: 6, PacketsKnown: true, Sent: 2}); err != nil {
		t.Fatal(err)
	}
	if err := b.AddEvidence(1020, Success, 30, "", Evidence{Family: 6}); err != nil {
		t.Fatal(err)
	}
	for i, reason := range []string{"dns", "cancelled", "no_address", "no_packets_sent", "send_error", "internal"} {
		if err := b.AddEvidence(1030+int64(i)*10, NotExecuted, 0, reason, Evidence{}); err != nil {
			t.Fatal(reason, err)
		}
	}
	if err := a.MergeEpoch(b); err != nil {
		t.Fatal(err)
	}
	raw, err := a.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	r, err := DecodeSegment(a.Epoch, a.Anchor, a.Interval, raw)
	want := EvidenceTotals{IPv4: 1, IPv6: 2, PacketSuccess: 1, PacketFailure: 1, Sent: 7, Received: 3}
	if err != nil || r.Totals.Evidence != want || r.Totals.NotExecuted != 6 || r.Totals.JitterPairs != 0 {
		t.Fatal(r, err)
	}
	if CauseOutcome(causeIndex(Failure, "dns")) != Failure || CauseOutcome(causeIndex(NotExecuted, "dns")) != NotExecuted {
		t.Fatal("DNS causes collapsed")
	}
}

func TestSegmentRejectsInvalidEvidenceWithoutMutation(t *testing.T) {
	for _, evidence := range []Evidence{{Family: 5}, {Sent: 1}, {Family: 4, PacketsKnown: true}, {Family: 4, PacketsKnown: true, Sent: 6, Received: 1}, {PacketsKnown: true, Sent: 1, Received: 1}, {Family: 6, PacketsKnown: true, Sent: 1, Received: 2}} {
		s, _ := NewSegment("proof", 1000, 10)
		before, _ := s.MarshalBinary()
		if err := s.AddEvidence(1000, Success, 10, "", evidence); err == nil {
			t.Fatal("invalid evidence accepted", evidence)
		}
		after, _ := s.MarshalBinary()
		if !bytes.Equal(before, after) {
			t.Fatal("rejected proof mutated segment")
		}
	}
	totals := EvidenceTotals{Sent: ^uint64(0)}
	before := totals
	if totals.merge(EvidenceTotals{IPv4: 1, Sent: 1}) == nil || totals != before {
		t.Fatal("overflow mutated counters")
	}
	for _, invalid := range []EvidenceTotals{{IPv4: 2}, {PacketSuccess: 1, Sent: 1, Received: 1}, {IPv4: 1, PacketFailure: 1, Sent: 1, Received: 1}, {IPv4: 1, PacketSuccess: 1, Sent: 6, Received: 1}} {
		if invalid.validate(1, 0) == nil {
			t.Fatal("inconsistent aggregate accepted", invalid)
		}
	}
}
