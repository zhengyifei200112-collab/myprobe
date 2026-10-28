package agentclient

import (
	"context"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	v2 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v2"
)

func (c *Client) sendConfigurationApplied(ctx context.Context, expected *websocket.Conn, version int, configuration v2.RuntimeConfiguration) error {
	envelope, err := v2.NewEnvelope(v2.TypeConfigurationApplied, c.sequence.Add(1), v2.ConfigurationApplied(configuration))
	if err != nil {
		return err
	}
	envelope.Version = version
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.currentConnection() != expected {
		return context.Canceled
	}
	return wsjson.Write(writeCtx, expected, envelope)
}
