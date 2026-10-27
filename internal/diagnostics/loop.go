package diagnostics

import (
	"sync"
	"time"
)

// Loop records entry/exit of the actual owning goroutine, separately from an
// old successful job. It is process-local evidence, not a persisted heartbeat.
type Loop struct {
	mu                     sync.Mutex
	active                 uint64
	started, stopped, next time.Time
}

type LoopSnapshot struct {
	Status           string     `json:"status"`
	ObservationScope string     `json:"observation_scope"`
	ActiveLoops      uint64     `json:"active_loops"`
	LastStartedAt    *time.Time `json:"last_started_at,omitempty"`
	LastStoppedAt    *time.Time `json:"last_stopped_at,omitempty"`
	NextRunAt        *time.Time `json:"next_run_at,omitempty"`
}

func (l *Loop) Begin() func() {
	l.mu.Lock()
	l.active++
	l.started = time.Now().UTC()
	l.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			l.active--
			l.stopped = time.Now().UTC()
			if l.active == 0 {
				l.next = time.Time{}
			}
		})
	}
}

func (l *Loop) Schedule(delay time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active > 0 {
		l.next = time.Now().UTC().Add(delay)
	}
}

func (l *Loop) Snapshot() LoopSnapshot {
	l.mu.Lock()
	defer l.mu.Unlock()
	r := LoopSnapshot{Status: "never_started", ObservationScope: "process", ActiveLoops: l.active}
	if !l.started.IsZero() {
		value := l.started
		r.LastStartedAt = &value
		r.Status = "not_running"
	}
	if !l.stopped.IsZero() {
		value := l.stopped
		r.LastStoppedAt = &value
	}
	if !l.next.IsZero() {
		value := l.next
		r.NextRunAt = &value
	}
	if l.active > 0 {
		r.Status = "running"
	}
	return r
}
