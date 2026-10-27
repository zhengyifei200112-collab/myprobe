package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	quality "github.com/zhengyifei200112-collab/myprobe/internal/networkquality"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

type qualityCountsDTO struct {
	Planned         *uint64 `json:"planned"`
	Pending         *uint64 `json:"pending"`
	Success         *uint64 `json:"success"`
	Failure         *uint64 `json:"failure"`
	Missing         *uint64 `json:"missing"`
	NotExecuted     *uint64 `json:"not_executed"`
	UnobservedTasks *uint64 `json:"unobserved_tasks"`
}

type qualityBoundsDTO struct {
	LowerMS float64 `json:"lower_ms"`
	UpperMS float64 `json:"upper_ms"`
}

func projectQualityBounds(b *quality.Bounds) *qualityBoundsDTO {
	if b == nil {
		return nil
	}
	return &qualityBoundsDTO{LowerMS: b.LowerMS, UpperMS: b.UpperMS}
}

type qualityNodeDTO struct {
	NodeID               string            `json:"node_id"`
	Name                 string            `json:"name"`
	Availability         string            `json:"availability"`
	AvailableFrom        *time.Time        `json:"available_from"`
	EffectiveStart       *time.Time        `json:"effective_start"`
	EffectiveEnd         *time.Time        `json:"effective_end"`
	GranularitySeconds   int64             `json:"granularity_seconds"`
	Counts               qualityCountsDTO  `json:"counts"`
	Coverage             *float64          `json:"coverage_pct"`
	FailureRate          *float64          `json:"execution_failure_pct"`
	RTTMean              *float64          `json:"rtt_mean_ms"`
	RTTMin               *float64          `json:"rtt_min_ms"`
	RTTMax               *float64          `json:"rtt_max_ms"`
	P50                  *float64          `json:"rtt_p50_ms"`
	P95                  *float64          `json:"rtt_p95_ms"`
	P50Bounds            *qualityBoundsDTO `json:"rtt_p50_bounds"`
	P95Bounds            *qualityBoundsDTO `json:"rtt_p95_bounds"`
	QuantileMethod       string            `json:"quantile_method"`
	Jitter               *float64          `json:"jitter_ms"`
	JitterPairs          *uint64           `json:"jitter_pairs"`
	FailureCauses        map[string]uint64 `json:"failure_causes"`
	NotExecutedCauses    map[string]uint64 `json:"not_executed_causes"`
	UnobservedTaskCauses map[string]uint64 `json:"unobserved_task_causes"`
}

func projectNetworkQuality(n store.NetworkQualityNodeStats) qualityNodeDTO {
	s := n.Stats
	r := qualityNodeDTO{NodeID: n.NodeID, Name: n.Name, Availability: s.Availability, GranularitySeconds: s.GranularitySeconds, QuantileMethod: s.QuantileMethod,
		Coverage: s.Coverage, FailureRate: s.FailureRate, RTTMean: s.RTTMean, RTTMin: s.RTTMin, RTTMax: s.RTTMax, P50: s.P50, P95: s.P95, P50Bounds: projectQualityBounds(s.P50Bounds), P95Bounds: projectQualityBounds(s.P95Bounds), Jitter: s.Jitter}
	if s.Availability == "no_plan_history" {
		return r
	}
	r.AvailableFrom, r.EffectiveStart, r.EffectiveEnd = &s.AvailableFrom, &s.EffectiveStart, &s.EffectiveEnd
	if s.Availability == "untracked_history" {
		return r
	}
	r.Counts = qualityCountsDTO{Planned: &s.Planned, Pending: &s.Pending, Success: &s.Success, Failure: &s.Failure, Missing: &s.Missing, NotExecuted: &s.NotExecuted, UnobservedTasks: &s.ObservedMissing}
	r.JitterPairs = &s.JitterPairs
	r.FailureCauses, r.NotExecutedCauses, r.UnobservedTaskCauses = map[string]uint64{}, map[string]uint64{}, map[string]uint64{}
	for i, name := range quality.CauseNames {
		switch {
		case i < 4:
			r.FailureCauses[name] = s.Totals.Causes[i]
		case i < 7:
			r.NotExecutedCauses[name] = s.Totals.Causes[i]
		default:
			r.UnobservedTaskCauses[name] = s.Totals.Causes[i]
		}
	}
	return r
}

