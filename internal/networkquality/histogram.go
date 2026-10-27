// Package networkquality implements bounded, mergeable network statistics.
package networkquality

import (
	"encoding/binary"
	"errors"
	"math"
	"math/bits"
	"sort"
)

const (
	MaxLatencyMS    = 60000
	maxMicros       = uint32(MaxLatencyMS * 1000)
	MaxBins         = 2560
	MaxEncodedBytes = 64 * 1024
)

var (
	ErrInvalid   = errors.New("invalid network statistics")
	ErrNoSamples = errors.New("no successful network samples")
)

type Bounds struct {
	LowerMS float64 `json:"lower_ms"`
	UpperMS float64 `json:"upper_ms"`
}

// Eight significant bits bound each nonzero integer bin's relative width.
// Ceiling to microseconds separately adds at most one microsecond uncertainty.
type Histogram struct {
	bins  map[uint32]uint64
	count uint64
}

func validRTT(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= MaxLatencyMS
}
func binShift(value uint32) int  { return max(0, bits.Len32(value)-8) }
func validBin(value uint32) bool { return value <= maxMicros && value%uint32(1<<binShift(value)) == 0 }

func (h *Histogram) Add(milliseconds float64) error {
	if !validRTT(milliseconds) {
		return ErrInvalid
	}
	micros := uint32(math.Ceil(milliseconds * 1000))
	lower := micros >> binShift(micros) << binShift(micros)
	return h.addCount(lower, 1)
}

func (h *Histogram) addCount(lower uint32, count uint64) error {
	if !validBin(lower) || count == 0 || ^uint64(0)-h.count < count {
		return ErrInvalid
	}
	if h.bins == nil {
		h.bins = make(map[uint32]uint64)
	}
	h.bins[lower] += count
	h.count += count
	return nil
}

func (h Histogram) Count() uint64 { return h.count }
func (h Histogram) BinCount() int { return len(h.bins) }

func (h *Histogram) Merge(other Histogram) error {
	if ^uint64(0)-h.count < other.count {
		return ErrInvalid
	}
	for lower, count := range other.bins {
		if err := h.addCount(lower, count); err != nil {
			return err
		}
	}
	return nil
}

func (h Histogram) keys() []uint32 {
	keys := make([]uint32, 0, len(h.bins))
	for lower := range h.bins {
		keys = append(keys, lower)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

func (h Histogram) Quantile(percent int) (Bounds, error) {
	if percent < 1 || percent > 100 {
		return Bounds{}, ErrInvalid
	}
	if h.count == 0 {
		return Bounds{}, ErrNoSamples
	}
	// Compute ceil(count*percent/100) without overflowing the multiplication.
	rank := h.count/100*uint64(percent) + (h.count%100*uint64(percent)+99)/100
	var seen uint64
	for _, lower := range h.keys() {
		seen += h.bins[lower]
		if seen < rank {
			continue
		}
		if lower == 0 {
			return Bounds{}, nil
		}
		upper := min(maxMicros, lower+uint32(1<<binShift(lower))-1)
		return Bounds{LowerMS: float64(lower-1) / 1000, UpperMS: float64(upper) / 1000}, nil
	}
	return Bounds{}, ErrInvalid
}

// MarshalBinary writes canonical ascending sparse bins; no raw samples survive
// here. Bounds on bins and bytes also apply when decoding stored data.
func (h Histogram) MarshalBinary() ([]byte, error) {
	if len(h.bins) > MaxBins {
		return nil, ErrInvalid
	}
	raw := []byte{'N', 'Q', 'H', 1}
	raw = binary.AppendUvarint(raw, uint64(len(h.bins)))
	raw = binary.AppendUvarint(raw, h.count)
	var previous uint32
	for _, lower := range h.keys() {
		raw = binary.AppendUvarint(raw, uint64(lower-previous))
		raw = binary.AppendUvarint(raw, h.bins[lower])
		previous = lower
	}
	if len(raw) > MaxEncodedBytes {
		return nil, ErrInvalid
	}
	return raw, nil
}

func DecodeHistogram(raw []byte) (Histogram, error) {
	h := Histogram{}
	if len(raw) < 6 || len(raw) > MaxEncodedBytes || string(raw[:4]) != "NQH\x01" {
		return h, ErrInvalid
	}
	offset := 4
	read := func() (uint64, error) {
		value, n := binary.Uvarint(raw[offset:])
		if n <= 0 {
			return 0, ErrInvalid
		}
		offset += n
		return value, nil
	}
	bins, err := read()
	if err != nil || bins > MaxBins {
		return h, ErrInvalid
	}
	expected, err := read()
	if err != nil {
		return h, err
	}
	var previous uint64
	for i := uint64(0); i < bins; i++ {
		delta, err := read()
		if err != nil || (i > 0 && delta == 0) || delta > uint64(maxMicros)-previous {
			return Histogram{}, ErrInvalid
		}
		lower := previous + delta
		count, err := read()
		if err != nil {
			return Histogram{}, err
		}
		if err = h.addCount(uint32(lower), count); err != nil {
			return Histogram{}, err
		}
		previous = lower
	}
	if offset != len(raw) || h.count != expected {
		return Histogram{}, ErrInvalid
	}
	return h, nil
}
