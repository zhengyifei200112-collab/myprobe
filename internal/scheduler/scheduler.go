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

type assignmentStore interface {
	ExpireNetworkDispatchLeases(context.Context, time.Time) error
	ListNetworkAssignments(context.Context, time.Time) ([]store.NetworkAssignment, error)
}

type dispatcher interface {
	DispatchNetwork(context.Context, string, time.Time) error
}

type Scheduler struct {
	store         assignmentStore
	dispatcher    dispatcher
	logger        *slog.Logger
	diagnosticsMu sync.Mutex
	cycleJob      diagnostics.Job
	lastCycle     *CycleDiagnostics
	running       atomic.Bool
}

func New(database assignmentStore, taskDispatcher dispatcher, logger *slog.Logger) *Scheduler {
	return &Scheduler{store: database, dispatcher: taskDispatcher, logger: logger}
}

func (s *Scheduler) Run(ctx context.Context) {
	s.running.Store(true)
	defer s.running.Store(false)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	s.dispatch(ctx, time.Now().UTC())
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.dispatch(ctx, now.UTC())
		}
	}
}

func (s *Scheduler) dispatch(ctx context.Context, now time.Time) {
	complete := s.cycleJob.Begin()
	var cycleError error
	cycle := CycleDiagnostics{ScheduledAt: now, StartDelaySeconds: max(0, time.Since(now).Seconds())}
	defer func() {
		if cycleError == nil {
			cycleError = ctx.Err()
		}
		s.diagnosticsMu.Lock()
		defer s.diagnosticsMu.Unlock()
		s.lastCycle = &cycle
		complete(cycleError)
	}()
	if err := s.store.ExpireNetworkDispatchLeases(ctx, now); err != nil {
		cycleError = err
		return
	}
	assignments, err := s.store.ListNetworkAssignments(ctx, now)
	if err != nil {
		cycleError = err
		if ctx.Err() == nil {
			s.logger.Warn("list latency assignments", "error", err)
		}
		return
	}
	cycle.Assignments = len(assignments)
	cycle.AssignmentsLoaded = true
	var workers sync.WaitGroup
	var cycleMu sync.Mutex
	jobs := make(chan store.NetworkAssignment)
	for range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for assignment := range jobs {
				err := s.dispatcher.DispatchNetwork(ctx, assignment.EpochID, assignment.ScheduledAt)
				cycleMu.Lock()
				switch {
				case err == nil:
					cycle.Dispatched++
				case errors.Is(err, agentgateway.ErrAgentOffline):
					cycle.Offline++
				case errors.Is(err, store.ErrNetworkTaskRejected), errors.Is(err, store.ErrNetworkTaskDispatched):
					cycle.Skipped++
				default:
					cycle.Failed++
					if cycleError == nil {
						cycleError = err
					}
				}
				cycleMu.Unlock()
				if err != nil && !errors.Is(err, agentgateway.ErrAgentOffline) && !errors.Is(err, store.ErrNetworkTaskRejected) && !errors.Is(err, store.ErrNetworkTaskDispatched) && ctx.Err() == nil {
					s.logger.Warn("dispatch latency task failed")
				}
			}
		}()
	}
	for _, assignment := range assignments {
		if !assignment.Eligible {
			continue
		}
		cycleMu.Lock()
		cycle.Due++
		cycleMu.Unlock()
		select {
		case jobs <- assignment:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(jobs)
	workers.Wait()
}
