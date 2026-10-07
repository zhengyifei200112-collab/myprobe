package agentgateway

import (
	"context"
	"errors"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
	v1 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	v2 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v2"
)

var ErrHTTPUnavailable = errors.New("HTTP execution is unavailable on this session")
var ErrHTTPBusy = errors.New("HTTP session task limit reached")

// EnableHTTPProbes is an integration gate, off by default. Enabling applies to
// new handshakes; existing sessions must reconnect to negotiate the capability.
func (g *Gateway) EnableHTTPProbes(enabled bool) { g.httpEnabled.Store(enabled) }
func (g *Gateway) HTTPProbesEnabled() bool       { return g.httpEnabled.Load() }

func (g *Gateway) DispatchHTTP(ctx context.Context, nodeID, serviceID string, scheduledAt time.Time) error {
	g.sessionsMu.RLock()
	session := g.sessions[nodeID]
	g.sessionsMu.RUnlock()
	if session == nil {
		return ErrAgentOffline
	}
	if !g.httpEnabled.Load() || !session.httpAllowed {
		return ErrHTTPUnavailable
	}
	session.httpMu.Lock()
	defer session.httpMu.Unlock()
	now := time.Now().UTC()
	for id, expiry := range session.httpPending {
		if now.After(expiry) {
			delete(session.httpPending, id)
		}
	}
	if len(session.httpPending) >= 4 {
		return ErrHTTPBusy
	}
	task, err := g.store.CreateHTTPTask(ctx, serviceID, nodeID, scheduledAt, now)
	if err != nil {
		return err
	}
	envelope, err := v2.NewEnvelope(v2.TypeHTTPTask, 0, task)
	if err != nil {
		return err
	}
	writeCtx, cancel := context.WithDeadline(ctx, task.ExpiresAt)
	defer cancel()
	if err = session.write(writeCtx, v1.Envelope(envelope)); err != nil {
		return err
	}
	session.httpPending[task.ID] = task.ExpiresAt.Add(time.Minute)
	return nil
}

func (g *Gateway) saveHTTPResult(ctx context.Context, session *agentSession, nodeID string, result httpcheck.Result) error {
	// Serialize admission with replacement so an obsolete connection cannot
	// contribute a result after the new session becomes authoritative.
	g.sessionsMu.RLock()
	defer g.sessionsMu.RUnlock()
	if g.sessions[nodeID] != session {
		return ErrHTTPUnavailable
	}
	session.httpMu.Lock()
	defer session.httpMu.Unlock()
	if _, ok := session.httpPending[result.TaskID]; !ok {
		return ErrHTTPUnavailable
	}
	if err := g.store.SaveHTTPResult(ctx, nodeID, result, time.Now().UTC()); err != nil {
		return err
	}
	delete(session.httpPending, result.TaskID)
	return nil
}
