package httpapi

import (
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

type qualitySeriesNodeDTO struct {
	qualityNodeDTO
	Points []qualityPointDTO `json:"points"`
}

// Point jitter is intentionally absent: the range result preserves adjacency
// across graph buckets, while a point's independent sequence stops at its edge.
type qualityPointDTO struct {
	Time           time.Time         `json:"time"`
	End            time.Time         `json:"end"`
	Availability   string            `json:"availability"`
	EffectiveStart *time.Time        `json:"effective_start"`
	EffectiveEnd   *time.Time        `json:"effective_end"`
	Counts         qualityCountsDTO  `json:"counts"`
	Coverage       *float64          `json:"coverage_pct"`
	FailureRate    *float64          `json:"execution_failure_pct"`
	RTTMean        *float64          `json:"rtt_mean_ms"`
	RTTMin         *float64          `json:"rtt_min_ms"`
	RTTMax         *float64          `json:"rtt_max_ms"`
	P50            *float64          `json:"rtt_p50_ms"`
	P95            *float64          `json:"rtt_p95_ms"`
	P50Bounds      *qualityBoundsDTO `json:"rtt_p50_bounds"`
	P95Bounds      *qualityBoundsDTO `json:"rtt_p95_bounds"`
	QuantileMethod string            `json:"quantile_method"`
}

func projectNetworkQualityPoint(p store.NetworkQualityPoint) qualityPointDTO {
	s := projectNetworkQuality(store.NetworkQualityNodeStats{Stats: p.Stats})
	r := qualityPointDTO{Time: p.Time, End: p.End, Availability: s.Availability, Counts: s.Counts, Coverage: s.Coverage, FailureRate: s.FailureRate, RTTMean: s.RTTMean, RTTMin: s.RTTMin, RTTMax: s.RTTMax, P50: s.P50, P95: s.P95, P50Bounds: s.P50Bounds, P95Bounds: s.P95Bounds, QuantileMethod: s.QuantileMethod}
	if p.Stats.Availability != "untracked_history" && p.Stats.Availability != "resolution_clipped" {
		r.EffectiveStart, r.EffectiveEnd = s.EffectiveStart, s.EffectiveEnd
	}
	return r
}
