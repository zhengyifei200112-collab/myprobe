package agentgateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/coder/websocket"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	v2 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v2"
)

func (g *Gateway) registerRuntimeSession(ctx context.Context, nodeID string, session *agentSession) (*v2.RuntimeConfiguration, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	session.epoch = hex.EncodeToString(nonce[:])
	// DB epoch and published session must advance in registration order. No
	// network I/O occurs while this registration fence is held.
	g.registrationMu.Lock()
	configuration, err := g.store.BeginAgentConnection(ctx, nodeID, session.epoch, session.version, session.configAllowed, time.Now().UTC())
	if err != nil {
		g.registrationMu.Unlock()
		return nil, err
	}
	g.sessionsMu.Lock()
	previous := g.sessions[nodeID]
	g.sessions[nodeID] = session
	g.sessionsMu.Unlock()
	g.registrationMu.Unlock()
	if configuration != nil {
		session.configRevision = configuration.Revision
	}
	if previous != nil && previous != session && previous.connection != nil {
		_ = previous.connection.Close(websocket.StatusPolicyViolation, "replaced by a newer connection")
	}
	return configuration, nil
}
func (g *Gateway) isCurrentSession(nodeID string, expected *agentSession) bool {
	g.sessionsMu.RLock()
	defer g.sessionsMu.RUnlock()
	return g.sessions[nodeID] == expected
}

// The epoch is returned only to the administrator API's internal fence check,
// never as a public connection or authentication identifier.
func (g *Gateway) RuntimeConnectionEpoch(nodeID string) (string, bool, bool) {
	if !g.sessionsMu.TryRLock() {
		return "", false, false
	}
	defer g.sessionsMu.RUnlock()
	session := g.sessions[nodeID]
	if session == nil {
		return "", false, true
	}
	return session.epoch, true, true
}

// Every configuration-writing path (single edit, batch, import) is observed by
// this connection-owned loop. Older peers receive no unnegotiated new frames.
func (g *Gateway) refreshSamplingConfiguration(ctx context.Context, nodeID string, session *agentSession) {
	if !session.configAllowed {
		return
	}
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if !g.isCurrentSession(nodeID, session) {
			return
		}
		operationCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		session.configMu.Lock()
		desired, err := g.store.DesiredSamplingConfiguration(operationCtx, nodeID)
		if err == nil && desired.Revision != session.configRevision {
			configuration, prepareErr := g.store.PrepareSamplingConfiguration(operationCtx, nodeID, session.epoch)
			if prepareErr == nil {
				envelope, envelopeErr := protocol.NewEnvelope(protocol.TypeConfiguration, 0, configuration)
				if envelopeErr == nil {
					err = session.write(operationCtx, envelope)
				} else {
					err = envelopeErr
				}
				sent := err == nil
				_ = g.store.CompleteSamplingDispatch(operationCtx, nodeID, session.epoch, configuration.Nonce, sent, time.Now().UTC())
				if sent {
					session.configRevision = configuration.Revision
				}
			}
		}
		session.configMu.Unlock()
		cancel()
	}
}
