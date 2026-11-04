package agentclient

import (
	"encoding/json"
	"math"
	"testing"
)

func TestJointSLORejectsNativeFunctionalOnlyPass(t *testing.T) {
	// Retained 50-node/five-browser result from uninstrumented 84e27cd:
	// functional checks passed, but thirty-day history exceeded the 2s SLO.
	result := jointLoadResult{
		Passed: true, SampledPeakRSSBytes: 65236992,
		APILatency: map[string]jointLatency{
			"overview":          {Count: 28, P95: 363.746438},
			"history_dense_30d": {Count: 28, P95: 5342.266962},
		},
	}
	evidence := evaluateJointSLO(result, true, true)
	if evidence.Passed || len(evidence.Gates) != 3 || !evidence.Gates[0].Passed || evidence.Gates[1].Passed || !evidence.Gates[2].Passed {
		t.Fatalf("functional-only pass escaped SLO gate: %+v", evidence)
	}
}

func TestJointSLOBoundariesAndUnavailableObservations(t *testing.T) {
	baseline := func() jointLoadResult {
		return jointLoadResult{SampledPeakRSSBytes: 700*1024*1024 - 1, APILatency: map[string]jointLatency{
			"overview":          {Count: 1, P95: math.Nextafter(500, 0)},
			"history_dense_30d": {Count: 1, P95: math.Nextafter(2000, 0)},
		}}
	}
	for _, tc := range []struct {
		name string
		edit func(*jointLoadResult)
	}{
		{"overview equality", func(r *jointLoadResult) { r.APILatency["overview"] = jointLatency{Count: 1, P95: 500} }},
		{"history equality", func(r *jointLoadResult) { r.APILatency["history_dense_30d"] = jointLatency{Count: 1, P95: 2000} }},
		{"rss equality", func(r *jointLoadResult) { r.SampledPeakRSSBytes = 700 * 1024 * 1024 }},
		{"missing overview", func(r *jointLoadResult) { delete(r.APILatency, "overview") }},
		{"missing history", func(r *jointLoadResult) { delete(r.APILatency, "history_dense_30d") }},
		{"empty history", func(r *jointLoadResult) { r.APILatency["history_dense_30d"] = jointLatency{} }},
		{"negative p95", func(r *jointLoadResult) { r.APILatency["overview"] = jointLatency{Count: 1, P95: -1} }},
		{"nan p95", func(r *jointLoadResult) { r.APILatency["overview"] = jointLatency{Count: 1, P95: math.NaN()} }},
		{"infinite p95", func(r *jointLoadResult) { r.APILatency["overview"] = jointLatency{Count: 1, P95: math.Inf(1)} }},
		{"missing rss", func(r *jointLoadResult) { r.SampledPeakRSSBytes = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := baseline()
			if !evaluateJointSLO(result, true, true).Passed {
				t.Fatal("strictly below all three limits must pass")
			}
			tc.edit(&result)
			evidence := evaluateJointSLO(result, true, true)
			if evidence.Passed {
				t.Fatalf("invalid observation accepted: %+v", evidence)
			}
			if _, err := json.Marshal(evidence); err != nil {
				t.Fatalf("failure evidence must remain serializable: %v", err)
			}
		})
	}
}

func TestJointSLOLimitedProfileDoesNotClaimUnmeasuredGates(t *testing.T) {
	result := jointLoadResult{APILatency: map[string]jointLatency{"overview": {Count: 1, P95: 10}}}
	evidence := evaluateJointSLO(result, false, false)
	if !evidence.Passed || len(evidence.Gates) != 1 || evidence.Gates[0].Metric != "overview.p95" {
		t.Fatalf("limited profile must only record measured gates: %+v", evidence)
	}
}
