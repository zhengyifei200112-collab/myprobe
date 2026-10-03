package scheduler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/diagnostics"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

type assignmentStore interface {
	ListTargetAssignments(context.Context) ([]store.TargetAssignment, error)
}

type dispatcher interface {
	SendTask(context.Context, string, protocol.Task) error
}

type Scheduler struct {
	store         assignmentStore
	dispatcher    dispatcher
	logger        *slog.Logger
	next          map[string]time.Time
	diagnosticsMu sync.Mutex
	cycleJob      diagnostics.Job
	lastCycle     *CycleDiagnostics
}

func New(database assignmentStore, taskDispatcher dispatcher, logger *slog.Logger) *Scheduler {
	return &Scheduler{store: database, dispatcher: taskDispatcher, logger: logger, next: make(map[string]time.Time)}
}

func (s *Scheduler) Run(ctx context.Context) {
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
	assignments, err := s.store.ListTargetAssignments(ctx)
	if err != nil {
		cycleError = err
		if ctx.Err() == nil {
			s.logger.Warn("list latency assignments", "error", err)
		}
		return
	}
	cycle.Assignments = len(assignments)
	cycle.AssignmentsLoaded = true
	active := make(map[string]struct{}, len(assignments))
	for _, assignment := range assignments {
		key := assignment.NodeID + ":" + assignment.Target.ID
		active[key] = struct{}{}
		if due, exists := s.next[key]; exists && due.After(now) {
			continue
		}
		cycle.Due++
		port := 0
		if assignment.Target.Port != nil {
			port = *assignment.Target.Port
		}
		task := protocol.Task{
			ID: randomTaskID(), Kind: assignment.Target.Kind, TargetID: assignment.Target.ID,
			Host: assignment.Target.Host, Port: port, TimeoutMS: assignment.Target.TimeoutMS,
			ExpiresAt: now.Add(time.Duration(assignment.Target.TimeoutMS)*time.Millisecond + 15*time.Second),
		}
		if err := s.dispatcher.SendTask(ctx, assignment.NodeID, task); err != nil {
			if errors.Is(err, agentgateway.ErrAgentOffline) {
				cycle.Offline++
			} else {
				cycle.Failed++
				cycleError = err
			}
			if !errors.Is(err, agentgateway.ErrAgentOffline) && ctx.Err() == nil {
				s.logger.Warn("dispatch latency task", "node_id", assignment.NodeID, "target_id", assignment.Target.ID, "error", err)
			}
			continue
		}
		cycle.Dispatched++
		s.next[key] = now.Add(time.Duration(assignment.Target.IntervalSeconds) * time.Second)
	}
	for key := range s.next {
		if _, exists := active[key]; !exists {
			delete(s.next, key)
		}
	}
}

func randomTaskID() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(buffer)
}
