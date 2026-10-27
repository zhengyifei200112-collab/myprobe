package scheduler

import (
	"github.com/zhengyifei200112-collab/myprobe/internal/diagnostics"
	"time"
)

type HTTPCycleDiagnostics struct {
	ScheduledAt       time.Time `json:"scheduled_at"`
	StartDelaySeconds float64   `json:"start_delay_seconds"`
	Assignments       int       `json:"assignments"`
	AssignmentsLoaded bool      `json:"assignments_loaded"`
	Due               int       `json:"due"`
	Dispatched        int       `json:"dispatched"`
	Offline           int       `json:"offline"`
	Unavailable       int       `json:"unavailable"`
	Busy              int       `json:"busy"`
	Rejected          int       `json:"rejected"`
	NotQueued         int       `json:"not_queued"`
	Failed            int       `json:"failed"`
}

type HTTPSnapshot struct {
	Running          bool                    `json:"running"`
	ObservationScope string                  `json:"observation_scope"`
	Job              diagnostics.JobSnapshot `json:"job"`
	LastCycle        *HTTPCycleDiagnostics   `json:"last_cycle,omitempty"`
}

func (s *HTTPScheduler) Diagnostics() HTTPSnapshot {
	s.diagnosticsMu.Lock()
	defer s.diagnosticsMu.Unlock()
	r := HTTPSnapshot{Running: s.running.Load(), ObservationScope: "process", Job: s.cycleJob.Snapshot()}
	if s.lastCycle != nil {
		value := *s.lastCycle
		r.LastCycle = &value
	}
	return r
}
