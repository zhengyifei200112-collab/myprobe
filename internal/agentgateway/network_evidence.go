package agentgateway

import (
	"context"
	"time"

	check "github.com/zhengyifei200112-collab/myprobe/internal/protocol/networkcheck"
)

// The server enables this after initializing the current Store schema. Existing
// connections must reconnect; capabilities never change mid-session.
func (g *Gateway) EnableNetworkProbes(enabled bool) { g.networkEnabled.Store(enabled) }
func (g *Gateway) NetworkProbesEnabled() bool       { return g.networkEnabled.Load() }

func (g *Gateway) saveNetworkProbeResult(ctx context.Context, session *agentSession, node string, result check.Result) (bool, error) {
	if !session.networkAllowed || !g.isCurrentSession(node, session) {
		return false, ErrNetworkUnavailable
	}
	return g.store.AcceptNetworkProbeResult(ctx, node, session.epoch, result, time.Now().UTC())
}
