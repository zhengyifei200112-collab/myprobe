package scheduler

import (
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/diagnostics"
)

type CycleDiagnostics struct {
	ScheduledAt       time.Time `json:"scheduled_at"`
	StartDelaySeconds float64   `json:"start_delay_seconds"`
	Assignments       int       `json:"assignments"`
	AssignmentsLoaded bool      `json:"assignments_loaded"`
	Due               int       `json:"due"`
	Dispatched        int       `json:"dispatched"`
	Offline           int       `json:"offline"`
	Failed            int       `json:"failed"`
}

type Snapshot struct {
	ObservationScope string                  `json:"observation_scope"`
	Job              diagnostics.JobSnapshot `json:"job"`
	LastCycle        *CycleDiagnostics       `json:"last_cycle,omitempty"`
}

func (s *Scheduler) Diagnostics() Snapshot {
	s.diagnosticsMu.Lock()
	defer s.diagnosticsMu.Unlock()
	result := Snapshot{ObservationScope: "process", Job: s.cycleJob.Snapshot()}
	if s.lastCycle != nil {
		cycle := *s.lastCycle
		result.LastCycle = &cycle
	}
	return result
}
