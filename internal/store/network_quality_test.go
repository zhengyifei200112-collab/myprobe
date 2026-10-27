package store

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

func networkQualityFixture(t *testing.T) (*Store, NetworkQualityQuery) {
	t.Helper()
	s, node, target, epoch, now := networkTaskFixture(t)
	ctx := context.Background()
	anchor := now.Truncate(time.Second)
	if _, err := s.db.Exec(`UPDATE network_schedule_metadata SET known_since=?,known_results_since=?`, anchor.Unix(), anchor.Unix()); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		offset int
		class  string
		rtt    float64
	}{{0, "", 10}, {30, "", 20}, {60, "timeout", 0}, {90, "busy", 0}, {120, "offline", 0}, {180, "", 40}} {
		slot := anchor.Add(time.Duration(item.offset) * time.Second)
		task, err := s.ClaimNetworkTask(ctx, epoch, slot, slot.Add(100*time.Millisecond))
		if err != nil {
			t.Fatal(err)
		}
		if item.class == "offline" {
			if err := s.FinishNetworkDispatch(ctx, task, "offline", slot.Add(100*time.Millisecond)); err != nil {
				t.Fatal(err)
			}
			continue
		}
		r := protocol.LatencyResult{TaskID: task.Task.ID, TargetID: target.ID, Success: item.class == "", LatencyMS: item.rtt, ErrorClass: item.class, CompletedAt: slot.Add(time.Second)}
		if accepted, err := s.AcceptNetworkResult(ctx, node, target.Kind, r, slot.Add(time.Second)); err != nil || !accepted {
			t.Fatal(err)
		}
	}
	// Diagnostics are accepted but must not enlarge the planned population.
	task, err := s.RecordManualNetworkTask(ctx, node, protocol.Task{ID: "synthetic-quality-manual", Kind: target.Kind, TargetID: target.ID, Host: target.Host, TimeoutMS: 1000, ExpiresAt: anchor.Add(time.Minute)}, anchor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AcceptNetworkResult(ctx, node, target.Kind, protocol.LatencyResult{TaskID: task.Task.ID, TargetID: target.ID, Success: true, LatencyMS: 600, CompletedAt: anchor.Add(time.Second)}, anchor.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	asOf := anchor.Add(220*time.Second + 500*time.Millisecond)
	return s, NetworkQualityQuery{NodeID: node, TargetID: target.ID, Kind: target.Kind, Start: anchor, End: asOf, AsOf: asOf}
}

func TestNetworkQualityRawCountsPercentilesAndJitter(t *testing.T) {
	s, q := networkQualityFixture(t)
	r, err := s.PublicNetworkQuality(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if r.Planned != 7 || r.Pending != 1 || r.Success != 3 || r.Failure != 1 || r.Missing != 3 || r.NotExecuted != 1 || r.ObservedMissing != 1 {
		t.Fatalf("counts differ from seven manual slots: %+v", r)
	}
	if r.Coverage == nil || math.Abs(*r.Coverage-400.0/7) > 1e-9 || r.FailureRate == nil || *r.FailureRate != 25 {
		t.Fatal("weighted rates differ")
	}
	if r.P50 == nil || *r.P50 != 20 || r.P95 == nil || *r.P95 != 40 || r.QuantileMethod != "exact_raw" {
		t.Fatal("exact sample quantiles differ")
	}
	if r.Jitter == nil || *r.Jitter != 10 || r.JitterPairs != 1 {
		t.Fatal("failure/missing slots did not interrupt jitter")
	}
	q.Start = q.Start.Add(100 * time.Millisecond)
	r, err = s.PublicNetworkQuality(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if r.Planned != 6 || r.Success != 2 || r.Coverage == nil || *r.Coverage != 50 || r.Jitter != nil {
		t.Fatal("half-open subsecond query included an earlier slot")
	}
}

func TestNetworkQualityUnknownHistoryPendingAndVisibility(t *testing.T) {
	s, q := networkQualityFixture(t)
	ctx := context.Background()
	old := q
	old.Start = q.Start.Add(-time.Hour)
	old.End = q.Start.Add(-time.Second)
	r, err := s.PublicNetworkQuality(ctx, old)
	if err != nil {
		t.Fatal(err)
	}
	if r.Availability != "untracked_history" || r.Coverage != nil || r.P50 != nil || r.Planned != 0 {
		t.Fatal("old untracked history was fabricated")
	}
	partial := q
	partial.Start = old.Start
	r, err = s.PublicNetworkQuality(ctx, partial)
	if err != nil || r.Availability != "partial_known_history" || r.Planned != 7 {
		t.Fatal("partial-known range not explained", err)
	}
	pending := q
	pending.AsOf = q.Start.Add(10 * time.Second)
	pending.End = pending.AsOf
	r, err = s.PublicNetworkQuality(ctx, pending)
	if err != nil {
		t.Fatal(err)
	}
	if r.Planned != 0 || r.Pending != 1 || r.Coverage != nil || r.FailureRate != nil || r.P50 != nil {
		t.Fatal("immature or zero denominator became healthy")
	}
	if _, err := s.UpdateNode(ctx, q.NodeID, UpdateNodeParams{Name: "Synthetic hidden quality", Hidden: true, LatencyMode: "ping", CollectionSeconds: 15, ReportSeconds: 15}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublicNetworkQuality(ctx, q); !errors.Is(err, ErrNotFound) {
		t.Fatal("hidden node quality exposed", err)
	}
	if _, err := s.AdminNetworkQuality(ctx, q); err != nil {
		t.Fatal("authorized administration lost hidden-node history", err)
	}
}

func TestNetworkQualityRejectsCorruptStoredResult(t *testing.T) {
	s, q := networkQualityFixture(t)
	if _, err := s.db.Exec(`UPDATE network_probe_tasks SET result_json='{}' WHERE source='scheduled' AND state='completed'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublicNetworkQuality(context.Background(), q); !errors.Is(err, ErrNetworkQualityUnavailable) {
		t.Fatal("corrupt results became partial healthy statistics", err)
	}
}
