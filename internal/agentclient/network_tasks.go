package agentclient

import (
	"context"
	"time"

	check "github.com/zhengyifei200112-collab/myprobe/internal/protocol/networkcheck"
)

func (c *Client) startNetworkTask(ctx context.Context, task check.Task) {
	select {
	case c.taskSlots <- struct{}{}:
		go func() {
			defer func() { <-c.taskSlots }()
			result := defaultNetworkProbeExecutor().execute(ctx, task)
			c.enqueueNetworkTaskResult(ctx, task, result)
		}()
	default:
		c.enqueueNetworkTaskResult(ctx, task, check.Result{TaskID: task.ID, TargetID: task.TargetID, Kind: task.Kind, Outcome: check.Unobserved, ErrorClass: "busy", AddressFamily: check.FamilyUnknown, CompletedAt: time.Now().UTC()})
	}
}
