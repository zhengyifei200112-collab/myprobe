package agentclient

import (
	"context"
	"os"
	"sync"
	"time"
)

// The sampler never reads the mutable process controller. Restart publishes an
// immutable observation target, so resource reads cannot race with start/stop.
type jointResourceTarget struct {
	mu               sync.RWMutex
	pid              int
	cgroup, database string
	cpuOffset        uint64
}

func (r *jointResourceTarget) set(s *jointServer, cpuOffset uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pid, r.cgroup, r.cpuOffset = 0, "", cpuOffset
	if s != nil {
		r.pid, r.cgroup, r.database = s.pid, s.cgroup, s.database
	}
}

type jointResourceReading struct {
	rss, wal        int64
	cpu             uint64
	serverAvailable bool
}

func (r *jointResourceTarget) read() jointResourceReading {
	r.mu.RLock()
	s := jointServer{pid: r.pid, cgroup: r.cgroup, database: r.database}
	offset := r.cpuOffset
	r.mu.RUnlock()
	reading := jointResourceReading{rss: s.rssBytes(), cpu: offset + s.cpuMicros()}
	reading.serverAvailable = reading.rss > 0
	if stat, err := os.Stat(s.database + "-wal"); err == nil {
		reading.wal = stat.Size()
	}
	return reading
}

type jointResourceObservation struct {
	Samples, ServerUnavailableSamples        int
	TargetIntervalSeconds, MaximumGapSeconds float64
	PeakRSSBytes, PeakWALBytes               int64
	Timeline                                 []jointResourcePoint `json:"-"`
}

type jointResourceSampler struct {
	cancel context.CancelFunc
	done   chan struct{}
	mu     sync.Mutex
	result jointResourceObservation
}

func startJointResourceSampler(ctx context.Context, started time.Time, interval time.Duration, read func() jointResourceReading) *jointResourceSampler {
	ctx, cancel := context.WithCancel(ctx)
	s := &jointResourceSampler{cancel: cancel, done: make(chan struct{}), result: jointResourceObservation{TargetIntervalSeconds: interval.Seconds()}}
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		var previous, lastTimeline time.Time
		var lastPoint jointResourcePoint
		sample := func() {
			now := time.Now()
			r := read()
			s.mu.Lock()
			defer s.mu.Unlock()
			s.result.Samples++
			if !r.serverAvailable {
				s.result.ServerUnavailableSamples++
			}
			if !previous.IsZero() {
				s.result.MaximumGapSeconds = max(s.result.MaximumGapSeconds, now.Sub(previous).Seconds())
			}
			previous = now
			s.result.PeakRSSBytes = max(s.result.PeakRSSBytes, r.rss)
			s.result.PeakWALBytes = max(s.result.PeakWALBytes, r.wal)
			lastPoint = jointResourcePoint{ElapsedSeconds: now.Sub(started).Seconds(), RSSBytes: r.rss, CPUMicroseconds: r.cpu, SampledPeakWALBytes: s.result.PeakWALBytes}
			if lastTimeline.IsZero() || now.Sub(lastTimeline) >= 30*time.Second {
				s.result.Timeline = append(s.result.Timeline, lastPoint)
				lastTimeline = now
			}
		}
		sample()
		for {
			select {
			case <-ctx.Done():
				sample()
				s.mu.Lock()
				s.result.Timeline = append(s.result.Timeline, lastPoint)
				s.mu.Unlock()
				return
			case <-ticker.C:
				sample()
			}
		}
	}()
	return s
}

func (s *jointResourceSampler) stop() jointResourceObservation {
	s.cancel()
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	result := s.result
	result.Timeline = append([]jointResourcePoint(nil), result.Timeline...)
	return result
}
