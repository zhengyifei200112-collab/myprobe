// Package diagnostics records bounded, process-local operational evidence without
// retaining raw errors, payloads or credentials.
package diagnostics

import (
	"context"
	"errors"
	"sync"
	"time"
)

type JobSnapshot struct {
	State               string     `json:"state"`
	ActiveRuns          uint64     `json:"active_runs"`
	CompletedRuns       uint64     `json:"completed_runs"`
	FailedRuns          uint64     `json:"failed_runs"`
	CancelledRuns       uint64     `json:"cancelled_runs"`
	LastStartedAt       *time.Time `json:"last_started_at,omitempty"`
	LastCompletedAt     *time.Time `json:"last_completed_at,omitempty"`
	LastSuccessAt       *time.Time `json:"last_success_at,omitempty"`
	LastResult          string     `json:"last_result,omitempty"`
	LastErrorCode       string     `json:"last_error_code,omitempty"`
	LastDurationSeconds *float64   `json:"last_duration_seconds,omitempty"`
}

// Job's zero value is ready to use. It must not be copied after first use.
type Job struct {
	mu                                   sync.Mutex
	active, completed, failed, cancelled uint64
	startedAt, completedAt, successAt    time.Time
	result, errorCode                    string
	duration                             float64
}

// Begin returns an idempotent completion function. Call it with the actual result
// of the operation, not the outcome of a later, different operation (for example,
// file generation is not proof of successful download or recovery).
func (j *Job) Begin() func(error) {
	started := time.Now()
	j.mu.Lock()
	j.active++
	j.startedAt = started.UTC()
	j.mu.Unlock()
	var once sync.Once
	return func(err error) {
		once.Do(func() {
			finished := time.Now()
			j.mu.Lock()
			defer j.mu.Unlock()
			j.active--
			j.completed++
			j.completedAt = finished.UTC()
			j.duration = max(0, finished.Sub(started).Seconds())
			j.result, j.errorCode = "success", ""
			if err == nil {
				j.successAt = finished.UTC()
			} else if errors.Is(err, context.Canceled) {
				j.result, j.errorCode = "cancelled", "cancelled"
				j.cancelled++
			} else {
				j.result, j.errorCode = "failed", "operation_failed"
				j.failed++
				if errors.Is(err, context.DeadlineExceeded) {
					j.errorCode = "deadline_exceeded"
				}
			}
		})
	}
}

func (j *Job) Snapshot() JobSnapshot {
	j.mu.Lock()
	defer j.mu.Unlock()
	result := JobSnapshot{
		State: "never_run", ActiveRuns: j.active, CompletedRuns: j.completed,
		FailedRuns: j.failed, CancelledRuns: j.cancelled, LastResult: j.result,
		LastErrorCode: j.errorCode,
	}
	if !j.startedAt.IsZero() {
		started := j.startedAt
		result.LastStartedAt = &started
	}
	if !j.completedAt.IsZero() {
		finished, duration := j.completedAt, j.duration
		result.LastCompletedAt, result.LastDurationSeconds = &finished, &duration
		result.State = j.result
	}
	if !j.successAt.IsZero() {
		success := j.successAt
		result.LastSuccessAt = &success
	}
	if j.active > 0 {
		result.State = "running"
	}
	return result
}
