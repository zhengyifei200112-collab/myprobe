package agentclient

import "math"

// Fixed acceptance limits from the development specification. Functional
// completion alone must not publish Passed=true or discard a failed fixture.
type jointSLOGate struct {
	Metric    string
	Unit      string
	Limit     float64
	Observed  float64
	Available bool
	Passed    bool
}

type jointSLOEvidence struct {
	Gates  []jointSLOGate
	Passed bool
}

func evaluateJointSLO(result jointLoadResult, withHistory, withRSS bool) jointSLOEvidence {
	evidence := jointSLOEvidence{Passed: true}
	add := func(metric, unit string, observed, limit float64, available bool) {
		finite := !math.IsNaN(observed) && !math.IsInf(observed, 0)
		gate := jointSLOGate{Metric: metric, Unit: unit, Limit: limit, Available: available && finite}
		// Keep the evidence JSON serializable even when an observation is invalid.
		if finite {
			gate.Observed = observed
		}
		gate.Passed = gate.Available && observed >= 0 && observed < limit
		evidence.Gates = append(evidence.Gates, gate)
		evidence.Passed = evidence.Passed && gate.Passed
	}
	latency := func(name string, limit float64) {
		value, exists := result.APILatency[name]
		add(name+".p95", "ms", value.P95, limit, exists && value.Count > 0)
	}
	latency("overview", 500)
	if withHistory {
		latency("history_dense_30d", 2000)
	}
	if withRSS {
		add("server.sampled_peak_rss", "bytes", float64(result.SampledPeakRSSBytes), 700*1024*1024, result.SampledPeakRSSBytes > 0)
	}
	return evidence
}
