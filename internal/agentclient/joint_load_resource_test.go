package agentclient

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	ProgressWriteErrors                      int
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

func startJointResourceSampler(ctx context.Context, started time.Time, interval time.Duration, read func() jointResourceReading, publish func(jointResourceObservation) error) *jointResourceSampler {
	ctx, cancel := context.WithCancel(ctx)
	s := &jointResourceSampler{cancel: cancel, done: make(chan struct{}), result: jointResourceObservation{TargetIntervalSeconds: interval.Seconds()}}
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		var previous, lastTimeline time.Time
		var lastPoint jointResourcePoint
		sample := func(final bool) {
			now := time.Now()
			r := read()
			s.mu.Lock()
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
			checkpoint := final || lastTimeline.IsZero() || now.Sub(lastTimeline) >= 30*time.Second
			if checkpoint {
				s.result.Timeline = append(s.result.Timeline, lastPoint)
				lastTimeline = now
			}
			observation := s.result
			observation.Timeline = append([]jointResourcePoint(nil), s.result.Timeline...)
			s.mu.Unlock()
			if checkpoint && publish != nil {
				if err := publish(observation); err != nil {
					s.mu.Lock()
					s.result.ProgressWriteErrors++
					s.mu.Unlock()
				}
			}
		}
		sample(false)
		for {
			select {
			case <-ctx.Done():
				sample(true)
				return
			case <-ticker.C:
				sample(false)
			}
		}
	}()
	return s
}

func writeJointResourceProgress(output string, nodes int, mode string, started time.Time, observation jointResourceObservation) error {
	raw, err := json.MarshalIndent(struct {
		Format                string `json:"format"`
		Status                string `json:"status"`
		StartedAt, ObservedAt time.Time
		Observation           jointResourceObservation
		Timeline              []jointResourcePoint
	}{"myprobe.joint-resource-progress.v1", "observation-only", started.UTC(), time.Now().UTC(), observation, observation.Timeline}, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(output, fmt.Sprintf("joint-resource-%d-%s.json", nodes, mode))
	file, err := os.OpenFile(path+".tmp", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(append(raw, '\n'))
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(path+".tmp", path)
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
