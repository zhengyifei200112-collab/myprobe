package agentgateway

import "time"

// GatewayDiagnostics describes process-local transport state, not node health
// or a durable scheduler queue. Counts are sampled under their respective locks.
type GatewayDiagnostics struct {
	ObservedAt       time.Time `json:"observed_at"`
	AgentConnections int       `json:"agent_connections"`
	PendingResults   int       `json:"pending_results"`
	ExpiredResults   int       `json:"expired_results"`
}

func (g *Gateway) Diagnostics() GatewayDiagnostics {
	return g.diagnosticsAt(time.Now().UTC())
}

func (g *Gateway) diagnosticsAt(now time.Time) GatewayDiagnostics {
	result := GatewayDiagnostics{ObservedAt: now}
	g.sessionsMu.RLock()
	result.AgentConnections = len(g.sessions)
	g.sessionsMu.RUnlock()
	g.pendingMu.Lock()
	for _, task := range g.pending {
		if !task.expires.After(now) {
			result.ExpiredResults++
		} else {
			result.PendingResults++
		}
	}
	g.pendingMu.Unlock()
	return result
}

// SubscriberCount counts active event subscriptions, independently of Agents.
func (h *Hub) SubscriberCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subscribers)
}
