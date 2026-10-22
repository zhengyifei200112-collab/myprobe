package agentclient

import (
	"context"
	"time"

	"github.com/coder/websocket"
	check "github.com/zhengyifei200112-collab/myprobe/internal/protocol/networkcheck"
	v2 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v2"
)

func (c *Client) startNetworkTask(ctx context.Context, connection *websocket.Conn, task check.Task) {
	select {
	case c.taskSlots <- struct{}{}:
		go func() {
			defer func() { <-c.taskSlots }()
			result := defaultNetworkProbeExecutor().execute(ctx, task)
			c.sendNetworkTaskResult(ctx, connection, result)
		}()
	default:
		c.sendNetworkTaskResult(ctx, connection, check.Result{TaskID: task.ID, TargetID: task.TargetID, Kind: task.Kind, Outcome: check.Unobserved, ErrorClass: "busy", AddressFamily: check.FamilyUnknown, CompletedAt: time.Now().UTC()})
	}
}
func (c *Client) sendNetworkTaskResult(ctx context.Context, connection *websocket.Conn, result check.Result) {
	if ctx.Err() != nil || result.Validate(time.Now().UTC()) != nil {
		return
	}
	sequence := c.nextSequence(v2.TypeNetworkResult)
	envelope, err := v2.NewEnvelope(v2.TypeNetworkResult, sequence, result)
	if err != nil {
		c.observeEnvelopeRejection(v2.TypeNetworkResult, sequence, "encoding")
		return
	}
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	c.writeMu.Lock()
	err = c.writeV2Envelope(writeCtx, connection, envelope)
	c.writeMu.Unlock()
	if err != nil {
		c.clearConnection(connection)
	}
}
