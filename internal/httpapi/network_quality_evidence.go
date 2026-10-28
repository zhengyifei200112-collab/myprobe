package httpapi

import "github.com/zhengyifei200112-collab/myprobe/internal/store"

// Only enum values and aggregate counters are public. Task IDs, resolved IPs,
// requested host/family, tool output and result bodies never enter these DTOs.
type qualityFamilyDTO struct {
	Status  string  `json:"status"`
	Reason  string  `json:"reason,omitempty"`
	IPv4    *uint64 `json:"ipv4_tasks"`
	IPv6    *uint64 `json:"ipv6_tasks"`
	Unknown *uint64 `json:"unknown_tasks"`
}
type qualityPacketDTO struct {
	Value    *float64 `json:"value_pct"`
	Status   string   `json:"status"`
	Reason   string   `json:"reason,omitempty"`
	Measured *uint64  `json:"measured_tasks"`
	Unknown  *uint64  `json:"unknown_tasks"`
	Sent     *uint64  `json:"sent"`
	Received *uint64  `json:"received"`
}

func projectQualityEvidence(s store.NetworkQualityStats, kind string) (qualityFamilyDTO, qualityPacketDTO) {
	f := qualityFamilyDTO{Status: "unknown", Reason: s.Availability}
	p := qualityPacketDTO{Status: "unavailable", Reason: s.Availability}
	if kind == "tcping" {
		p.Status, p.Reason = "not_applicable", "tcp_connect_probe"
	}
	if s.Availability == "no_plan_history" || s.Availability == "untracked_history" || s.Availability == "resolution_clipped" {
		return f, p
	}
	e := s.Totals.Evidence
	executed := s.Success + s.Failure
	unknownFamily := executed - e.IPv4 - e.IPv6
	f.IPv4, f.IPv6, f.Unknown = &e.IPv4, &e.IPv6, &unknownFamily
	switch {
	case e.IPv4+e.IPv6 == 0:
		f.Reason = "not_reported_by_protocol"
		if executed == 0 {
			f.Reason = "no_executions"
		}
	case unknownFamily > 0:
		f.Status, f.Reason = "partial", "family_not_reported_for_all_executions"
	case e.IPv4 > 0 && e.IPv6 > 0:
		f.Status, f.Reason = "mixed", ""
	case e.IPv4 > 0:
		f.Status, f.Reason = "ipv4", ""
	default:
		f.Status, f.Reason = "ipv6", ""
	}
	if kind == "tcping" {
		return f, p
	}
	measured := e.PacketSuccess + e.PacketFailure
	unknownPackets := executed - measured
	p.Measured, p.Unknown, p.Sent, p.Received = &measured, &unknownPackets, &e.Sent, &e.Received
	if measured == 0 {
		// No observed packets: zero counter sums must not masquerade as a
		// measurement that the probe sent no packets.
		p.Sent, p.Received = nil, nil
		p.Reason = "packet_counts_not_reported"
		if executed == 0 {
			p.Reason = "no_executions"
		}
		return f, p
	}
	loss := 100 * float64(e.Sent-e.Received) / float64(e.Sent)
	p.Value, p.Status, p.Reason = &loss, "available", ""
	if unknownPackets > 0 {
		p.Status, p.Reason = "partial", "packet_counts_not_reported_for_all_executions"
	}
	return f, p
}
