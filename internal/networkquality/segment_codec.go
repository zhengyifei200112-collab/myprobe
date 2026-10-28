package networkquality

import (
	"encoding/binary"
	"hash/crc32"
	"math"
)

const MaxSegmentBytes = MaxEncodedBytes + 512

func appendFloat(raw []byte, value float64) []byte {
	return binary.LittleEndian.AppendUint64(raw, math.Float64bits(value))
}

func segmentChecksum(epoch string, anchor, interval int64, raw []byte) uint32 {
	var metadata [16]byte
	binary.LittleEndian.PutUint64(metadata[:8], uint64(anchor))
	binary.LittleEndian.PutUint64(metadata[8:], uint64(interval))
	checksum := crc32.ChecksumIEEE([]byte(epoch))
	checksum = crc32.Update(checksum, crc32.IEEETable, metadata[:])
	return crc32.Update(checksum, crc32.IEEETable, raw)
}

func (s Segment) MarshalBinary() ([]byte, error) {
	if s.Validate() != nil {
		return nil, ErrInvalid
	}
	histogram, err := s.Totals.Histogram.MarshalBinary()
	if err != nil {
		return nil, err
	}
	raw := []byte{'N', 'Q', 'S', 2}
	t := s.Totals
	for _, count := range []uint64{t.Observed, t.Success, t.Failure, t.NotExecuted, t.Unobserved, t.JitterPairs} {
		raw = binary.AppendUvarint(raw, count)
	}
	for _, count := range t.Causes {
		raw = binary.AppendUvarint(raw, count)
	}
	for _, count := range []uint64{t.Evidence.IPv4, t.Evidence.IPv6, t.Evidence.PacketSuccess, t.Evidence.PacketFailure, t.Evidence.Sent, t.Evidence.Received} {
		raw = binary.AppendUvarint(raw, count)
	}
	for _, value := range []float64{t.RTTSum, t.RTTMin, t.RTTMax, t.JitterSum} {
		raw = appendFloat(raw, value)
	}
	if t.Observed > 0 {
		raw = binary.AppendUvarint(raw, uint64(s.First.Slot-s.Anchor))
		raw = binary.AppendUvarint(raw, uint64(s.Last.Slot-s.Anchor))
		var flags byte
		if s.First.Success {
			flags |= 1
		}
		if s.Last.Success {
			flags |= 2
		}
		raw = append(raw, flags)
		raw = appendFloat(raw, s.First.RTT)
		raw = appendFloat(raw, s.Last.RTT)
	}
	raw = binary.AppendUvarint(raw, uint64(len(histogram)))
	raw = append(raw, histogram...)
	raw = binary.LittleEndian.AppendUint32(raw, segmentChecksum(s.Epoch, s.Anchor, s.Interval, raw))
	if len(raw) > MaxSegmentBytes {
		return nil, ErrInvalid
	}
	return raw, nil
}

type statReader struct {
	raw    []byte
	offset int
}

func (r *statReader) uint() (uint64, error) {
	value, n := binary.Uvarint(r.raw[r.offset:])
	if n <= 0 {
		return 0, ErrInvalid
	}
	r.offset += n
	return value, nil
}
func (r *statReader) float() (float64, error) {
	if len(r.raw)-r.offset < 8 {
		return 0, ErrInvalid
	}
	value := math.Float64frombits(binary.LittleEndian.Uint64(r.raw[r.offset:]))
	r.offset += 8
	return value, nil
}

func DecodeSegment(epoch string, anchor, interval int64, raw []byte) (Segment, error) {
	s, err := NewSegment(epoch, anchor, interval)
	if err != nil {
		return s, err
	}
	if len(raw) < 8 || len(raw) > MaxSegmentBytes || string(raw[:3]) != "NQS" || raw[3] != 1 && raw[3] != 2 {
		return Segment{}, ErrInvalid
	}
	if segmentChecksum(epoch, anchor, interval, raw[:len(raw)-4]) != binary.LittleEndian.Uint32(raw[len(raw)-4:]) {
		return Segment{}, ErrInvalid
	}
	raw = raw[:len(raw)-4]
	version := raw[3]
	r := statReader{raw: raw, offset: 4}
	t := &s.Totals
	for _, count := range []*uint64{&t.Observed, &t.Success, &t.Failure, &t.NotExecuted, &t.Unobserved, &t.JitterPairs} {
		*count, err = r.uint()
		if err != nil {
			return Segment{}, err
		}
	}
	for i := range t.Causes {
		if version == 1 && i >= LegacyCauseCount {
			break
		}
		t.Causes[i], err = r.uint()
		if err != nil {
			return Segment{}, err
		}
	}
	if version == 2 {
		for _, count := range []*uint64{&t.Evidence.IPv4, &t.Evidence.IPv6, &t.Evidence.PacketSuccess, &t.Evidence.PacketFailure, &t.Evidence.Sent, &t.Evidence.Received} {
			*count, err = r.uint()
			if err != nil {
				return Segment{}, err
			}
		}
	}
	for _, value := range []*float64{&t.RTTSum, &t.RTTMin, &t.RTTMax, &t.JitterSum} {
		*value, err = r.float()
		if err != nil {
			return Segment{}, err
		}
	}
	if t.Observed > 0 {
		first, err := r.uint()
		if err != nil || first > uint64(math.MaxInt64-anchor) {
			return Segment{}, ErrInvalid
		}
		last, err := r.uint()
		if err != nil || last > uint64(math.MaxInt64-anchor) {
			return Segment{}, ErrInvalid
		}
		if r.offset >= len(raw) || raw[r.offset] > 3 {
			return Segment{}, ErrInvalid
		}
		flags := raw[r.offset]
		r.offset++
		s.First = Boundary{Slot: anchor + int64(first), Success: flags&1 != 0}
		s.Last = Boundary{Slot: anchor + int64(last), Success: flags&2 != 0}
		s.First.RTT, err = r.float()
		if err != nil {
			return Segment{}, err
		}
		s.Last.RTT, err = r.float()
		if err != nil {
			return Segment{}, err
		}
	}
	size, err := r.uint()
	if err != nil || size > uint64(len(raw)-r.offset) || int(size) != len(raw)-r.offset {
		return Segment{}, ErrInvalid
	}
	t.Histogram, err = DecodeHistogram(raw[r.offset:])
	if err != nil {
		return Segment{}, err
	}
	if s.Validate() != nil {
		return Segment{}, ErrInvalid
	}
	return s, nil
}
