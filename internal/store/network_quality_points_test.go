package store

import (
	"context"
	"math"
	"sort"
	"testing"
	"time"
)

func assertNetworkPointOracle(t *testing.T, q NetworkQualityQuery, r NetworkQualityStats) {
	t.Helper()
	if len(r.Points) == 0 || len(r.Points) > MaxNetworkQualityPoints {
		t.Fatal("missing or unbounded points", len(r.Points))
	}
	var planned, pending, success, failure, missing, pairs uint64
	for i, p := range r.Points {
		if i == 0 && !p.Time.Equal(q.Start) || i > 0 && !p.Time.Equal(r.Points[i-1].End) {
			t.Fatal("series has a time gap/overlap", i)
		}
		if p.End.After(q.End) || !p.End.After(p.Time) {
			t.Fatal("point expanded query or inverted its range")
		}
		s := p.Stats
		var wantSuccess, wantFailure, wantNotExecuted, wantUnobserved, wantPlanned uint64
		var values []float64
		for slot := p.Time; slot.Before(p.End); slot = slot.Add(5 * time.Second) {
			wantPlanned++
			index := int(slot.Sub(q.Start) / (5 * time.Second))
			if index >= 500 {
				continue
			}
			switch index % 17 {
			case 3:
			case 4:
				wantUnobserved++
			case 5:
				wantFailure++
			case 6:
				wantNotExecuted++
			default:
				wantSuccess++
				values = append(values, float64(index%100)/3)
			}
		}
		if s.Planned != wantPlanned || s.Success != wantSuccess || s.Failure != wantFailure || s.NotExecuted != wantNotExecuted || s.ObservedMissing != wantUnobserved || s.Missing != wantPlanned-wantSuccess-wantFailure || s.Pending != 0 {
			t.Fatalf("point %d differs from independent slot pattern: %+v", i, s)
		}
		if len(values) == 0 {
			if s.RTTMean != nil || s.P95 != nil {
				t.Fatal("no sample became zero RTT")
			}
		} else {
			sort.Float64s(values)
			for _, item := range []struct {
				percent      int
				actual       *float64
				lower, upper *float64
			}{{percent: 50, actual: s.P50}, {percent: 95, actual: s.P95}} {
				want := values[(len(values)*item.percent+99)/100-1]
				if s.QuantileMethod == "exact_raw" {
					if item.actual == nil || *item.actual != want {
						t.Fatal("raw point percentile differs", i)
					}
				} else {
					b := s.P50Bounds
					if item.percent == 95 {
						b = s.P95Bounds
					}
					if b == nil || want < b.LowerMS || want > b.UpperMS {
						t.Fatal("point histogram excludes true quantile", i)
					}
				}
			}
		}
		planned += s.Planned
		pending += s.Pending
		success += s.Success
		failure += s.Failure
		missing += s.Missing
		pairs += s.JitterPairs
	}
	if !r.Points[len(r.Points)-1].End.Equal(q.End) || planned != r.Planned || pending != r.Pending || success != r.Success || failure != r.Failure || missing != r.Missing {
		t.Fatal("series does not partition the exact queried population")
	}
	if pairs >= r.JitterPairs {
		t.Fatal("oracle no longer tests aggregate adjacency across point boundaries")
	}
}

