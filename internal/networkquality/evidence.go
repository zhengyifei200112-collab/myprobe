package networkquality

const MaxProbePackets uint64 = 5

// Zero family and false PacketsKnown mean unavailable, including all v1 history.
type Evidence struct {
	Family         uint8
	PacketsKnown   bool
	Sent, Received uint64
}

type EvidenceTotals struct {
	IPv4, IPv6                   uint64
	PacketSuccess, PacketFailure uint64
	Sent, Received               uint64
}

func (e Evidence) totals(outcome Outcome) (EvidenceTotals, error) {
	var t EvidenceTotals
	if e.Family != 0 && e.Family != 4 && e.Family != 6 {
		return t, ErrInvalid
	}
	if outcome != Success && outcome != Failure {
		if e != (Evidence{}) {
			return t, ErrInvalid
		}
		return t, nil
	}
	if e.Family == 4 {
		t.IPv4 = 1
	}
	if e.Family == 6 {
		t.IPv6 = 1
	}
	if !e.PacketsKnown {
		if e.Sent != 0 || e.Received != 0 {
			return t, ErrInvalid
		}
		return t, nil
	}
	if e.Family == 0 || e.Sent < 1 || e.Sent > MaxProbePackets || e.Received > e.Sent || outcome == Success && e.Received < 1 || outcome == Failure && e.Received != 0 {
		return t, ErrInvalid
	}
	if outcome == Success {
		t.PacketSuccess = 1
	} else {
		t.PacketFailure = 1
	}
	t.Sent, t.Received = e.Sent, e.Received
	return t, nil
}

func (t *EvidenceTotals) merge(other EvidenceTotals) error {
	a := []*uint64{&t.IPv4, &t.IPv6, &t.PacketSuccess, &t.PacketFailure, &t.Sent, &t.Received}
	b := []uint64{other.IPv4, other.IPv6, other.PacketSuccess, other.PacketFailure, other.Sent, other.Received}
	for i, p := range a {
		if ^uint64(0)-*p < b[i] {
			return ErrInvalid
		}
	}
	for i, p := range a {
		*p += b[i]
	}
	return nil
}

func (t EvidenceTotals) validate(success, failure uint64) error {
	if ^uint64(0)-success < failure || ^uint64(0)-t.IPv4 < t.IPv6 || t.IPv4+t.IPv6 > success+failure || t.PacketSuccess > success || t.PacketFailure > failure || ^uint64(0)-t.PacketSuccess < t.PacketFailure {
		return ErrInvalid
	}
	packets := t.PacketSuccess + t.PacketFailure
	if packets > t.IPv4+t.IPv6 || packets > ^uint64(0)/MaxProbePackets || t.Sent < packets || t.Sent > packets*MaxProbePackets || t.Received > t.Sent || t.Received < t.PacketSuccess || t.Received > t.PacketSuccess*MaxProbePackets || t.Sent-t.Received < t.PacketFailure {
		return ErrInvalid
	}
	return nil
}

// AddEvidence preserves the same adjacency/population rules as Add, with proof
// checked before mutating the histogram. Legacy callers of Add add no evidence.
func (s *Segment) AddEvidence(slot int64, outcome Outcome, rtt float64, reason string, e Evidence) error {
	part, err := e.totals(outcome)
	if err != nil {
		return err
	}
	combined := s.Totals.Evidence
	if err = combined.merge(part); err != nil {
		return err
	}
	if err = s.Add(slot, outcome, rtt, reason); err != nil {
		return err
	}
	s.Totals.Evidence = combined
	return nil
}
