package networkquality

import "math"

type Outcome string

const (
	Success     Outcome = "success"
	Failure     Outcome = "failure"
	NotExecuted Outcome = "not_executed"
	Unobserved  Outcome = "unobserved"
)

var CauseNames = [...]string{"timeout", "dns", "refused", "unreachable", "busy", "unsupported", "invalid_task", "offline", "write_error", "dispatch_unknown", "missing"}

type Totals struct {
	Observed, Success, Failure, NotExecuted, Unobserved uint64
	RTTSum, RTTMin, RTTMax                              float64
	JitterSum                                           float64
	JitterPairs                                         uint64
	Causes                                              [len(CauseNames)]uint64
	Histogram                                           Histogram
}

type Boundary struct {
	Slot    int64
	Success bool
	RTT     float64
}

// One Segment belongs to one immutable epoch. Its boundary witnesses allow
// cross-bucket jitter without bridging a failed or absent planned observation.
type Segment struct {
	Epoch            string
	Anchor, Interval int64
	First, Last      Boundary
	Totals           Totals
}

func NewSegment(epoch string, anchor, interval int64) (Segment, error) {
	if epoch == "" || len(epoch) > 128 || anchor < 0 || interval < 5 || interval > 86400 {
		return Segment{}, ErrInvalid
	}
	return Segment{Epoch: epoch, Anchor: anchor, Interval: interval}, nil
}

func causeIndex(outcome Outcome, reason string) int {
	for i, name := range CauseNames {
		if name == reason {
			if (outcome == Failure && i < 4) || (outcome == NotExecuted && i >= 4 && i < 7) || (outcome == Unobserved && i >= 7) {
				return i
			}
		}
	}
	return -1
}

func (s *Segment) Add(slot int64, outcome Outcome, rtt float64, reason string) error {
	if s.Epoch == "" || s.Interval < 5 || slot < s.Anchor || (slot-s.Anchor)%s.Interval != 0 || (s.Totals.Observed > 0 && slot <= s.Last.Slot) || s.Totals.Observed == ^uint64(0) {
		return ErrInvalid
	}
	index := -1
	if outcome == Success {
		if reason != "" || !validRTT(rtt) {
			return ErrInvalid
		}
	} else {
		index = causeIndex(outcome, reason)
		if index < 0 || rtt != 0 {
			return ErrInvalid
		}
	}
	boundary := Boundary{Slot: slot, Success: outcome == Success, RTT: rtt}
	if outcome == Success {
		if err := s.Totals.Histogram.Add(rtt); err != nil {
			return err
		}
		if s.Totals.Success == 0 {
			s.Totals.RTTMin, s.Totals.RTTMax = rtt, rtt
		} else {
			s.Totals.RTTMin = min(s.Totals.RTTMin, rtt)
			s.Totals.RTTMax = max(s.Totals.RTTMax, rtt)
		}
		s.Totals.RTTSum += rtt
		if s.Totals.Observed > 0 && s.Last.Success && slot-s.Last.Slot == s.Interval {
			s.Totals.JitterPairs++
			s.Totals.JitterSum += math.Abs(rtt - s.Last.RTT)
		}
		s.Totals.Success++
	} else {
		s.Totals.Causes[index]++
		switch outcome {
		case Failure:
			s.Totals.Failure++
		case NotExecuted:
			s.Totals.NotExecuted++
		case Unobserved:
			s.Totals.Unobserved++
		}
	}
	if s.Totals.Observed == 0 {
		s.First = boundary
	}
	s.Last = boundary
	s.Totals.Observed++
	return nil
}

// MergeEpoch appends strictly later data from the same epoch, preserving gap
// breaks and combining sample weights rather than bucket-level percentages.
func (s *Segment) MergeEpoch(other Segment) error {
	if s.Epoch != other.Epoch || s.Anchor != other.Anchor || s.Interval != other.Interval || s.Validate() != nil || other.Validate() != nil {
		return ErrInvalid
	}
	if other.Totals.Observed == 0 {
		return nil
	}
	if s.Totals.Observed > 0 && other.First.Slot <= s.Last.Slot {
		return ErrInvalid
	}
	cross := s.Totals.Observed > 0 && s.Last.Success && other.First.Success && other.First.Slot-s.Last.Slot == s.Interval
	if ^uint64(0)-s.Totals.Observed < other.Totals.Observed {
		return ErrInvalid
	}
	first := s.First
	if s.Totals.Observed == 0 {
		first = other.First
	}
	if err := s.Totals.Merge(other.Totals); err != nil {
		return err
	}
	if cross {
		s.Totals.JitterPairs++
		s.Totals.JitterSum += math.Abs(other.First.RTT - s.Last.RTT)
	}
	s.First, s.Last = first, other.Last
	return nil
}