func TestNetworkQualitySeriesRawAndMixedTierPopulations(t *testing.T) {
	s, q, policy := networkArchiveFixture(t)
	ctx := context.Background()
	q.Series = true
	raw, err := s.PublicNetworkQuality(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if raw.BucketSeconds != 60 || len(raw.Points) != 45 {
		t.Fatal("unexpected raw grid", raw.BucketSeconds, len(raw.Points))
	}
	assertNetworkPointOracle(t, q, raw)
	for range 10 {
		b, err := s.RetainNetworkQuality(ctx, q.AsOf, policy)
		if err != nil {
			t.Fatal(err)
		}
		if !b.More {
			break
		}
	}
	rolled, err := s.PublicNetworkQuality(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if rolled.BucketSeconds != 300 || len(rolled.Points) != 9 {
		t.Fatal("retained resolution was split", rolled.BucketSeconds, len(rolled.Points))
	}
	assertNetworkQualityEquivalent(t, raw, rolled)
	assertNetworkPointOracle(t, q, rolled)
}

func TestNetworkQualitySeriesUnknownAndPendingAreNotHealthy(t *testing.T) {
	s, q := networkQualityFixture(t)
	ctx := context.Background()
	anchor := q.Start
	q.Series = true
	q.Start = q.Start.Add(-time.Hour)
	q.End = anchor.Add(10 * time.Second)
	q.AsOf = q.End
	r, err := s.PublicNetworkQuality(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Points) == 0 {
		t.Fatal("unknown/pending range lost its timeline")
	}
	unknown, known := 0, 0
	var pending uint64
	for _, p := range r.Points {
		if p.Stats.Availability == "untracked_history" {
			unknown++
			if p.Stats.Coverage != nil || p.Stats.RTTMean != nil {
				t.Fatal("unknown point became healthy")
			}
		} else {
			known++
			pending += p.Stats.Pending
			if p.Stats.Planned != 0 || p.Stats.Coverage != nil || p.Stats.P95 != nil {
				t.Fatal("immature point counted as execution")
			}
		}
	}
	if unknown == 0 || known == 0 || pending != 1 {
		t.Fatal("unknown and pending evidence were lost", unknown, known, pending)
	}
	q.End = anchor.Add(-time.Second)
	q.AsOf = anchor
	r, err = s.PublicNetworkQuality(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if r.Availability != "untracked_history" || len(r.Points) == 0 {
		t.Fatal("wholly unknown range lost points or fabricated coverage")
	}
	for _, p := range r.Points {
		if p.Stats.Availability != "untracked_history" {
			t.Fatal("old point marked known")
		}
	}
}

func TestNetworkQualityPointGridBoundsAndPartialEndpoints(t *testing.T) {
	for _, duration := range []time.Duration{time.Nanosecond, time.Hour, 24 * time.Hour, 7 * 24 * time.Hour, 30 * 24 * time.Hour, 365 * 24 * time.Hour} {
		start := time.Date(2026, 1, 2, 3, 4, 5, 123, time.UTC)
		end := start.Add(duration)
		for _, minimum := range []int64{1, 60, 300} {
			q := NetworkQualityQuery{Start: start, End: end, AsOf: end, Series: true}
			r := NetworkQualityStats{EffectiveStart: start, EffectiveEnd: end, GranularitySeconds: minimum}
			c, err := newNetworkPointCollector(q, r)
			if err != nil {
				t.Fatal(err)
			}
			if len(c.points) > MaxNetworkQualityPoints || len(c.points) == 0 || !c.points[0].Time.Equal(start) || !c.points[len(c.points)-1].End.Equal(end) || c.size < minimum {
				t.Fatal("grid expanded bounds or point count", duration, len(c.points))
			}
		}
	}
	end := time.Unix(9223372035, 999999999).UTC()
	start := end.Add(-24 * time.Hour)
	c, err := newNetworkPointCollector(NetworkQualityQuery{Start: start, End: end, AsOf: end, Series: true}, NetworkQualityStats{EffectiveStart: start, EffectiveEnd: end, GranularitySeconds: 1})
	if err != nil || len(c.points) == 0 || len(c.points) > 2000 || !c.points[len(c.points)-1].End.Equal(end) {
		t.Fatal("late UnixNano boundary overflowed", err)
	}
	if _, err = networkQualityPointSize(time.Unix(0, 0), time.Unix(math.MaxInt64/1000000000, 0), 1); err == nil {
		t.Fatal("unbounded grid accepted")
	}
}

func TestNetworkQualitySeriesEpochBoundaryInsidePoint(t *testing.T) {
	s, q, policy := networkArchiveFixture(t)
	ctx := context.Background()
	q.Series = true
	before, err := s.PublicNetworkQuality(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	var epoch string
	if err = s.db.QueryRow(`SELECT id FROM network_schedule_epochs WHERE node_id=?`, q.NodeID).Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	boundary := q.Start.Add(246 * 5 * time.Second).Unix()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`UPDATE network_schedule_epochs SET ends_at=? WHERE id=?`, []any{boundary, epoch}},
		{`INSERT INTO network_schedule_epochs(id,node_id,target_id,kind,host,port,interval_seconds,timeout_ms,starts_at) SELECT 'synthetic-series-epoch',node_id,target_id,kind,host,port,interval_seconds,timeout_ms,? FROM network_schedule_epochs WHERE id=?`, []any{boundary, epoch}},
		{`UPDATE network_probe_tasks SET epoch_id='synthetic-series-epoch' WHERE epoch_id=? AND scheduled_at>=?`, []any{epoch, boundary}},
	} {
		if _, err = s.db.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	for phase := range 2 {
		got, err := s.PublicNetworkQuality(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		if got.JitterPairs+1 != before.JitterPairs || got.Success != before.Success {
			t.Fatal("range bridged epochs inside a point")
		}
		var actual uint64
		for _, p := range got.Points {
			actual += p.Stats.JitterPairs
		}
		var expected uint64
		success := func(i int) bool { return i >= 0 && i < 500 && (i%17 < 3 || i%17 > 6) }
		for i := 1; i < 500; i++ {
			if i != 246 && success(i-1) && success(i) && int64((i-1)*5)/got.BucketSeconds == int64(i*5)/got.BucketSeconds {
				expected++
			}
		}
		if actual != expected {
			t.Fatal("point bridged an epoch or failed slot", phase, actual, expected)
		}
		if phase == 0 {
			for range 10 {
				b, err := s.RetainNetworkQuality(ctx, q.AsOf, policy)
				if err != nil {
					t.Fatal(err)
				}
				if !b.More {
					break
				}
			}
		}
	}
}
