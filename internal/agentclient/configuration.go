package agentclient

import (
	"context"
	"time"

	"github.com/coder/websocket"
	v2 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v2"
)

func (c *Client) sendConfigurationApplied(ctx context.Context, expected *websocket.Conn, version int, configuration v2.RuntimeConfiguration) error {
	sequence := c.nextSequence(v2.TypeConfigurationApplied)
	envelope, err := v2.NewEnvelope(v2.TypeConfigurationApplied, sequence, v2.ConfigurationApplied(configuration))
	if err != nil {
		c.observeEnvelopeRejection(v2.TypeConfigurationApplied, sequence, "encoding")
		return err
	}
	envelope.Version = version
	writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.currentConnection() != expected {
		c.observeEnvelopeRejection(envelope.Type, envelope.Sequence, "canceled")
		return context.Canceled
	}
	return c.writeV2Envelope(writeCtx, expected, envelope)
}