// Merge combines populations without implying adjacency between epochs.
func (t *Totals) Merge(other Totals) error {
	if ^uint64(0)-t.Observed < other.Observed {
		return ErrInvalid
	}
	if err := t.Histogram.Merge(other.Histogram); err != nil {
		return err
	}
	if other.Success > 0 {
		if t.Success == 0 {
			t.RTTMin, t.RTTMax = other.RTTMin, other.RTTMax
		} else {
			t.RTTMin = min(t.RTTMin, other.RTTMin)
			t.RTTMax = max(t.RTTMax, other.RTTMax)
		}
	}
	t.Observed += other.Observed
	t.Success += other.Success
	t.Failure += other.Failure
	t.NotExecuted += other.NotExecuted
	t.Unobserved += other.Unobserved
	t.RTTSum += other.RTTSum
	t.JitterSum += other.JitterSum
	t.JitterPairs += other.JitterPairs
	for i, count := range other.Causes {
		t.Causes[i] += count
	}
	return nil
}

func (s Segment) Validate() error {
	if _, err := NewSegment(s.Epoch, s.Anchor, s.Interval); err != nil {
		return err
	}
	t := s.Totals
	var sum uint64
	for _, count := range []uint64{t.Success, t.Failure, t.NotExecuted, t.Unobserved} {
		if ^uint64(0)-sum < count {
			return ErrInvalid
		}
		sum += count
	}
	if sum != t.Observed || t.Histogram.Count() != t.Success {
		return ErrInvalid
	}
	for _, group := range []struct {
		start, end int
		expected   uint64
	}{{0, 4, t.Failure}, {4, 7, t.NotExecuted}, {7, len(CauseNames), t.Unobserved}} {
		var count uint64
		for _, value := range t.Causes[group.start:group.end] {
			if ^uint64(0)-count < value {
				return ErrInvalid
			}
			count += value
		}
		if count != group.expected {
			return ErrInvalid
		}
	}
	if math.IsNaN(t.RTTSum) || math.IsInf(t.RTTSum, 0) || math.IsNaN(t.JitterSum) || math.IsInf(t.JitterSum, 0) || t.RTTSum < 0 || t.JitterSum < 0 {
		return ErrInvalid
	}
	if t.Success == 0 {
		if t.RTTSum != 0 || t.RTTMin != 0 || t.RTTMax != 0 || t.JitterPairs != 0 || t.JitterSum != 0 {
			return ErrInvalid
		}
	} else {
		if !validRTT(t.RTTMin) || !validRTT(t.RTTMax) || t.RTTMin > t.RTTMax || t.JitterPairs > t.Success-1 {
			return ErrInvalid
		}
		epsilon := 1e-9 * max(1, t.RTTSum)
		if t.RTTSum+epsilon < float64(t.Success)*t.RTTMin || t.RTTSum-epsilon > float64(t.Success)*t.RTTMax || t.JitterSum > float64(t.JitterPairs)*MaxLatencyMS+epsilon {
			return ErrInvalid
		}
	}
	if t.Observed == 0 {
		if s.First != (Boundary{}) || s.Last != (Boundary{}) {
			return ErrInvalid
		}
		return nil
	}
	if s.First.Slot < s.Anchor || s.Last.Slot < s.First.Slot || (s.First.Slot-s.Anchor)%s.Interval != 0 || (s.Last.Slot-s.Anchor)%s.Interval != 0 || t.Observed > uint64((s.Last.Slot-s.First.Slot)/s.Interval)+1 {
		return ErrInvalid
	}
	for _, boundary := range []Boundary{s.First, s.Last} {
		if boundary.Success {
			if t.Success == 0 || !validRTT(boundary.RTT) {
				return ErrInvalid
			}
		} else if boundary.RTT != 0 {
			return ErrInvalid
		}
	}
	if t.Observed == 1 && s.First != s.Last {
		return ErrInvalid
	}
	if s.First.Success && s.Last.Success && s.First.Slot != s.Last.Slot && t.Success < 2 {
		return ErrInvalid
	}
	return nil
}
