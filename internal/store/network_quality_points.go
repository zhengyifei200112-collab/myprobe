package store

import (
	"time"

	quality "github.com/zhengyifei200112-collab/myprobe/internal/networkquality"
)

const MaxNetworkQualityPoints = 2000

type NetworkQualityPoint struct {
	Time, End time.Time
	Stats     NetworkQualityStats
}

// UTC buckets share one grid across all observers. All retained source buckets
// must fit without proportional histogram splitting or invented observations.
func networkQualityPointSize(start, end time.Time, minimum int64) (int64, error) {
	for _, size := range []int64{60, 300, 600, 900, 1800, 3600, 7200, 10800, 21600, 43200, 86400} {
		if size < minimum {
			continue
		}
		first := start.Unix() / size * size
		n := (ceilNetworkSecond(end.UnixNano()) - first + size - 1) / size
		if n > 0 && n <= MaxNetworkQualityPoints {
			return size, nil
		}
	}
	return 0, quality.ErrInvalid
}

type networkPointPopulation struct {
	samples     []float64
	approximate bool
}

type networkPointCollector struct {
	points      []NetworkQualityPoint
	populations []networkPointPopulation
	segments    map[int]*quality.Segment
	first, size int64
}

func newNetworkPointCollector(q NetworkQualityQuery, result NetworkQualityStats) (*networkPointCollector, error) {
	if !q.Series {
		return nil, nil
	}
	end := min(q.End.UnixNano(), q.AsOf.UnixNano())
	if end <= q.Start.UnixNano() {
		return &networkPointCollector{}, nil
	}
	size, err := networkQualityPointSize(q.Start, time.Unix(0, end), result.GranularitySeconds)
	if err != nil {
		return nil, err
	}
	c := &networkPointCollector{first: q.Start.Unix() / size * size, size: size}
	for at := c.first; at < ceilNetworkSecond(end); at += size {
		startNS := max(q.Start.UnixNano(), at*int64(time.Second))
		endNS := end
		if at+size < ceilNetworkSecond(end) {
			endNS = (at + size) * int64(time.Second)
		}
		p := NetworkQualityPoint{Time: time.Unix(0, startNS).UTC(), End: time.Unix(0, endNS).UTC(), Stats: NetworkQualityStats{RequestedStart: time.Unix(0, startNS).UTC(), RequestedEnd: time.Unix(0, endNS).UTC(), AvailableFrom: result.AvailableFrom, GranularitySeconds: result.GranularitySeconds, QuantileMethod: "unavailable", Availability: "untracked_history"}}
		if result.Availability != "untracked_history" && p.End.After(result.AvailableFrom) {
			p.Stats.Availability = "resolution_clipped"
		}
		a, b := max(startNS, result.EffectiveStart.UnixNano()), min(endNS, result.EffectiveEnd.UnixNano())
		if b > a {
			p.Stats.EffectiveStart, p.Stats.EffectiveEnd = time.Unix(0, a).UTC(), time.Unix(0, b).UTC()
			p.Stats.Availability = "available"
			if a != startNS || b != endNS {
				p.Stats.Availability = result.Availability
			}
		}
		c.points = append(c.points, p)
		c.populations = append(c.populations, networkPointPopulation{})
	}
	return c, nil
}

func (c *networkPointCollector) plan(epoch string, anchor, interval, start, end, mature int64) {
	if c == nil {
		return
	}
	c.segments = make(map[int]*quality.Segment)
	if end <= start {
		return
	}
	first := max(int64(0), (start/int64(time.Second)-c.first)/c.size)
	last := min(int64(len(c.points)), (ceilNetworkSecond(end)-c.first+c.size-1)/c.size)
	for i := first; i < last; i++ {
		p := &c.points[i].Stats
		a, b := max(start, p.EffectiveStart.UnixNano()), min(end, p.EffectiveEnd.UnixNano())
		if !networkQualityPointAvailable(p.Availability) || b <= a {
			continue
		}
		n := networkSlotCount(anchor, interval, a, min(b, mature))
		p.Planned += n
		p.Pending += networkSlotCount(anchor, interval, a, b) - n
	}
}

func (c *networkPointCollector) segment(epoch string, anchor, interval, slot int64) (int, *quality.Segment, error) {
	index := int((slot - c.first) / c.size)
	if slot < c.first || index < 0 || index >= len(c.points) {
		return 0, nil, ErrNetworkQualityUnavailable
	}
	segment := c.segments[index]
	if segment == nil {
		part, err := quality.NewSegment(epoch, anchor, interval)
		if err != nil {
			return 0, nil, ErrNetworkQualityUnavailable
		}
		segment = &part
		c.segments[index] = segment
	}
	return index, segment, nil
}

func (c *networkPointCollector) raw(epoch string, anchor, interval, slot int64, value networkQualityValue) error {
	if c == nil {
		return nil
	}
	i, segment, err := c.segment(epoch, anchor, interval, slot)
	if err != nil {
		return err
	}
	if err = segment.Add(slot, value.outcome, value.rtt, value.reason); err != nil {
		return ErrNetworkQualityUnavailable
	}
	if value.outcome == quality.Success {
		c.populations[i].samples = append(c.populations[i].samples, value.rtt)
	}
	return nil
}

func (c *networkPointCollector) rollup(part quality.Segment) error {
	if c == nil {
		return nil
	}
	i, segment, err := c.segment(part.Epoch, part.Anchor, part.Interval, part.First.Slot)
	if err != nil {
		return err
	}
	if (part.Last.Slot-c.first)/c.size != int64(i) {
		return ErrNetworkQualityUnavailable
	}
	if err = segment.MergeEpoch(part); err != nil {
		return ErrNetworkQualityUnavailable
	}
	c.populations[i].approximate = true
	return nil
}

func (c *networkPointCollector) finishEpoch() error {
	if c == nil {
		return nil
	}
	for i, s := range c.segments {
		if err := c.points[i].Stats.Totals.Merge(s.Totals); err != nil {
			return ErrNetworkQualityUnavailable
		}
	}
	return nil
}

func (c *networkPointCollector) finish(result *NetworkQualityStats) error {
	if c == nil {
		return nil
	}
	for i := range c.points {
		p := &c.points[i].Stats
		if !networkQualityPointAvailable(p.Availability) {
			continue
		}
		if err := finishNetworkQuality(p, c.populations[i].samples, c.populations[i].approximate); err != nil {
			return err
		}
	}
	result.BucketSeconds, result.Points = c.size, c.points
	return nil
}

func networkQualityPointAvailable(availability string) bool {
	return availability == "available" || availability == "partial_known_history" || availability == "partial_resolution"
}
