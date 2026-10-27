package agentclient

import (
	"context"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
	v2 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v2"
)

type httpExecutor interface {
	Execute(context.Context, httpcheck.Task) httpcheck.Result
}

func (c *Client) startHTTPTask(ctx context.Context, connection *websocket.Conn, task httpcheck.Task) {
	select {
	case c.httpTaskSlots <- struct{}{}:
		go func() {
			defer func() { <-c.httpTaskSlots }()
			result := c.httpExecutor.Execute(ctx, task)
			c.sendHTTPTaskResult(ctx, connection, result)
		}()
	default:
		c.sendHTTPTaskResult(ctx, connection, httpcheck.Result{TaskID: task.ID, ServiceID: task.ServiceID, Revision: task.Revision, ScheduledAt: task.ScheduledAt, CompletedAt: time.Now().UTC(), Outcome: "unobserved", ErrorClass: "busy"})
	}
}

func (c *Client) sendHTTPTaskResult(ctx context.Context, connection *websocket.Conn, result httpcheck.Result) {
	if ctx.Err() != nil {
		return
	}
	envelope, err := v2.NewEnvelope(v2.TypeHTTPResult, c.sequence.Add(1), result)
	if err != nil {
		return
	}
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	c.writeMu.Lock()
	err = wsjson.Write(writeCtx, connection, envelope)
	c.writeMu.Unlock()
	if err != nil {
		c.clearConnection(connection)
	}
}
