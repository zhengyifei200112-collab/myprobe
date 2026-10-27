package networkquality

import (
	"bytes"
	"math"
	"testing"
)

func TestSegmentMergePreservesJitterBreaksAndWeights(t *testing.T) {
	newSegment := func() Segment {
		s, err := NewSegment("synthetic-epoch", 1000, 10)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	whole, a, b, c := newSegment(), newSegment(), newSegment(), newSegment()
	items := []struct {
		slot    int64
		outcome Outcome
		rtt     float64
		reason  string
	}{{1000, Success, 10, ""}, {1010, Success, 20, ""}, {1020, Failure, 0, "timeout"}, {1030, Success, 30, ""}, {1050, Success, 50, ""}, {1060, Success, 40, ""}}
	for i, item := range items {
		if err := whole.Add(item.slot, item.outcome, item.rtt, item.reason); err != nil {
			t.Fatal(err)
		}
		part := &a
		if i >= 4 {
			part = &c
		} else if i >= 2 {
			part = &b
		}
		if err := part.Add(item.slot, item.outcome, item.rtt, item.reason); err != nil {
			t.Fatal(err)
		}
	}
	for _, part := range []Segment{b, c} {
		raw, err := part.MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		restored, err := DecodeSegment(part.Epoch, part.Anchor, part.Interval, raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := a.MergeEpoch(restored); err != nil {
			t.Fatal(err)
		}
	}
	want, _ := whole.MarshalBinary()
	got, _ := a.MarshalBinary()
	if !bytes.Equal(got, want) {
		t.Fatal("partition/restore changes aggregate")
	}
	if a.Totals.Success != 5 || a.Totals.Failure != 1 || a.Totals.Observed != 6 || a.Totals.RTTSum != 150 || a.Totals.JitterPairs != 2 || a.Totals.JitterSum != 20 {
		t.Fatalf("manual aggregate differs: %+v", a.Totals)
	}
	q, err := a.Totals.Histogram.Quantile(50)
	if err != nil || q.LowerMS > 30 || q.UpperMS < 30 {
		t.Fatal("P50 excludes true sample", q, err)
	}
}

func TestSegmentCrossBucketPairAndEpochBoundary(t *testing.T) {
	a, _ := NewSegment("one", 1000, 10)
	b, _ := NewSegment("one", 1000, 10)
	a.Add(1000, Success, 10, "")
	b.Add(1010, Success, 30, "")
	if err := a.MergeEpoch(b); err != nil || a.Totals.JitterPairs != 1 || a.Totals.JitterSum != 20 {
		t.Fatal("cross-bucket pair missing", err)
	}
	other, _ := NewSegment("two", 1000, 10)
	other.Add(1020, Success, 100, "")
	if a.MergeEpoch(other) == nil {
		t.Fatal("cross-epoch sequence accepted")
	}
	total := Totals{}
	if err := total.Merge(a.Totals); err != nil {
		t.Fatal(err)
	}
	if err := total.Merge(other.Totals); err != nil {
		t.Fatal(err)
	}
	if total.JitterPairs != 1 || total.JitterSum != 20 {
		t.Fatal("cross-epoch totals invented another pair")
	}
	if a.MergeEpoch(b) == nil {
		t.Fatal("overlapping data merged twice")
	}
}

func TestSegmentMissingAndUnexecutedInterruptJitter(t *testing.T) {
	for _, outcome := range []Outcome{Failure, NotExecuted, Unobserved} {
		t.Run(string(outcome), func(t *testing.T) {
			s, _ := NewSegment("one", 1000, 10)
			s.Add(1000, Success, 10, "")
			reason := map[Outcome]string{Failure: "dns", NotExecuted: "unsupported", Unobserved: "offline"}[outcome]
			if err := s.Add(1010, outcome, 0, reason); err != nil {
				t.Fatal(err)
			}
			s.Add(1020, Success, 30, "")
			if s.Totals.JitterPairs != 0 || s.Validate() != nil {
				t.Fatal("non-success observation did not break the pair")
			}
		})
	}
}

func TestSegmentEncodingRejectsCorruptionAndInvalidRows(t *testing.T) {
	s, _ := NewSegment("one", 1000, 10)
	s.Add(1000, Success, 10, "")
	raw, err := s.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	for i := range raw {
		broken := append([]byte(nil), raw...)
		broken[i] ^= 1
		if _, err := DecodeSegment("one", 1000, 10, broken); err == nil {
			t.Fatalf("bit corruption accepted at %d", i)
		}
		if _, err := DecodeSegment("one", 1000, 10, raw[:i]); err == nil {
			t.Fatal("truncated segment accepted")
		}
	}
	if _, err := DecodeSegment("one", 1000, 10, append(append([]byte{}, raw...), 0)); err == nil {
		t.Fatal("trailing data accepted")
	}
	if _, err := DecodeSegment("another-epoch", 1000, 10, raw); err == nil {
		t.Fatal("segment was rebound to another epoch")
	}
	for _, add := range []func() error{
		func() error { return s.Add(1000, Success, 10, "") },
		func() error { return s.Add(1005, Success, 10, "") },
		func() error { return s.Add(1010, Failure, 0, "arbitrary private text") },
		func() error { return s.Add(1010, Success, math.NaN(), "") },
	} {
		if add() == nil {
			t.Fatal("invalid observation accepted")
		}
		after, _ := s.MarshalBinary()
		if !bytes.Equal(after, raw) {
			t.Fatal("rejected row mutated aggregate")
		}
	}
	s.Totals.Success++
	if s.Validate() == nil {
		t.Fatal("inconsistent counters accepted")
	}
}

func TestHistogramEveryRepresentableBinMeetsErrorBudget(t *testing.T) {
	count := 0
	check := func(lower uint32) {
		if lower > maxMicros {
			return
		}
		count++
		var h Histogram
		if err := h.addCount(lower, 1); err != nil {
			t.Fatal(err)
		}
		bounds, err := h.Quantile(100)
		if err != nil {
			t.Fatal(err)
		}
		if bounds.UpperMS-bounds.LowerMS > 0.001+0.01*bounds.LowerMS+1e-12 {
			t.Fatalf("bin %d exceeds budget: %+v", lower, bounds)
		}
	}
	for lower := uint32(0); lower < 256; lower++ {
		check(lower)
	}
	for shift := 1; shift <= 18; shift++ {
		for significant := uint32(128); significant < 256; significant++ {
			check(significant << shift)
		}
	}
	if count > MaxBins {
		t.Fatal("encoded bin bound is too small")
	}
}

func BenchmarkSegmentSparseRoundTrip(b *testing.B) {
	s, _ := NewSegment("synthetic", 1000, 60)
	for i := range 5 {
		s.Add(1000+int64(i)*60, Success, float64(10+i), "")
	}
	raw, _ := s.MarshalBinary()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := DecodeSegment("synthetic", 1000, 60, raw); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(len(raw)), "bytes/bucket")
}
