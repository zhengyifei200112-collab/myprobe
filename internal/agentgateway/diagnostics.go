package agentgateway

import "time"

// GatewayDiagnostics describes process-local transport state, not node health
// or a durable scheduler queue. Counts are sampled under their respective locks.
type GatewayDiagnostics struct {
	ObservedAt         time.Time `json:"observed_at"`
	AgentConnections   int       `json:"agent_connections"`
	PendingResults     int       `json:"pending_results"`
	ExpiredResults     int       `json:"expired_results"`
	HTTPResultsStatus  string    `json:"http_results_status"`
	HTTPPendingResults *int      `json:"http_pending_results,omitempty"`
	HTTPExpiredResults *int      `json:"http_expired_results,omitempty"`
	V1Connections      int       `json:"v1_connections"`
	V2Connections      int       `json:"v2_connections"`
	UnknownConnections int       `json:"unknown_connections"`
}

func (g *Gateway) Diagnostics() GatewayDiagnostics {
	return g.diagnosticsAt(time.Now().UTC())
}

// TryDiagnostics is for bounded export preparation. A busy global lock means
// unavailable evidence; it must not be reported as zero activity.
func (g *Gateway) TryDiagnostics() (GatewayDiagnostics, bool) {
	now := time.Now().UTC()
	result := GatewayDiagnostics{ObservedAt: now}
	if !g.sessionsMu.TryRLock() {
		return result, false
	}
	if !g.pendingMu.TryLock() {
		g.sessionsMu.RUnlock()
		return result, false
	}
	sessions := make([]*agentSession, 0, len(g.sessions))
	result.AgentConnections = len(g.sessions)
	for _, session := range g.sessions {
		switch session.version {
		case 2:
			result.V2Connections++
		case 1:
			result.V1Connections++
		default:
			result.UnknownConnections++
		}
		sessions = append(sessions, session)
	}
	g.sessionsMu.RUnlock()
	for _, task := range g.pending {
		if task.expires.After(now) {
			result.PendingResults++
		} else {
			result.ExpiredResults++
		}
	}
	g.pendingMu.Unlock()
	result.HTTPResultsStatus = "available"
	pending, expired := 0, 0
	for _, session := range sessions {
		if !session.httpMu.TryLock() {
			result.HTTPResultsStatus = "unavailable"
			continue
		}
		for _, expiry := range session.httpPending {
			if expiry.After(now) {
				pending++
			} else {
				expired++
			}
		}
		session.httpMu.Unlock()
	}
	if result.HTTPResultsStatus == "available" {
		result.HTTPPendingResults = &pending
		result.HTTPExpiredResults = &expired
	}
	return result, true
}
func (h *Hub) TrySubscriberCount() (int, bool) {
	if !h.mu.TryRLock() {
		return 0, false
	}
	defer h.mu.RUnlock()
	return len(h.subscribers), true
}

func (g *Gateway) diagnosticsAt(now time.Time) GatewayDiagnostics {
	result := GatewayDiagnostics{ObservedAt: now}
	g.sessionsMu.RLock()
	result.AgentConnections = len(g.sessions)
	sessions := make([]*agentSession, 0, len(g.sessions))
	for _, session := range g.sessions {
		if session.version == 2 {
			result.V2Connections++
		} else if session.version == 1 {
			result.V1Connections++
		} else {
			result.UnknownConnections++
		}
		sessions = append(sessions, session)
	}
	g.sessionsMu.RUnlock()
	result.HTTPResultsStatus = "available"
	httpPending, httpExpired := 0, 0
	for _, session := range sessions {
		// Dispatch holds this mutex over IO. Polling must not wait or invent
		// zero counts when a complete observation cannot be acquired.
		if !session.httpMu.TryLock() {
			result.HTTPResultsStatus = "unavailable"
			continue
		}
		for _, expiry := range session.httpPending {
			if expiry.After(now) {
				httpPending++
			} else {
				httpExpired++
			}
		}
		session.httpMu.Unlock()
	}
	if result.HTTPResultsStatus == "available" {
		result.HTTPPendingResults, result.HTTPExpiredResults = &httpPending, &httpExpired
	}
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

func (g *Gateway) NodeConnected(nodeID string) bool {
	g.sessionsMu.RLock()
	defer g.sessionsMu.RUnlock()
	_, connected := g.sessions[nodeID]
	return connected
}