func parseNetworkQualityQuery(raw string, now time.Time) (store.NetworkQualityBatchQuery, string, error) {
	q := store.NetworkQualityBatchQuery{AsOf: now.UTC(), End: now.UTC()}
	if len(raw) > 4096 {
		return q, "", quality.ErrInvalid
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return q, "", quality.ErrInvalid
	}
	for key, v := range values {
		if key != "node_id" && (len(v) != 1 || len(v[0]) > 128) {
			return q, "", quality.ErrInvalid
		}
		switch key {
		case "node_id", "target_id", "kind", "range", "start", "end", "timezone":
		default:
			return q, "", quality.ErrInvalid
		}
	}
	q.NodeIDs, q.TargetID, q.Kind = values["node_id"], values.Get("target_id"), values.Get("kind")
	if len(q.NodeIDs) < 1 || len(q.NodeIDs) > 8 || len(q.TargetID) == 0 || (q.Kind != "ping" && q.Kind != "tcping") {
		return q, "", quality.ErrInvalid
	}
	tz := values.Get("timezone")
	if tz == "" {
		tz = "UTC"
	}
	if tz == "Local" {
		return q, "", quality.ErrInvalid
	}
	if _, err = time.LoadLocation(tz); err != nil {
		return q, "", quality.ErrInvalid
	}
	start, end := values.Get("start"), values.Get("end")
	if values.Has("start") || values.Has("end") {
		if start == "" || end == "" || values.Has("range") {
			return q, "", quality.ErrInvalid
		}
		q.Start, err = time.Parse(time.RFC3339Nano, start)
		if err != nil {
			return q, "", quality.ErrInvalid
		}
		q.End, err = time.Parse(time.RFC3339Nano, end)
		if err != nil {
			return q, "", quality.ErrInvalid
		}
	} else {
		rangeName := values.Get("range")
		if rangeName == "" {
			rangeName = "1h"
		}
		duration, _, ok := historyRange(rangeName)
		if !ok {
			return q, "", quality.ErrInvalid
		}
		q.Start = q.End.Add(-duration)
	}
	if q.Start.Unix() < 0 || !q.End.After(q.Start) || q.End.After(now) || q.End.Sub(q.Start) > 365*24*time.Hour {
		return q, "", quality.ErrInvalid
	}
	seen := map[string]bool{}
	for _, id := range q.NodeIDs {
		if len(id) < 1 || len(id) > 128 || strings.TrimSpace(id) != id || strings.IndexFunc(id, unicode.IsControl) >= 0 || seen[id] {
			return q, "", quality.ErrInvalid
		}
		seen[id] = true
	}
	return q, tz, nil
}

func (s *Server) publicNetworkQuality(c *gin.Context) { s.writeNetworkQuality(c, "", false) }
func (s *Server) adminNetworkQuality(c *gin.Context)  { s.writeNetworkQuality(c, "", true) }
func (s *Server) shareNetworkQuality(c *gin.Context) {
	item, ok := s.requireShare(c)
	if !ok {
		return
	}
	s.writeNetworkQuality(c, item.ID, false)
}

func (s *Server) writeNetworkQuality(c *gin.Context, share string, admin bool) {
	now := time.Now().UTC()
	q, tz, err := parseNetworkQualityQuery(c.Request.URL.RawQuery, now)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid network quality query"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	var batch store.NetworkQualityBatch
	switch {
	case share != "":
		batch, err = s.store.SharedNetworkQualityBatch(ctx, share, q)
	case admin:
		batch, err = s.store.AdminNetworkQualityBatch(ctx, q)
	default:
		batch, err = s.store.PublicNetworkQualityBatch(ctx, q)
	}
	if err != nil {
		switch {
		case errors.Is(err, quality.ErrInvalid):
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid network quality query"})
		case errors.Is(err, store.ErrNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "network quality not found"})
		case errors.Is(err, store.ErrNetworkQualityUnavailable) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled):
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "network quality temporarily unavailable"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read network quality"})
		}
		return
	}
	nodes := make([]qualityNodeDTO, 0, len(batch.Nodes))
	for _, n := range batch.Nodes {
		nodes = append(nodes, projectNetworkQuality(n))
	}
	c.JSON(http.StatusOK, gin.H{"version": 1, "aggregation": "range_summary", "direction": "observer_to_target", "target": gin.H{"id": batch.TargetID, "name": batch.TargetName, "kind": batch.Kind}, "address_family": gin.H{"status": "unknown", "reason": "not_reported_by_protocol"}, "packet_loss": gin.H{"value_pct": nil, "status": "unavailable", "reason": "packet_counts_not_reported"}, "requested_start": q.Start.UTC(), "requested_end": q.End.UTC(), "as_of": q.AsOf, "timezone": tz, "nodes": nodes})
}
