package networkquality

import (
	"bytes"
	"encoding/binary"
	"math"
	"math/rand/v2"
	"sort"
	"testing"
)

func TestHistogramBoundsAndMerge(t *testing.T) {
	values := []float64{0, 0.00001, 0.001, 0.255, 0.256, 0.257, 0.511, 0.512, 1, 10, 100, 1000, 59999.9999, 60000}
	for power := uint32(256); power < maxMicros; power <<= 1 {
		for _, delta := range []float64{-0.000001, 0, 0.000001} {
			values = append(values, float64(power)/1000+delta)
		}
	}
	random := rand.New(rand.NewPCG(1, 2))
	for range 10000 {
		values = append(values, random.Float64()*MaxLatencyMS)
	}
	var whole, left, right Histogram
	for i, value := range values {
		var single Histogram
		if err := single.Add(value); err != nil {
			t.Fatal(err)
		}
		bounds, err := single.Quantile(100)
		if err != nil || value < bounds.LowerMS || value > bounds.UpperMS {
			t.Fatalf("bounds exclude %g: %+v %v", value, bounds, err)
		}
		if bounds.UpperMS-bounds.LowerMS > 0.001+0.01*value {
			t.Fatalf("error budget exceeded: %g %+v", value, bounds)
		}
		if err := whole.Add(value); err != nil {
			t.Fatal(err)
		}
		part := &left
		if i%2 != 0 {
			part = &right
		}
		if err := part.Add(value); err != nil {
			t.Fatal(err)
		}
	}
	if err := left.Merge(right); err != nil {
		t.Fatal(err)
	}
	a, _ := whole.MarshalBinary()
	b, _ := left.MarshalBinary()
	if !bytes.Equal(a, b) {
		t.Fatal("merge changes distribution")
	}
	restored, err := DecodeHistogram(a)
	if err != nil {
		t.Fatal(err)
	}
	sort.Float64s(values)
	for _, p := range []int{1, 50, 95, 99, 100} {
		actual := values[(len(values)*p+99)/100-1]
		bounds, err := restored.Quantile(p)
		if err != nil || actual < bounds.LowerMS || actual > bounds.UpperMS {
			t.Fatalf("quantile %d: %g %+v %v", p, actual, bounds, err)
		}
	}
}

func TestHistogramWeightsAndInvalidEncoding(t *testing.T) {
	var a, b Histogram
	for range 99 {
		a.Add(1)
	}
	b.Add(100)
	if err := a.Merge(b); err != nil {
		t.Fatal(err)
	}
	q, err := a.Quantile(95)
	if err != nil || q.UpperMS > 1.01 {
		t.Fatal("averaged bucket quantiles", q, err)
	}
	for _, value := range []float64{-1, MaxLatencyMS + 1, math.NaN(), math.Inf(1)} {
		if a.Add(value) == nil {
			t.Fatal("invalid RTT accepted")
		}
	}
	raw, _ := a.MarshalBinary()
	cases := [][]byte{nil, []byte("NQH\x02\x00\x00"), append(append([]byte{}, raw...), 0), []byte("NQH\x01\x01\x01\x01\x00")}
	for i := range len(raw) {
		cases = append(cases, raw[:i])
	}
	for _, broken := range cases {
		if _, err := DecodeHistogram(broken); err == nil {
			t.Fatal("corrupt histogram accepted")
		}
	}
	// A valid header cannot request unbounded map allocation.
	oversized := binary.AppendUvarint([]byte("NQH\x01"), MaxBins+1)
	oversized = binary.AppendUvarint(oversized, 1)
	if _, err := DecodeHistogram(oversized); err == nil {
		t.Fatal("too many bins accepted")
	}
	var full Histogram
	if err := full.addCount(0, ^uint64(0)); err != nil {
		t.Fatal(err)
	}
	if full.Add(0) == nil {
		t.Fatal("counter overflow accepted")
	}
	if _, err := full.Quantile(95); err != nil {
		t.Fatal("large-count rank overflow", err)
	}
}

func BenchmarkHistogramSparseRoundTrip(b *testing.B) {
	var h Histogram
	for i := range 5 {
		h.Add(float64(10 + i))
	}
	raw, _ := h.MarshalBinary()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := DecodeHistogram(raw); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(len(raw)), "bytes/bucket")
}
