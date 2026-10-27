package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/diagnostics"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

type httpAssignmentStore interface {
	ListHTTPAssignments(context.Context) ([]store.HTTPAssignment, error)
}
type httpDispatcher interface {
	DispatchHTTP(context.Context, string, string, time.Time) error
}

type httpSlotKey struct {
	service, node string
	revision      int64
}

// HTTPScheduler has one owning Run goroutine and a bounded dispatch worker pool.
type HTTPScheduler struct {
	store         httpAssignmentStore
	dispatcher    httpDispatcher
	logger        *slog.Logger
	last          map[httpSlotKey]time.Time
	running       atomic.Bool
	diagnosticsMu sync.Mutex
	cycleJob      diagnostics.Job
	lastCycle     *HTTPCycleDiagnostics
}

func NewHTTP(database httpAssignmentStore, dispatcher httpDispatcher, logger *slog.Logger) *HTTPScheduler {
	return &HTTPScheduler{store: database, dispatcher: dispatcher, logger: logger, last: make(map[httpSlotKey]time.Time)}
}

func (s *HTTPScheduler) Run(ctx context.Context) {
	s.running.Store(true)
	defer s.running.Store(false)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	now := time.Now().UTC()
	s.dispatchAt(ctx, now, now)
	for {
		select {
		case <-ctx.Done():
			return
		case scheduledAt := <-ticker.C:
			if ctx.Err() != nil {
				return
			}
			s.dispatchAt(ctx, scheduledAt.UTC(), time.Now().UTC())
		}
	}
}

func (s *HTTPScheduler) dispatch(ctx context.Context, now time.Time) {
	s.dispatchAt(ctx, now, now)
}

// The ticker time measures delay; the actual cycle time chooses live HTTP slots.
func (s *HTTPScheduler) dispatchAt(ctx context.Context, scheduledAt, now time.Time) {
	complete := s.cycleJob.Begin()
	cycle := HTTPCycleDiagnostics{ScheduledAt: scheduledAt, StartDelaySeconds: max(0, now.Sub(scheduledAt).Seconds())}
	var cycleError error
	var cycleMu sync.Mutex
	defer func() {
		if cycleError == nil {
			cycleError = ctx.Err()
		}
		s.diagnosticsMu.Lock()
		defer s.diagnosticsMu.Unlock()
		s.lastCycle = &cycle
		complete(cycleError)
	}()
	items, err := s.store.ListHTTPAssignments(ctx)
	if err != nil {
		cycleError = err
		if ctx.Err() == nil {
			s.logger.Warn("list HTTP assignments failed")
		}
		return
	}
	cycle.AssignmentsLoaded, cycle.Assignments = true, len(items)
	type work struct {
		assignment store.HTTPAssignment
		slot       time.Time
	}
	jobs := make(chan work)
	var workers sync.WaitGroup
	for i := 0; i < 4; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for job := range jobs {
				// A queued job cannot outlive its dispatch window.
				deadline := job.slot.Add(5 * time.Second)
				jobCtx, cancel := context.WithDeadline(ctx, deadline)
				err := jobCtx.Err()
				if err == nil {
					err = s.dispatcher.DispatchHTTP(jobCtx, job.assignment.NodeID, job.assignment.ServiceID, job.slot)
				}
				cancel()
				cycleMu.Lock()
				switch {
				case err == nil:
					cycle.Dispatched++
				case errors.Is(err, agentgateway.ErrAgentOffline):
					cycle.Offline++
				case errors.Is(err, agentgateway.ErrHTTPUnavailable):
					cycle.Unavailable++
				case errors.Is(err, agentgateway.ErrHTTPBusy):
					cycle.Busy++
				case errors.Is(err, store.ErrHTTPTaskRejected):
					cycle.Rejected++
				default:
					cycle.Failed++
					if cycleError == nil {
						cycleError = err
					}
				}
				cycleMu.Unlock()
				if err != nil && ctx.Err() == nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, agentgateway.ErrAgentOffline) && !errors.Is(err, agentgateway.ErrHTTPUnavailable) && !errors.Is(err, agentgateway.ErrHTTPBusy) && !errors.Is(err, store.ErrHTTPTaskRejected) {
					s.logger.Warn("dispatch HTTP task failed", "service_id", job.assignment.ServiceID, "node_id", job.assignment.NodeID)
				}
			}
		}()
	}
	active := make(map[httpSlotKey]bool, len(items))
	for _, item := range items {
		key := httpSlotKey{item.ServiceID, item.NodeID, item.Revision}
		active[key] = true
		if item.IntervalSeconds < 30 || item.IntervalSeconds > 86400 || now.Before(item.Anchor) {
			continue
		}
		period := time.Duration(item.IntervalSeconds) * time.Second
		slot := item.Anchor.Add(now.Sub(item.Anchor) / period * period)
		if now.Sub(slot) >= 5*time.Second || !slot.After(s.last[key]) {
			continue
		}
		// Failed/offline attempts are not retried each second and never turn into
		// a false service failure. Historical slots are not replayed after downtime.
		s.last[key] = slot
		cycle.Due++
		select {
		case jobs <- work{item, slot}:
		case <-ctx.Done():
			cycle.NotQueued++
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(jobs)
	workers.Wait()
	for key := range s.last {
		if !active[key] {
			delete(s.last, key)
		}
	}
}
